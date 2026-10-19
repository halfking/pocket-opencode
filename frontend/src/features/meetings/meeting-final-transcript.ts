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
import {
  mergeChunkedOutcomes, planWavChunks, sliceWavChunk, type ChunkedTranscribePart,
} from './meeting-full-transcribe-chunks.ts'
import type { MeetingSegment } from './meetings-store'

/**
 * 全量重转的单段回执，**只声明本模块真正用到的字段**。
 *
 * ⚠ 刻意不 import `SttFullSegment`（api/stt-settings）：那份类型所在的模块
 * 会拖进 http → auth store，node 环境解析不了。本文件既有的约定就是
 * 「I/O 一律外置、纯函数必须能脱离浏览器单测」，这里遵守同一条。
 *
 * ★ 它同时是一个**证据**：后端 `TranscribeFull` 早就逐段返回了
 * `[{index,startSec,endSec,text,error}]`（server_stt_stream.go:98），前端
 * 类型 `SttFullResult.segments` 也早就声明了 —— 但下面 `transcribeFull` 的
 * 注入签名把它结构化地抹成了 `{text, failed}`，于是字段在**接缝处**消失，
 * 上层拿不到、也没人发现。见 `attributeFullTranscript` 的头注释。
 */
export interface FullSegmentReceipt {
  index: number
  startSec: number
  endSec: number
  text?: string
  error?: string
}

/** transcribeFull 的注入契约。返回 segments 才能保住说话人归属。 */
export interface FullTranscribeOutcome {
  text: string
  failed: number
  /** 逐段回执；老调用方/老后端可能没有，那时走单段降级。 */
  segments?: FullSegmentReceipt[] | null
  /**
   * 服务端返回的错误信息（如「全量转写没有任何有效文本（5 段全部失败…）」）。
   *
   * ★ 为什么需要它（§927）：服务端在「一次成功段都没有」时返回
   *   `{ok:false, error:…}`（HTTP 仍是 200）⇒ 客户端**拿不到任何内容**。
   *   而「整块都是纯静音」与「整块真失败」**服务端用同一形态返回**，
   *   只有 error 文案能区分（`backend/internal/stt/full.go:506` 的「纯静音」提示）。
   *   ⇒ 没有它，纯静音的块会被当成失败报给用户（「有 N 段没转出来」，其实没失败）。
   */
  error?: string
}

