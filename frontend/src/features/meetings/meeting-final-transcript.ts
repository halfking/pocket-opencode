/**
 * meeting-final-transcript.ts — 收尾**全量重转**（两阶段转写的第二阶段）。
 *
 * ── 为什么需要这一步（2026-10-06，用户实测反馈「识别错误率高」）──
 *
 * 录音管线是「短段切片 + 逐段转写」，每段约 8 秒。短段的固有问题是
 * **没有上下文**：ASR 看不到前后文，所以
 *   - 专有名词/人名/产品名（同音字）错得最厉害；
 *   - 数字、金额、日期听错；
 *   - 断句在切片边界被切断。
 *
 * 这些错误在收尾时**无法靠「润色文本」修好**——LLM 只能看到已经错了的字符，
 * 它不知道音频里原本说的是什么。所以修复必须回到**音频**。
 *
 * 这正是 FunASR 官方部署矩阵里的两阶段模式（先流式出字，再用离线模型
 * 修正整段），也是讯飞听见/Otter/Granola 的共同架构。
 *
 * ── 为什么复用 transcribeFull 而不是自己上传 ──
 *
 * ⚠️ 本文件第一版是「把 IndexedDB 里的 webm blob 直接 POST 给
 * /api/meetings/{id}/transcribe」。那是**错的**，两条硬限制都会让它失败：
 *
 *   1. 上游单次时长上限：智谱 30s / OpenRouter ~60s / MiniMax 500s
 *      （backend/internal/stt/target.go 的 MaxSeconds）。超过 8 分 20 秒的
 *      会议**必然失败**——而长会议恰恰是最需要精校的场景。
 *   2. 格式：真机录的是 webm，网关只收 mp3/wav（recording-audio-transcode.ts
 *      头注释有完整实测）。
 *
 * 而 `sttSettingsApi.transcribeFull` 已经把这三件事都做完了：
 *   - 服务端按**静音边界自动切段**、逐段转写、按序聚合（长录音不用前端切）；
 *   - 返回 {succeeded, failed}，让「有 N 段没转出来」能被告知用户；
 *   - 笔记录音路径（recordingRuntime.ts:1042 起）已在用，注释明确写着
 *     「换成 transcribeFull 是因为任何 ASR 都不允许无限长音频单次上传」。
 *
 * 也就是说：会议场景缺的不是能力，是**接线**。本模块只做接线。
 *
 * ── 失败处理 ──
 *
 * 全量重转是**增强**而非必需：失败时保留分段文本，录音照常结束。
 * 绝不能让「精校」把用户的录音结果搞丢。
 */
import { loadMeetingAudio } from '../../native/meeting-audio.ts'
import { filenameForMimeType } from '../../api/stt-filename.ts'
import type { MeetingSegment } from './meetings-store'

export interface FinalTranscribeResult {
  /** 高精度整段文本；失败时为 null。 */
  text: string | null
  /** 是否真的跑了全量重转（false = 降级，沿用分段文本）。 */
  applied: boolean
  /** 降级原因，写日志用。 */
  reason?: string
  /** 有多少段没转出来（transcribeFull 的 failed 计数）。 */
  failedSegments?: number
  /** 由调用方注入：把 webm blob 转成 16k WAV。
   *  录音 runtime 手上有 RollingWebmDecoder（它已经把整段解码过一次），
   *  复用它比这里再解一次省一大截 CPU；拿不到时传 null 退回原 blob。 */
  toWav?: (blob: Blob) => Promise<ArrayBuffer | null>
}

/**
 * 取回录音音频并做整段高精度重转。
 *
 * @param meetingId 会议 id（对应 IndexedDB 里的录音）
 * @param transcribeFull 注入的转写调用（sttSettingsApi.transcribeFull）。
 *        注入而非直接 import：api/stt-settings 会拖进 http → auth store，
 *        那些在 node 环境（门禁测试）解析不了。本模块的判定逻辑
 *        shouldAdoptFullTranscript 必须能脱离浏览器单测，所以 I/O 一律外置。
 */
