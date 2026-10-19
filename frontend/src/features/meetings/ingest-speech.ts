import { unref, type MaybeRef } from 'vue'
import { sttApi } from '../../api/stt'
import { extractEmbeddingResult } from '../../native/speaker-embedding'
import { detectLang } from '../../native/detect-lang'
import { saveAudioPart } from './audio-parts'
import { liveTranslate } from './live-translate'
import { updateSegmentTranslation } from './meetings-live'
import { saveSegment, updateTranscript } from './meetings-store'
import type { SpeakerDiarizer } from '../../native/speaker-diarization'
import { renderTranscript } from './meeting-dedup.ts'
import { describeSegmentDrop } from './segment-drop-notice.ts'
import type { MeetingSegment } from './meetings-store.ts'

export async function ingestSpeechBlob(opts: {
  meetingId: MaybeRef<string>
  blob: Blob
  startMs: number
  endMs: number
  seq: number
  diarizer: SpeakerDiarizer
  segments: MeetingSegment[]
  segmentProfiles: Map<string, string>
  signal?: AbortSignal
}): Promise<MeetingSegment | null> {
  const meetingId = unref(opts.meetingId)
  if (!meetingId) return null
  // signal 一路传到 sttApi.transcribe：会议录音时每个语音块都是一次独立转写
  // （各自 3 分钟预算），此前这一层是整条录音链上唯一**既跨页存活又完全无法
  // 终止**的环节——录音 runtime 的 cancelTranscription() 只能停录完后的兜底
  // 全量转写，停不了这些分片。
  //
  // 中止不在这里吞：Promise.all 会把 AbortError 原样抛给调用方，由
  // processSegment 判断「是取消」还是「真失败」——真失败要给用户可行动的原因，
  // 取消则一个字都不该显示。
  const [sttResult, embeddingResult] = await Promise.all([
    sttApi.transcribe({ audioBlob: opts.blob }, opts.signal),
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
  void saveAudioPart(meetingId, opts.seq, opts.blob).catch((e) => {
    // §818/§819 同一族：观测失败不得影响转写，但也不许完全无声 ——
    // 这里是唯一一份音频拷贝，它没存下来就等于那段话永久找不回来了。
    console.warn(`[ingest-speech] 第 ${opts.seq} 段音频分片落盘失败：`, e)
  })

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
  const id = await saveSegment(seg)
  opts.segmentProfiles.set(id, profileId)
  const saved: MeetingSegment = { id, ...seg }
  // 按 startMs 有序插入：分段并发转写，完成顺序 ≠ 说话顺序，
  // 直接 push 会让 transcript 与实时列表乱序。
  let at = opts.segments.length
  while (at > 0 && opts.segments[at - 1].startMs > saved.startMs) at--
  opts.segments.splice(at, 0, saved)
  // ★ 相邻段去重（2026-10-06）。VadSegmenter 的取片窗口带 sliceMs 余量以
  //   补偿 MediaRecorder 缓冲延迟，RAF 节流又会让语音起点判定回退，
  //   相邻两段因此可能取到重叠音频并被转写两次 ⇒ 文本重复。
  //   整条前端链路此前**完全没有**去重（后端 mergeIncremental 属于
  //   IncrementalTranscriber，前端从不调用它），所以必须在这一层消解。
  await updateTranscript(meetingId, renderTranscript(opts.segments))

  void liveTranslate(saved.text, lang)
    .then((translation) => {
      if (!translation) return
      saved.translation = translation
      return updateSegmentTranslation(id, translation)
    })
    .catch(() => {})
  return saved
}