export interface FinalTranscribeResult {
  /** 高精度整段文本；失败时为 null。 */
  text: string | null
  /** 是否真的跑了全量重转（false = 降级，沿用分段文本）。 */
  applied: boolean
  /** 降级原因，写日志用。 */
  reason?: string
  /**
   * ★§818：转码这一步**失败过**（`toWav` 抛错），不是「没传 toWav」。
   *
   * ⚠⚠ 为什么必须把它与 `reason` 分开：`reason` 描述的是**最终结果**
   * （`empty-transcript` / `transcribe-failed: …`），而转码失败的后果要再经过
   * 「退回原 webm 上传 → 网关只收 wav/mp3 → 拒收」才浮现 ⇒ 到了 `reason` 那里
   * **根因已经丢了**，只剩一个下游症状。
   *
   * 此前这个信息在 `:144` 的空 catch 块里被整个吞掉：转码失败 ⇒ 退回原 blob ⇒
   * 网关拒收 ⇒ `empty-transcript`，而唯一生产调用方 `finalizeRecording`
   * 一个 `reason` 字段都不读 ⇒ **整条失败链在生产里没有任何出口**。
   */
  transcodeFailed?: boolean
  /** 转码失败的原始异常信息，便于定位是解码失败还是缺 AudioContext。 */
  transcodeError?: string
  /** 有多少段没转出来（transcribeFull 的 failed 计数）。 */
  failedSegments?: number
  /** ★ 逐段回执：带时间范围，是**唯一**能把高精度文本重新挂回说话人的依据。 */
  segments?: FullSegmentReceipt[]
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
  transcribeFull: (blob: Blob, filename: string) => Promise<FullTranscribeOutcome>,
  opts?: {
    toWav?: (blob: Blob) => Promise<ArrayBuffer | null>
    /**
     * 测试注入：直接给出音频 blob，绕过 `loadMeetingAudio`（IndexedDB +
     * objectURL 在 node 里都没有）。
     *
     * ★ 为什么值得开这个口子（§50）：此前这条接缝在单测里**永远**只能验到
     *   「降级不抛」那一步 —— 于是「传没传 toWav」「转码有没有真的发生」
     *   两件决定真机成败的事，只能靠人读源码发现。
     *   §50 的 webm 400 就是这么漏了四轮。给了这个口子之后，
     *   「webm 进、wav 出」是**可执行**的断言，不再是阅读理解题。
     */
    loadAudio?: (meetingId: string) => Promise<Blob | null>
    /**
     * §927：整场重转的**分块长度**（秒）。不传 = 生产默认值
     * （`FULL_TRANSCRIBE_CHUNK_SECONDS` = 8 分钟，取值依据见
     * `meeting-full-transcribe-chunks.ts` 的文件头）。
     *
     * ⚠ 这是一个**注入点**，不是设置项：要不要暴露给用户属产品口径
     *   （简报第 151 条待拍板），本处不代拍。
     */
    chunkSeconds?: number
  },
): Promise<FinalTranscribeResult> {
  if (!meetingId) return { text: null, applied: false, reason: 'no-meeting-id' }

  let audioUrl: string | null = null
  let injected: Blob | null = null
  try {
    if (opts?.loadAudio) injected = await opts.loadAudio(meetingId)
    else audioUrl = await loadMeetingAudio(meetingId)
  } catch (e) {
    return { text: null, applied: false, reason: `audio-load-failed: ${msg(e)}` }
  }
  if (!audioUrl && !injected) {
    // 录音没落盘（老版本录音、存储配额、用户提前杀进程）⇒ 沿用分段文本。
    return { text: null, applied: false, reason: 'audio-not-found' }
  }

  // ★§818：转码失败**必须留痕**。此前这里是空 catch 块，失败被整个吞掉，
  // 而它的后果（退回原 webm → 网关拒收）在 `reason` 里只呈现为 `empty-transcript`
  // ⇒ 根因丢失，且唯一生产调用方 `finalizeRecording` 不读 `reason` ⇒ 全链路静默。
  // 声明刻意放在 try **之外**：转码失败后可能一路走到外层 catch（连上传都抛了），
  // 那里同样要透出，否则「上传失败」会把「转码失败」这个更早的根因盖掉。
  let transcodeFailed = false
  let transcodeError = ''
  /** 转码痕迹的统一展开形状；没失败时是空对象（不改变任何现有返回的形状）。 */
  const tc = () => (transcodeFailed ? { transcodeFailed: true, transcodeError } : {})

  try {
    const blob = injected ?? (await (await fetch(audioUrl!)).blob())
    if (!blob.size) return { text: null, applied: false, reason: 'audio-empty' }

    // 网关只收 mp3/wav，真机录的是 webm —— 必须转码。
    // 决策逻辑在 resolveUploadTarget（纯函数，可单测），这里只负责取 wav。
    let wav: ArrayBuffer | null = null
    if (opts?.toWav) {
      try {
        wav = await opts.toWav(blob)
      } catch (e) {
        // 转码失败不致命，继续用原 blob —— **降级行为完全不变**，只是让失败可见。
        wav = null
        transcodeFailed = true
        transcodeError = msg(e)
      }
    }
    const target = resolveUploadTarget(blob, wav)

    // ⚠⚠ §927（= §923 的 P2）：整场重转**分块** ⇒ 每块各拿一份服务端的 10 分钟预算。
    //   改造前这里是一次整场调用 ⇒ 段数一多，服务端那条 10 分钟的
    //   `fullTranscribeTimeout` 就会把整场重转腰斩（§923.3「墙 3」实测推导）。
    const res = await transcribeInChunks(target, wav, transcribeFull, opts?.chunkSeconds)
    const text = String(res?.text ?? '').trim()
    // 转码失败无论后面成没成都要透出：成功也可能是「退回原 blob 后恰好被接受」，
    // 那种情况下这次失败同样需要有人知道。
    if (!text) return { text: null, applied: false, reason: 'empty-transcript', ...tc() }
    return {
      text,
      applied: true,
      ...tc(),
      // 有段失败必须透出，否则用户以为记录是完整的。
      ...(res.failed > 0 ? { failedSegments: res.failed } : {}),
      // ★ 逐段回执必须一起带上来。它是后面 `attributeFullTranscript` 唯一
      //   能把高精度文本挂回说话人的依据；在这里丢掉，后面就再也拿不回来。
      ...(res.segments?.length ? { segments: res.segments } : {}),
    }
  } catch (e) {
    return {
      text: null,
      applied: false,
      reason: `transcribe-failed: ${msg(e)}`,
      ...tc(),
    }
  } finally {
    // loadMeetingAudio 返回的是 objectURL，用完必须释放，否则长录音会
    // 一直占着内存（会议音频动辄几十 MB）。
    if (audioUrl) {
      try { URL.revokeObjectURL(audioUrl) } catch { /* ok */ }
    }
  }
}