export async function refetchFullTranscript(
  meetingId: string,
  transcribeFull: (blob: Blob, filename: string) => Promise<{ text: string; failed: number }>,
  opts?: { toWav?: (blob: Blob) => Promise<ArrayBuffer | null> },
): Promise<FinalTranscribeResult> {
  if (!meetingId) return { text: null, applied: false, reason: 'no-meeting-id' }

  let audioUrl: string | null = null
  try {
    audioUrl = await loadMeetingAudio(meetingId)
  } catch (e) {
    return { text: null, applied: false, reason: `audio-load-failed: ${msg(e)}` }
  }
  if (!audioUrl) {
    // 录音没落盘（老版本录音、存储配额、用户提前杀进程）⇒ 沿用分段文本。
    return { text: null, applied: false, reason: 'audio-not-found' }
  }

  try {
    const blob = await (await fetch(audioUrl)).blob()
    if (!blob.size) return { text: null, applied: false, reason: 'audio-empty' }

    // 网关只收 mp3/wav，真机录的是 webm —— 必须转码。
    // 决策逻辑在 resolveUploadTarget（纯函数，可单测），这里只负责取 wav。
    let wav: ArrayBuffer | null = null
    if (opts?.toWav) {
      try {
        wav = await opts.toWav(blob)
      } catch {
        // 转码失败不致命，继续用原 blob。
        wav = null
      }
    }
    const target = resolveUploadTarget(blob, wav)

    const res = await transcribeFull(target.blob, target.filename)
    const text = String(res?.text ?? '').trim()
    if (!text) return { text: null, applied: false, reason: 'empty-transcript' }
    return {
      text,
      applied: true,
      // 有段失败必须透出，否则用户以为记录是完整的。
      ...(res.failed > 0 ? { failedSegments: res.failed } : {}),
    }
  } catch (e) {
    return { text: null, applied: false, reason: `transcribe-failed: ${msg(e)}` }
  } finally {
    // loadMeetingAudio 返回的是 objectURL，用完必须释放，否则长录音会
    // 一直占着内存（会议音频动辄几十 MB）。
    if (audioUrl) {
      try { URL.revokeObjectURL(audioUrl) } catch { /* ok */ }
    }
  }
}

/**
 * 决定「上传给 transcribeFull 的那个 blob 是什么」——纯函数，可直接单测。
 *
 * 为什么要抽出来：`refetchFullTranscript` 内部第一件事就是
 * `loadMeetingAudio`（浏览器 IndexedDB），在 node 里必然失败，所以整条
 * 链路在单测中只能验到「降级不抛」那一步，**转码分支永远测不到**。
 * 把这步决策抽成纯函数，才有一个真的能被变异打红的判据。
 *
 * @param wav toWav 的返回值；null/undefined 或抛错 ⇒ 退回原始 blob
 * @return 上传用的 blob 与 filename
 */
export function resolveUploadTarget(
  raw: Blob,
  wav: ArrayBuffer | null,
): { blob: Blob; filename: string } {
  if (!wav) {
    // 转码失败/未提供：退回原始 blob。服务端的 webm 拒收是既有行为，
    // 不比「因为转码挂了所以什么都不发」更差，且保留了错误可见性。
    // baseName 传 'meeting'：filenameForMimeType 默认产 'recording.<ext>'，
    // 直接拼会得到 'meetingrecording.webm'（2026-10-06 实测）。
    return { blob: raw, filename: filenameForMimeType(raw.type, 'meeting') }
  }
  return { blob: new Blob([wav], { type: 'audio/wav' }), filename: 'meeting.wav' }
}

function msg(e: unknown): string {
  return e instanceof Error ? e.message : String(e)
}

/**
 * 判断「整段重转结果是否比分段累积文本更值得采用」。
 *
 * ★ 为什么要这一步，而不是无脑用重转结果替换：
 *   全量重转**不保证**比分段结果好——如果用户中途在设置里换了更差的模型/
 *   通道，或者长音频切段时大量段落失败，重转可能更短更差。所以要比较：
 *     1. 长度不能显著变短（掉一半以上说明大概率有大量段失败）；
 *     2. 空文本直接拒绝。
 *   两者任一不满足就沿用分段文本，宁可保持原样也不要让用户看到内容变少。
 *
 * @param full  整段重转文本
 * @param segs  分段结果
 */
export function shouldAdoptFullTranscript(
  full: string,
  segs: Pick<MeetingSegment, 'text'>[],
): { adopt: boolean; reason: string } {
  const fullClean = full.trim()
  if (!fullClean) return { adopt: false, reason: 'full-empty' }
  const segText = segs.map((s) => s.text ?? '').join('').trim()
  if (!segText) return { adopt: true, reason: 'segments-empty' }
  // 明显变短 ⇒ 多半大量段失败或模型降级。
  if (fullClean.length < segText.length * 0.5) {
    return { adopt: false, reason: `full-too-short(${fullClean.length}<${Math.floor(segText.length * 0.5)})` }
  }
  return { adopt: true, reason: 'ok' }
}
