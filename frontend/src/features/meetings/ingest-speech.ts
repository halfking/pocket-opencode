import { captureRecordingOperation, type RecordingOperation } from './recording-operation'
import { unref, type MaybeRef } from 'vue'
import { sttApi } from '../../api/stt'
import { extractEmbeddingResult } from '../../native/speaker-embedding'
import { detectLang } from '../../native/detect-lang'
import { saveAudioPart } from './audio-parts'
import { liveTranslate } from './live-translate'
import { updateSegmentTranslation } from './meetings-live'
import { saveSegment, updateTranscript } from './meetings-store'
import type { SpeakerDiarizer } from '../../native/speaker-diarization'
import { renderTranscript, type TranscriptRenderer } from './meeting-dedup.ts'
import { describeSegmentDrop } from './segment-drop-notice.ts'
import type { MeetingSegment } from './meetings-store.ts'
import { assertCloudModule } from '../../api/data-policy.ts'
import { isAbortError } from '../../api/abort.ts'

/**
 * 分片落盘 URI → sherpa 原生层的裸路径。
 *
 * `saveAudioPart` 存的是 `Filesystem.getUri` 的产物（`file:///data/...`），
 * 而 SherpaPlugin 用 `new File(audioPath)` 解析 —— 不剥前缀会拿到
 * 「文件不存在」（URI 不是合法的 Linux 路径）。
 * mimeType 只用于注释追踪：Android 插件构造 Blob 时可能丢失 MIME，
 * 因此原生落盘路径统一交给 Sherpa 自校验。
 */