/**
 * §927：把「整场重转」按块发出去（服务端零改动）。
 *
 * ⭐ **短音频（装得下一块）走原来的单次调用** ⇒ 行为**逐字不变**
 * （绝大多数会议都在这一侧 ⇒ 零风险）。
 * ⭐ **只有拿到 WAV 时才切**：webm 兜底路径（`toWav` 失败）没有 demuxer
 * ⇒ 保持单次调用 ⇒ 那一侧的墙仍在（= §923 的 P3，⛔ 真机未验证）。
 *
 * ⚠ **一块失败不许把整场拖死**：其余块的内容仍然值钱 ⇒ 逐块兜住；
 * 但**全部**失败时的失败形态必须与改造前**同义** ⇒
 *   · 块是**抛错**的（网络/超时）⇒ 抛第一个错 ⇒ `reason` 仍是 `transcribe-failed`；
 *   · 块是**服务端回 ok:false** 的（HTTP 200 但没有内容）⇒ 不抛 ⇒
 *     合并后文本为空 ⇒ `reason` 是 `empty-transcript` ——
 *     ⚠ 这与改造前**逐字同义**（单次调用时服务端也这么回）。
 * ⇒ 不新增任何失败形态（属主看得懂的失败形态集合不变）。
 *
 * ⚠⚠ **整块失败时真实段数未知**（服务端按自己的静音切段决定，而失败时
 * 连回执都不返回）⇒ 这里只记 **1** ⇒ 用户提示里的「有 N 段没转出来」
 * 在这种情形下会**少报** ⇒ 如实记一笔，不虚报。
 * ⚠ **纯静音的块不算失败**：服务端把「整段都是纯静音」与「整段都失败」
 *   用**同一个形态**返回（都是 ok:false），只有 error 文案能区分
 *   （`backend/internal/stt/full.go:506`）⇒ 靠文案判定 ⇒
 *   **服务端改文案时这条会退化成「静音也算失败」**（方向是保守的：
 *   多报一次失败，不会少报）。
 */
async function transcribeInChunks(
  target: { blob: Blob; filename: string },
  wav: ArrayBuffer | null,
  transcribeFull: (blob: Blob, filename: string) => Promise<FullTranscribeOutcome>,
  chunkSeconds?: number,
): Promise<FullTranscribeOutcome> {
  const plan = wav ? planWavChunks(wav, chunkSeconds ? { chunkSeconds } : undefined) : null
  if (!plan || plan.bounds.length <= 1 || !wav) {
    return transcribeFull(target.blob, target.filename)
  }
  const parts: ChunkedTranscribePart[] = []
  let firstError: unknown = null
  let succeeded = 0
  let failedChunks = 0
  for (let i = 0; i < plan.bounds.length; i++) {
    const offsetSec = plan.offsetsSec[i]
    try {
      // ⚠ 块**现切现用**：切完立刻发，发完这一轮就被回收
      //   ⇒ 峰值内存 = 原件 + 一块，与会议时长无关。
      const chunk = sliceWavChunk(wav, plan, i)
      const outcome = await transcribeFull(new Blob([chunk], { type: 'audio/wav' }), 'meeting.wav')
      const text = String(outcome?.text ?? '')
      if (!text && !Number(outcome?.failed)) {
        // 这一块**没有产出任何内容** ⇒ 要么整块纯静音，要么整块失败。
        if (isSilenceOnly(outcome?.error)) {
          // 纯静音：不是失败，也不该出现在「有 N 段没转出来」里。
          parts.push({ outcome: { text: '', failed: 0 }, offsetSec })
          continue
        }
        failedChunks += 1
        parts.push({ outcome: { text: '', failed: 0 }, offsetSec })
        continue
      }
      parts.push({ outcome, offsetSec })
      succeeded += 1
    } catch (e) {
      if (firstError === null) firstError = e
      failedChunks += 1
      parts.push({ outcome: { text: '', failed: 0 }, offsetSec })
    }
  }
  // 全部失败且失败形态是「抛错」⇒ 抛出去（与单次调用同义）。
  if (succeeded === 0 && firstError !== null) throw firstError
  const merged = mergeChunkedOutcomes(parts)
  return { ...merged, failed: merged.failed + failedChunks }
}

/**
 * 服务端「这一块整段都是纯静音」的文案特征。
 *
 * 出处：服务端在全量转写「一次成功段都没有」时，把错误写成
 * `全量转写没有任何有效文本（N 段全部失败，首段错误：…）`，
 * 而当所有段都是被跳过的纯静音时，首段错误是
 * `（无逐段错误，全部段被判定为纯静音而跳过）`（`backend/internal/stt/full.go:506`）。
 */
const SILENCE_ONLY_MARK = '纯静音'