export function nativePartPathForSherpa(filePath: string | null, mimeType: string): string | undefined {
  // 2026-11-14（第六层修复）：Android 原生录音产出 PCM/WAV，但 Blob.type 可能
  //   是 'audio/webm' 或空字符串；只要存在原生 filePath 就交给 Sherpa 自校验。
  if (!filePath) return undefined
  return filePath.replace(/^file:\/\//, '')
}

export async function ingestSpeechBlob(opts: {
  meetingId: MaybeRef<string>
  blob: Blob
  startMs: number
  endMs: number
  seq: number
  diarizer: SpeakerDiarizer
  segments: MeetingSegment[]
  /**
   * §931：增量渲染器（**生产调用方必须传**）。
   *
   * ⚠ 不传时退回「每段整场重渲染」—— 行为**正确**但成本是 O(n²)
   *   ⇒ 生产上必须传（`recordingRuntime.processSegment` 传的是它自己那个）。
   */
  transcriptRenderer?: TranscriptRenderer
  segmentProfiles: Map<string, string>
  signal?: AbortSignal
  operation?: RecordingOperation
}): Promise<MeetingSegment | null> {
  const meetingId = unref(opts.meetingId)
  if (!meetingId) return null
  const operation = opts.operation ?? captureRecordingOperation('meetings')
  operation.assertLocal()
  // Android 分片可能是唯一副本。隐私拒绝和任何 STT 失败之前先确认落盘。
  //
  // ⚠ 2026-10-29（审计发现）：这一行此前被改成**裸 await**，两个后果：
  //   ① 分片落盘失败 ⇒ 整个 ingestSpeechBlob 抛出去 ⇒ **转写整段丢失**，
  //      而失败原因（磁盘满 / 权限 / 库写不进去）**一个字都没留下**；
  //   ② 取消（AbortError）也会被当成普通失败一起吞掉 ⇒ 用户点了停止，
  //      而录音 runtime 还在等一个永远不会回来的 promise。
  //   ⇒ 恢复「失败要出声」的语义（§818/§819 同一族），但**取消不许被就地消化**：
  //     AbortError 原样抛出，由调用方 processSegment 判断「是取消」还是「真失败」。
  // ⚠ 2026-11-14（层3修复）：分片落盘返回的 filePath 现在接进转写 opts ——
  //   这是「本地 sherpa 优先」从 stt.ts:94 的分支真正通电的那一步：
  //   之前只传 Blob ⇒ 本地分支因 `opts.audioPath` 缺失永远被跳过 ⇒
  //   云端 STT 一挂，整场转写全灭（生产 pocketd 未配 GROQ key 的实测现场）。
  //   路径只对 WAV 分片传（Web 链的 webm 传给 sherpa 是必败调用）。
  let savedPartPath: string | undefined
  try {
    const saved = await saveAudioPart(meetingId, opts.seq, opts.blob, opts.blob.type || 'audio/webm', operation.localDb)
    savedPartPath = nativePartPathForSherpa(saved.filePath, opts.blob.type || 'audio/webm')
  } catch (error) {
    if (isAbortError(error)) throw error
    // 观测失败不得影响转写，但也不许完全无声 ——
    // 这里是唯一一份音频拷贝，它没存下来就等于那段话永久找不回来了。
    console.warn(`[ingest-speech] 第 ${opts.seq} 段音频分片落盘失败：`, error)
  }
  // ⚠ 2026-11-14 真机实测（第四层故障·二次修复）：assertCloudModule 移走后
  //   还有**第三个拦截点** —— transcribe 的第三参 assertCurrent 这里传的是
  //   operation.assertCloud，而 stt.ts:96 第一行就调 assertCurrent?.() ⇒
  //   local 模式下本地分支一个字都没转就被弹死（logcat 实证：Sherpa.transcribe
  //   调用数为 0，只有 extractEmbedding）。⇒ assertCurrent 传 operation.assertLocal
  //   （它才是「这段转写还有效」的判据 —— 云端归属的校验已由 stt.ts 内部
  //   在云端路径自行处理，本地路径不需要）。
  // signal 一路传到 sttApi.transcribe：会议录音时每个语音块都是一次独立转写
  // （各自 3 分钟预算），此前这一层是整条录音链上唯一**既跨页存活又完全无法
  // 终止**的环节——录音 runtime 的 cancelTranscription() 只能停录完后的兜底
  // 全量转写，停不了这些分片。
  //
  // 中止不在这里吞：Promise.all 会把 AbortError 原样抛给调用方，由
  // processSegment 判断「是取消」还是「真失败」——真失败要给用户可行动的原因，
  // 取消则一个字都不该显示。
  const [sttResult, embeddingResult] = await Promise.all([
    sttApi.transcribe({ audioBlob: opts.blob, audioPath: savedPartPath, module: 'meetings' }, opts.signal),
    // ⚠ 旧写法 `.catch(() => new Float32Array())` 把「提取失败」伪装成
    //   一个合法的空向量；而余弦相似度对零向量恒为 0 < 阈值
    //   ⇒ **每一次提取失败都被算成一个新说话人**，用户看到的是
    //   「说话人 1…30」而其实只有他一个人在说话。
    //   现在失败是显式的 null，由 SpeakerDiarizer 走整场共用的兜底标签。
    extractEmbeddingResult(opts.blob),
  ])
  // ⚠⚠⚠ §828：**音频分片必须先于「转写有没有出字」落盘。**
  //
  //   旧顺序是「先判空文本 → return null → 才 saveAudioPart」⇒ 转写无字的那一段，
  //   **文字没建行、音频分片也没落盘、没有任何提示**，两样都没了。
  //   而 `audio-parts.ts` 的头注释写的就是「崩溃后可凭 local_meeting_audio_parts
  //   恢复转写」—— 既然分片的用途就是**事后补救**，那它在补救之前就不能先被跳过。
  //   ⚠§826 让这件事在 Android 上变得不可逆：原生模式压根不产整段音频，
  //   分片是那段话的**唯一一份拷贝**。

  // ⚠ 2026-11-14（与上方第四层修复同族）：本地模式下转写结果来自本地引擎，
  //   这里不能再用 assertCloudModule 把它拦掉；落库分两条腿：
  //   云端可用 ⇒ 云库 saveSegment/updateTranscript；本地模式 ⇒ 本地库。
  //   captureRecordingOperation 的 db 句柄按模式分发（operation.cloudDb 在
  //   local 模式下是本地句柄的别名），因此这里只需放行、无需选择。
  if (!sttResult.text.trim()) {
    // 「转写成功但没出字」与「转写失败」在调用点**长得一样**（都不抛错），
    // 而前者更隐蔽：它连 sttError 都不会触发。不留痕 ⇒ 这段话凭空消失。
    console.warn(describeSegmentDrop({
      seq: opts.seq, startMs: opts.startMs, endMs: opts.endMs, engine: sttResult.engine,
    }))
    return null
  }

  const { profileId, label } = opts.diarizer.identify(
    embeddingResult.ok ? embeddingResult.embedding : null,
  )
  const lang = detectLang(sttResult.text)
  const seg: Omit<MeetingSegment, 'id'> = {
    meetingId,
    speakerLabel: label,
    lang,
    confidence: sttResult.confidence,
    startMs: opts.startMs,
    endMs: opts.endMs,
    text: sttResult.text.trim(),
    // §132：这是 ASR 的产物，因此**享受**相邻去重（切片重叠是它的老毛病）。
    // 显式写出来而不是留空 —— `origin` 是必填字段，缺了编译器会指到这里。
    origin: 'asr',
  }
  // ⚠ 2026-11-14（第四层修复·落库）：cloudDb 只在模块模式为 cloud 时存在
  //   （recording-operation.ts:28）。本地模式下它是 null —— 旧代码的
  //   `operation.cloudDb!` 非空断言在 local 模式下把 null 递进 saveSegment
  //   ⇒ ownerOf(null) TypeError。本地转写出的段落必须落**本地库**
  //   （operation.localDb 与分片落盘同一个租约/归属）。
  //   ⚠ cloud 模式（cloudDb 非 null）走原句柄不动：local-edit 与 cloud-sync
  //     是两种租约，assertCurrent 的判据不同（后者带 policy epoch 校验），
  //     不能混用 —— 所以下面的 `??` 只在 cloudDb === null（local 模式）时
  //     才会取 localDb，cloud 模式的落库语义一字未变。
  const segmentDb = operation.cloudDb ?? operation.localDb
  const id = await saveSegment(seg, segmentDb)
  operation.assertLocal()
  opts.segmentProfiles.set(id, profileId)
  const saved: MeetingSegment = { id, ...seg }
  // 按 startMs 有序插入：分段并发转写，完成顺序 ≠ 说话顺序，
  // 直接 push 会让 transcript 与实时列表乱序。
  let at = opts.segments.length
  while (at > 0 && opts.segments[at - 1].startMs > saved.startMs) at--
  opts.segments.splice(at, 0, saved)
  // ⚠⚠ §931：增量渲染 —— 必须**紧跟 splice**（中间不能有 await）。
  //   ⇒ 否则并发转写的另一段可能插进来，`at` 就过期了。
  //   ⚠ 插到**中间**时（at !== 已渲染段数）渲染器会整场重算 ⇒ 正确性不依赖调用方。
  const renderer = opts.transcriptRenderer
  if (renderer) renderer.appendAt(opts.segments, at)
  // ★ 相邻段去重（2026-10-06）。VadSegmenter 的取片窗口带 sliceMs 余量以
  //   补偿 MediaRecorder 缓冲延迟，RAF 节流又会让语音起点判定回退，
  //   相邻两段因此可能取到重叠音频并被转写两次 ⇒ 文本重复。
  //   整条前端链路此前**完全没有**去重（后端 mergeIncremental 属于
  //   IncrementalTranscriber，前端从不调用它），所以必须在这一层消解。
  // ⚠ §925：`throttled` —— 这是**逐段追加**那条路径（Web 链），声明「我可以晚点落库」
  //   ⇒ 8 小时会议的转写落库量从 376.3 MB 降到 2.4 MB（纯写中位 17.3 s → 1.44 s）。
  //   ⚠ 节流是**显式 opt-in**：不声明的写入点一律立刻落库（安全的那一侧）。
  //   ⚠ 收尾会**整篇补齐**一次（`recordingRuntime.stop()`）⇒ 会议结束时这一列是完整的。
  // ⚠⚠ §931：传惰性源 ⇒ **正文只在真要落库时**才渲染。
  //   以前这里是 `renderTranscript(opts.segments)`（当场整场渲染）⇒ 每段一次 O(n)，
  //   而那次结果绝大多数被节流直接丢弃（8 小时真正落库 13 次）⇒ 纯浪费。
  // ⚠ 这一句**必须写在一行** ⇒ 门禁 J（§442）按**行**扫 updateTranscript 的调用点。
  await updateTranscript(meetingId, renderer ? renderer.source() : renderTranscript(opts.segments), { throttled: true, db: segmentDb })

  void liveTranslate(saved.text, lang, operation.assertCloud)
    .then((translation) => {
      operation.assertCloud()
      if (!translation) return
      saved.translation = translation
      return updateSegmentTranslation(id, translation, segmentDb)
    })
    .catch((e) => {
      // 翻译没回来（或译文写库失败）⇒ 这一段没有译文，用户侧只表现为「没翻译」。
      // 没有别处会提示 ⇒ 留痕。
      console.warn('[meetings] 实时翻译未能落库：', e)
    })
  return saved
}