/** 这一块是不是「整段纯静音」而不是「失败」。 */
function isSilenceOnly(error?: string): boolean {
  return typeof error === 'string' && error.includes(SILENCE_ONLY_MARK)
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

/** attributeFullTranscript 用得到的最小分段视图。 */
type AttributableSegment = Readonly<
  Pick<MeetingSegment, 'id' | 'meetingId' | 'speakerLabel' | 'lang' | 'confidence' | 'startMs' | 'endMs' | 'text' | 'origin'>
>

/** 两个闭区间 [aStart,aEnd] 与 [bStart,bEnd] 的重叠长度（不相交为 0）。 */
function overlapMs(aStart: number, aEnd: number, bStart: number, bEnd: number): number {
  return Math.min(aEnd, bEnd) - Math.max(aStart, bStart)
}

/** 两个区间之间的「距离」：相交为 0，否则是最近端点之差。 */
function gapMs(aStart: number, aEnd: number, bStart: number, bEnd: number): number {
  if (aEnd < bStart) return bStart - aEnd
  if (bEnd < aStart) return aStart - bEnd
  return 0
}

/**
 * ★ 把整段高精度文本按**时间范围**挂回原有的说话人标签。
 *
 * ── 这个函数存在的原因：一次此前无人发现的静默信息丢失 ──
 *   收尾全量重转返回的不只是一整段文本，还有**逐段回执**
 *   （`[{index,startSec,endSec,text,error}]`，后端 stt/full.go 的
 *   `FullResult.Segments`，经 `/api/stt/transcribe-full` 原样透出）。
 *   但调用方此前把整段文本**压成一条 `speakerLabel: null` 的分段**再交给精校，
 *   于是：
 *     - 录音期间逐段积累、且用户**亲手改过名**的说话人标签，在停止那一刻全部消失；
 *     - 服务端精校提示词第 6 条写着「保留每行开头的 [说话人] 标记」，
 *       而它的输入整场会议只有**一个** `[说话人]` —— 这条要求恒真地满足，
 *       属于「判据/契约与输入同形」的空洞；
 *     - 产出的精校文本进笔记，用户读到的是一段无归属的长文。
 *
 * ── 为什么按时间挂，而不是让模型自己判断谁在说话 ──
 *   两个转写版本的**文字不一致**（全量版才是纠过错的），所以按文本对齐
 *   会漂；而时间范围是两版**共有**的量。逐段回执带 startSec/endSec，
 *   实时分段带 startMs/endMs —— 只用时间做归属，不猜、不编。
 *
 * ── 守不住的场合返回 null，由调用方退回「压成一条」的旧行为 ──
 *   调用方**必须**把 null 当成「照旧」，不能当成失败：
 *     1. 有正文的回执少于 2 条 —— 一条回执表达不了轮次归属（webm 不可切分
 *        时后端正是只回一条整段）。硬分派会把整场文本塞进某一段，
 *        其余段保留旧文本 ⇒ **同一段内容出现两次**，比丢标签更糟。
 *     2. 原分段里一个说话人标签都没有 —— 那就没有可挂的东西。
 *
 * ── 分到某段却没分到正文时，保留该段**自己的**原文本 ──
 *   与本文件既有立场一致（`shouldAdoptFullTranscript`：宁可保持原样，
 *   也不要让用户看到内容变少）。丢内容用户一眼看得出，边界处一小段
 *   归属不准看不出来。
 */
export function attributeFullTranscript(
  original: AttributableSegment[],
  full: ReadonlyArray<FullSegmentReceipt> | null | undefined,
): MeetingSegment[] | null {
  if (!full?.length) return null
  if (!original.some((s) => (s.speakerLabel ?? '').trim())) return null

  // 有正文的回执（含失败占位），按 index 排序保证落回原顺序。
  const bodies = full
    .map((r) => {
      const raw = (r.text ?? '').trim()
      // 失败段在聚合文本里是占位符（stt/full.go 同格式）。逐段重建时若把它
      // 丢掉，那一段就**静默消失**了 —— 与本文件一贯的「不静默丢内容」冲突。
      const body = raw || (r.error ? `［第 ${r.index + 1} 段转写失败：${r.error}］` : '')
      return body ? { body, start: toMs(r.startSec), end: toMs(r.endSec) } : null
    })
    .filter((x): x is { body: string; start: number; end: number } => x !== null)
    .sort((a, b) => a.start - b.start)

  if (bodies.length < 2) return null

  const buckets: string[][] = original.map(() => [])
  for (const b of bodies) {
    // 两轮挑选：先在得分并列的候选里挑**还没拿到正文**的那一段，
    // 全部都被占用时才回到纯得分。
    //
    // ⚠ 为什么要这两轮（2026-10-07）：两段的时间范围完全相同时
    //   （`refine-plan.test.ts` 的 LABELED 就是 [0,1000] × 2），
    //   严格的 `>` 比较会让**第一条**通吃所有正文，第二条变成它的尾巴重复。
    //   后果不是「多几个重复字」——下游 `dedupeSegments` 识别出这段正文已
    //   被前一段完全包含，会**整段丢弃**，于是第二位说话人的归属消失。
    //   那正是 §40 花了很大力气才修回来的东西。
    //
    // ★ 但**只在回执带有位置信息时**才这么做：`b.start === b.end` 说明这条
    //   回执的时间范围是退化的（wire 键被改名 ⇒ `toMs(undefined) = 0` ⇒
    //   全部塌成 [0,0]）。那种情况下回执根本没有位置信息，把它们按顺序
    //   分给不同分段会**让退化变得不可见** ——
    //   `transcribe-full-wire-contract.test.ts` 守的正是这一点：
    //   退化形态必须表现为「第 0 段吞掉全场」，否则输出看起来完全正常，
    //   后端改了字段名都没人发现。
    let best = b.start === b.end
      ? pickBucket(original, buckets, b, false)
      : pickBucket(original, buckets, b, true)
    if (best < 0) best = pickBucket(original, buckets, b, false)
    if (best >= 0) buckets[best].push(b.body)
  }

  return original.map((o, i) => ({
    ...o,
    text: buckets[i].join('\n') || o.text,
  }))
}

/**
 * 给一条回执挑归属分段。重叠优先；完全不相交时退化成「最近的那一段」。
 *
 * @param preferEmpty true 时只考虑**尚未分到正文**的分段。
 * @returns 下标；没有可用候选时返回 -1。
 */
function pickBucket(
  original: AttributableSegment[],
  buckets: string[][],
  b: { body: string; start: number; end: number },
  preferEmpty: boolean,
): number {
  let best = -1
  let bestScore = -Infinity
  for (let i = 0; i < original.length; i++) {
    if (preferEmpty && buckets[i].length > 0) continue
    const o = original[i]
    const ov = overlapMs(o.startMs, o.endMs, b.start, b.end)
    const score = ov > 0 ? ov : -gapMs(o.startMs, o.endMs, b.start, b.end)
    if (score > bestScore) {
      bestScore = score
      best = i
    }
  }
  return best
}

/** 秒（浮点，可能来自 JSON）→ 毫秒。非有限值归零，不让 NaN 污染整条链。 */
function toMs(sec: number): number {
  return Number.isFinite(sec) ? Math.round(sec * 1000) : 0
}

/** buildRefineBaseSegments 的入参。 */
export interface RefineBaseInput {
  meetingId: string
  /** 实时分段：带说话人标签与毫秒时间范围。 */
  original: AttributableSegment[]
  /** 整段重转的聚合文本。 */
  fullText: string
  /** 整段重转的逐段回执（可能缺失）。 */
  fullSegments?: ReadonlyArray<FullSegmentReceipt> | null
  /** 录音总时长（毫秒），用于压成一条时的兜底区间。 */
  elapsedMs: number
}

/**
 * ★ 决定「交给精校的 baseSegments 到底是什么」——**纯函数，可直接单测**。
 *
 * 抽出来的理由与本文件 `resolveUploadTarget` 同一条：这段决策原先埋在
 * Vue composable 里（要 meetingId / recorder / toWav 才能跑），于是判据只能
 * 去扫源码形状 —— 而「扫到 `??` 就算过」这种判据，把 `attributed` 换成永不为
 * null 的表达式照样全绿。**判据必须能执行到那条分支。**
 *
 * 三条路径：
 *   1. 不采纳重转 ⇒ 原样用分段（既有行为）；
 *   2. 采纳且能可靠挂回说话人 ⇒ 用挂了归属的高精度分段；
 *   3. 采纳但挂不回去 ⇒ 压成一条（旧行为，保留为安全网而不是主路径）。
 */
export function buildRefineBaseSegments(input: RefineBaseInput): MeetingSegment[] {
  const { meetingId, original, fullText, fullSegments, elapsedMs } = input
  const attributed = attributeFullTranscript(original, fullSegments)
  if (attributed) return attributed
  const first = original[0]
  return [{
    id: `${meetingId}-full`,
    meetingId,
    speakerLabel: null,
    lang: first?.lang ?? 'zh',
    confidence: 1,
    startMs: 0,
    endMs: elapsedMs,
    text: fullText,
    // §132：这条是**整段高精度重转**的产物（ASR），不是人写的 ——
    // 因此它**要**享受相邻去重，不能因为「看起来像一条大段」就豁免。
    origin: 'asr',
  }]
}
