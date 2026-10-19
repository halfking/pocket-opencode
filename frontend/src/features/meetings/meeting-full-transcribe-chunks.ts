/**
 * meeting-full-transcribe-chunks.ts — 收尾「整场重转」的**分块化**（§927 = §923 的 P2）。
 *
 * ── 为什么需要它（§923.3 的「墙 3 / 墙 4」）──
 * 收尾的整场重转原先是**一次**整场调用（`transcribeFull(target.blob, ...)`），
 * 而服务端 `handleSttTranscribeFull` 是
 * `context.WithTimeout(r.Context(), fullTranscribeTimeout)`，
 * `fullTranscribeTimeout = 10 * time.Minute`（`server_stt_stream.go:39`）
 * ⇒ **整个请求（含逐段串行转写）只有 10 分钟预算**。
 * 段数由服务端的 `SplitWAV` 决定（静音感知切段，上限 25 s）：
 * §923 实测真实会议语料 ⇒ **3.03 段/分钟**（连续发言）、
 * 真实录音更碎 ⇒ **10.5 段/分钟** ⇒
 * ⇒ 10 分钟预算下「每次上游调用允许的秒数」= 600 / 段数：
 * 1 小时会议只剩 **0.95–3.30 s/次**、4 小时只剩 **0.24–0.83 s/次**
 * ⇒ ⇒ **一旦被腰斩，整场重转白跑**（用户拿回段级文本）。
 *
 * ⚠⚠ **第二道墙是字节**（这一道 §923 没记，是本节补的）：
 * 服务端全量端点走 **JSON base64** 入参，解码后音频上限
 * `maxFullAudioBytes = 400 << 20`（= 400 MiB，`server_stt_stream.go:171`）。
 * 16 kHz 单声道 16-bit = **32 000 B/s** ⇒
 * ⇒ 400 MiB / 32 000 B/s ≈ **3.55 小时** ⇒
 * **超过 3.55 小时的会议，整场重转的请求会被**直接拒收**（不是超时，是压根发不出去）**。
 * ⇒ 这道墙与「时长无限」的目标**正面冲突**：不是「慢」，是**没有结果**。
 *
 * ⇒ 修法（**服务端零改动**）：**客户端**把整场 WAV 切成若干块，
 * **逐块**各发一次 `/api/stt/transcribe-full` ⇒
 *   ① 每块各拿一份 10 分钟预算（时间墙按块摊薄）；
 *   ② 每块的请求体恒定在十几 MB（字节墙与会议时长**脱钩**）。
 *
 * ── 块长怎么定的（算出来的，不是拍的）──
 * 目标：每块的**服务端工作量**要留够余量 ⇒ 每块的上游调用次数 ≤
 * `600 s / 每次调用至少 5 s` = **120 段** ⇒ 按最碎的实测段频（10.5 段/分）
 * ⇒ 120 / 10.5 ≈ **11.4 分钟** ⇒ 取 **8 分钟**（⇒ 84 段/块 ⇒
 * 每次调用仍有 **7.1 s** 预算，是 §923 表里「1 小时会议」那一格的 **7.5×**）。
 * ⛔ 每次上游调用的**实际耗时未实测**（网关 key 仍 401）⇒ 上面的「至少 5 s」是
 * **要求值**不是读数 ⇒ 因此块长取的是**保守的一侧**。
 *
 * ── 块边界不能切在词中间 ──
 * 硬切会把一个词劈成两半（两侧各错一点，拼接处各丢一半信息 ——
 * 服务端 `full_cross_segment_merge_status_test.go` 记的就是这个形态）。
 * ⇒ 切点**贴着静音走**：在目标点 ±20 s 的窗口里找 **RMS 最低**的位置，
 * 评估窗**居中**（切点两侧各 50 ms）—— 只看一侧的话切点可能正好落在**词尾**，
 * 词照样被劈开。
 * ⚠ 静音门槛 **0.012**（= 服务端 `silenceFloor`，`backend/internal/stt/full.go:109`）
 *   是**合成夹具**上的口径（真静音 = 采样全 0 ⇒ 两档可分）。
 *   ⛔ **真实录音上这条不成立**（§928 实测，12.54 min 真人会议语料）：
 *   100 ms 居中窗低于 0.012 的只占 **11.0%** ⇒ 「切点落在静音里」是**夹具性质**，
 *   不是真实录音的性质。真实可断言的命题是「**切点比硬切安静**」
 *   （实测 494.15 s 处 0.02150 vs 硬切 480 s 处 0.05547 ⇒ **安静 2.58×**）。
 *   ⇒ 本模块**不设门槛** —— 找不到静音时取**最安静**的那个点 ⇒ 仍然比硬切好。
 * ⚠ 口径提醒：服务端比的是**均方**（`silenceFloorSquared`），本模块算的是**均方根**
 *   —— 同一个门槛的两种表达，等价；但**不能**拿均方去比 0.012，
 *   那会把等效门槛放大到 sqrt(0.012) ≈ 0.11（服务端 `full.go` 的注释记着这个坑）。
 *
 * ── 资源纪律（本节新增，用户诉求「不增加大量存储或资源」）──
 * ⭐ **块必须「现切现用」**：一次把整场切成 N 块再逐块发 ⇒
 * 8 小时会议（16 kHz/16-bit = 921 MB）会**同时**持有 921 MB 原件 + 921 MB 块
 * ⇒ **内存翻倍**，而这正是 §923 记的「WebView 堆上限」那条未实测项。
 * ⇒ 本模块**只算切点**（`planWavChunks`，零音频拷贝），
 * **每块在用之前才切**（`sliceWavChunk`，只多活一块 = 8 分钟 ≈ 15.4 MB）。
 * ⇒ 峰值内存与**会议时长无关**，只与块长有关。
 *
 * ── 块长上界（预算推导的唯一依据，§928.6）──
 * ⚠⚠ **块长 ≠ 块长参数**：切点是在「目标点 ± 搜索窗」里**吸附**出来的 ⇒
 *   - 相邻两切点之差最大 = `块长 + 2×搜索窗`；
 *   - **尾块**还能多出一个「最小块长」（网格点离文件尾太近时循环直接 break，
 *     剩下的整段并进尾块）。
 * ⇒ 真实上界 = `max(块长 + 2×搜索窗, 块长 + 最小块长)`
 * ⇒ 默认参数下 = `max(480+40, 480+60)` = **540 s**
 * ⇒ 每次上游调用实际还剩 `600 / (540/60×10.5)` = **6.3 s**（⛔「每次调用 ≥ 5 s」
 *   是**要求值不是读数**：网关 key 401）⇒ §927 文档原先写的 7.1 s **高估了余量 12%**。
 * ⇒ ⚠ 这条上界是**涌现**的（不是被代码强制的）⇒ 所以判据必须**按行为钉住它**
 *   （`meeting-final-transcript.test.ts` 的 I 组），否则改搜索窗宽度会静悄悄吃掉余量。
 *
 * ⚠⚠ **本模块是纯函数**（无 import、无 I/O）⇒ 门禁能直接跑真身。
 * 它只处理 **16-bit PCM WAV**（客户端 `toWav` 的产物就是 16 kHz 单声道 WAV，
 * `utils/wav-encode.ts` 的 `encodeWav` 就是标准 44 字节头）；
 * 其它格式（webm 兜底路径）一律返回 null ⇒ 调用方保持**单次调用**（行为不变）。
 */

/** 每块的时长（秒）。取值依据见文件头。 */
export const FULL_TRANSCRIBE_CHUNK_SECONDS = 8 * 60

/** 切点搜索窗口（秒）：在目标点 ±这个范围内找最安静的位置。 */
export const FULL_TRANSCRIBE_CUT_SEARCH_SECONDS = 20

/**
 * 每块至少留这么多秒（避免切出碎片块）。
 *
 * ⚠⚠ **它同时决定块长上界** ⇒ 改这个值会改预算推导（见文件头「块长上界」那段）。
 */
export const MIN_CHUNK_SECONDS = 60


/** 搜索时的步长（毫秒）与评估窗长（毫秒）。 */
const SCAN_STEP_MS = 10
const SCAN_WINDOW_MS = 100

export interface WavLayout {
  /** 从文件开头到 `data` 载荷起点（含 `data` 块头）的字节。 */
  header: Uint8Array
  /** `data` 载荷在文件里的起点。 */
  dataOffset: number
  /** `data` 载荷字节数。 */
  dataLength: number
  sampleRate: number
  channels: number
  bitsPerSample: number
  /** 每帧字节数（channels × bits / 8）。 */
  blockAlign: number
}

/**
 * 解析 WAV 布局。**只支持 16-bit PCM**（其余一律 null ⇒ 调用方走原路径）。
 *
 * ⚠ `data` 之后若有别的块（LIST/metadata），**丢弃**它们 ——
 *   切出来的块只保留「fmt + data」，仍是合法 WAV。
 */
export function parseWavLayout(bytes: Uint8Array): WavLayout | null {
  if (bytes.length < 44) return null
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  const tag = (off: number) => String.fromCharCode(bytes[off], bytes[off + 1], bytes[off + 2], bytes[off + 3])
  if (tag(0) !== 'RIFF' || tag(8) !== 'WAVE') return null
  let pos = 12
  let fmt: { channels: number; sampleRate: number; bits: number; blockAlign: number } | null = null
  let dataOffset = -1
  let dataLength = 0
  while (pos + 8 <= bytes.length) {
    const id = tag(pos)
    const size = view.getUint32(pos + 4, true)
    const body = pos + 8
    if (id === 'fmt ' && body + 16 <= bytes.length) {
      const audioFormat = view.getUint16(body, true)
      const channels = view.getUint16(body + 2, true)
      const sampleRate = view.getUint32(body + 4, true)
      const blockAlign = view.getUint16(body + 12, true)
      const bits = view.getUint16(body + 14, true)
      // audioFormat 1 = 未压缩 PCM；3 = 浮点（不支持）。
      if (audioFormat !== 1 || bits !== 16 || channels < 1 || sampleRate < 1) return null
      fmt = { channels, sampleRate, bits, blockAlign }
    } else if (id === 'data') {
      dataOffset = body
      dataLength = Math.min(size, bytes.length - body)
      break
    }
    pos = body + size + (size % 2) // 块按偶数字节对齐
  }
  if (!fmt || dataOffset < 0 || dataLength <= 0) return null
  if (fmt.blockAlign < 1) return null
  return {
    header: bytes.subarray(0, dataOffset),
    dataOffset,
    dataLength,
    sampleRate: fmt.sampleRate,
    channels: fmt.channels,
    bitsPerSample: fmt.bits,
    blockAlign: fmt.blockAlign,
  }
}

/**
 * 一帧的功率（= 各声道均方再取平均）。
 *
 * ⚠⚠ **必须按有符号读**：`getInt16` 而不是 `bytes[o] | (bytes[o+1] << 8)`。
 * 后者把 -1 读成 65535 ⇒ `/32768` 得 2.0 ⇒ 功率 4.0（真实值是 3e-9）⇒
 * **静音判定整个失真**（负半周的样本被当成满幅）。
 * 服务端 `full.go` 的 `frameRMS` 用的就是 `int16(binary.LittleEndian.Uint16(...))`，
 * 这边必须同口径，否则「静音」两个字的定义在两端不是同一个东西。
 */
function framePower(view: DataView, byteOffset: number, blockAlign: number): number {
  let acc = 0
  for (let c = 0; c < blockAlign; c += 2) {
    const v = view.getInt16(byteOffset + c, true) / 32768
    acc += v * v
  }
  return acc / (blockAlign / 2)
}

export interface WavChunkPlan {
  /** WAV 布局（header 是原件的**视图**，不复制字节）。 */
  layout: WavLayout
  /** 整场总帧数。 */
  totalFrames: number
  /** 每块 [起帧, 止帧)（止帧不含）。 */
  bounds: Array<[number, number]>
  /** 每块在整场里的起点（秒）—— 合并回执时要把时间轴平移回来。 */
  offsetsSec: number[]
  /** 实际切点（毫秒）—— 供日志/诊断。 */
  cutPointsMs: number[]
}

/**
 * 把整场 WAV 切成若干块。
 *
 * ⚠ **只算切点，不复制音频** —— 块由 `sliceWavChunk` 现切现用（见文件头的资源纪律）。
 *
 * @returns 不足一块长 ⇒ **null**（调用方保持单次调用 ⇒ 行为完全不变）。
 */
export function planWavChunks(
  wav: ArrayBuffer,
  opts?: { chunkSeconds?: number; searchSeconds?: number; minChunkSeconds?: number },
): WavChunkPlan | null {
  const bytes = new Uint8Array(wav)
  const layout = parseWavLayout(bytes)
  if (!layout) return null
  const chunkSeconds = opts?.chunkSeconds ?? FULL_TRANSCRIBE_CHUNK_SECONDS
  const searchSeconds = opts?.searchSeconds ?? FULL_TRANSCRIBE_CUT_SEARCH_SECONDS
  if (!(chunkSeconds > 0)) return null

  const totalFrames = Math.floor(layout.dataLength / layout.blockAlign)
  if (totalFrames <= 0) return null
  if (totalFrames / layout.sampleRate <= chunkSeconds) return null // 一块都装得下 ⇒ 不切（行为不变）

  // 每块的最小长度：默认 60 s，但**不得超过块长本身**（否则小块会被自己的下限卡死）。
  const minChunkSeconds = Math.min(opts?.minChunkSeconds ?? MIN_CHUNK_SECONDS, chunkSeconds)
  const framesPerChunk = Math.max(1, Math.round(chunkSeconds * layout.sampleRate))
  const searchFrames = Math.max(0, Math.round(searchSeconds * layout.sampleRate))
  const minChunkFrames = Math.max(1, Math.round(minChunkSeconds * layout.sampleRate))
  const stepFrames = Math.max(1, Math.round((SCAN_STEP_MS / 1000) * layout.sampleRate))
  const windowFrames = Math.max(2, Math.round((SCAN_WINDOW_MS / 1000) * layout.sampleRate))
  const halfFrames = Math.floor(windowFrames / 2)

  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  const pcmByte = layout.dataOffset

  /** 某一帧的功率。 */
  const powerAt = (frame: number) => framePower(view, pcmByte + frame * layout.blockAlign, layout.blockAlign)

  /**
   * 在 [lo, hi]（帧号，闭区间）里找**窗口 RMS 最低**的位置；并列取最靠前的。
   *
   * ⚠ 评估窗是**居中**的（切点两侧各 50 ms），不是「切点之后」：
   *   只看之后那一侧 ⇒ 切点可能正好落在**词尾**（词在切点前结束）⇒ 词被劈开；
   *   两侧都看 ⇒ 「切在静音里」这件事才成立。
   *
   * 用**滑动和**而不是每个位置重算整个窗口：窗口 100 ms、步长 10 ms 时，
   * 重算法是 4000 个位置 × 1600 帧 = 640 万次/切点（8 小时会议 60 个切点
   * ⇒ 3.8 亿次）⇒ 滑动和把它降到「扫过的帧数」量级（同样 40 s 区间约 64 万次）。
   *
   * ⚠ 比的是**窗口内的功率和**而不是 RMS：窗口长度恒定 ⇒ 两者等价，
   *   省掉每个位置的 sqrt。
   */
  const findQuietest = (lo: number, hi: number): number => {
    const loClamped = Math.max(lo, halfFrames)
    const hiClamped = Math.min(hi, totalFrames - halfFrames)
    if (loClamped > hiClamped) return -1
    let sum = 0
    for (let k = 0; k < windowFrames; k++) sum += powerAt(loClamped - halfFrames + k)
    let best = loClamped
    let bestSum = sum
    for (let f = loClamped + stepFrames; f <= hiClamped; f += stepFrames) {
      // 窗口从 [f-step-half, f-step+half) 移到 [f-half, f+half)。
      for (let k = 0; k < stepFrames; k++) sum -= powerAt(f - stepFrames - halfFrames + k)
      for (let k = 0; k < stepFrames; k++) sum += powerAt(f - stepFrames + halfFrames + k)
      if (sum < bestSum) { bestSum = sum; best = f }
    }
    return best
  }

  // 切点：每 chunkSeconds 一个目标点，贴到最近的安静处。
  //
  // ⚠ 搜索窗口必须与「合法区间」求交，而不是先找安静处再判合法性：
  //   先后顺序会让「离上一个切点太近」时**退回硬切的原目标点** ——
  //   那正好是一个词中间（§923 记的服务端 `full_cross_segment_merge_status_test.go` 形态）。
  const cuts: number[] = []
  let lastCut = 0
  for (let target = framesPerChunk; target < totalFrames; target += framesPerChunk) {
    const lo = Math.max(target - searchFrames, lastCut + minChunkFrames)
    const hi = Math.min(target + searchFrames, totalFrames - minChunkFrames)
    if (lo > hi) break // 往后只会更紧 ⇒ 不再切
    const cut = findQuietest(lo, hi)
    if (cut < 0 || cut <= lastCut) break
    cuts.push(cut)
    lastCut = cut
  }

  const bounds: Array<[number, number]> = []
  const offsetsSec: number[] = []
  const cutPointsMs: number[] = []
  let from = 0
  for (const cut of cuts) {
    bounds.push([from, cut])
    offsetsSec.push(from / layout.sampleRate)
    cutPointsMs.push((cut / layout.sampleRate) * 1000)
    from = cut
  }
  bounds.push([from, totalFrames])
  offsetsSec.push(from / layout.sampleRate)

  return { layout, totalFrames, bounds, offsetsSec, cutPointsMs }
}

/**
 * 按计划切出**一块**（一个完整 WAV）。
 *
 * ⚠ 每次只切一块 ⇒ 峰值内存 = 原件 + 一块（8 分钟 ≈ 15.4 MB），
 * 与会议时长**无关**（文件头「资源纪律」那条）。
 */
export function sliceWavChunk(wav: ArrayBuffer, plan: WavChunkPlan, index: number): ArrayBuffer {
  const b = plan.bounds[index]
  if (!b) throw new Error(`chunk index out of range: ${index}`)
  const layout = plan.layout
  const bytes = new Uint8Array(wav)
  const [from, to] = b
  const sliceBytes = (to - from) * layout.blockAlign
  const out = new Uint8Array(layout.header.length + sliceBytes)
  out.set(layout.header, 0)
  out.set(
    bytes.subarray(layout.dataOffset + from * layout.blockAlign, layout.dataOffset + to * layout.blockAlign),
    layout.header.length,
  )
  // 改写头里的两个长度：RIFF 块长（4）与 data 块长（dataOffset+4 处的 4 字节）。
  const dv = new DataView(out.buffer)
  dv.setUint32(4, (layout.dataOffset - 8) + sliceBytes, true)
  dv.setUint32(layout.dataOffset - 4, sliceBytes, true)
  return out.buffer
}

/** 一块的重转结果（结构上与 `FullTranscribeOutcome` 一致 ⇒ 不 import 那个模块，避免环）。 */
export interface ChunkTranscribeOutcome {
  text: string
  failed: number
  segments?: Array<{ index: number; startSec: number; endSec: number; text?: string; error?: string }> | null
}

export interface ChunkedTranscribePart {
  outcome: ChunkTranscribeOutcome
  /** 这块在整场里的起点（秒）—— 用来把回执的时间轴平移回整场。 */
  offsetSec: number
}

/**
 * 合并多块的重转结果。
 *
 * ⭐⭐ **回执的时间轴必须平移**：每块的 `startSec/endSec` 是**块内**时间，
 * 而 `attributeFullTranscript` 靠它把文本挂回说话人 ⇒ 不平移 ⇒
 * 第 2 块以后的回执会全部落到「会议开头」⇒ **说话人归属全错**。
 *
 * ⭐⭐ **失败占位符的段号必须换成全局的**：服务端聚合文本时写的是
 * `［第 N 段转写失败：…］`，N 是**块内**段号（`full.go:478` 用 `seg.Index+1`）。
 * 单次调用时 N 恰好就是全局段号；**分块后不是了** ⇒ 直接拼块文本会给出
 * 「第 1 段转写失败」而它其实是整场的第 37 段 ⇒ 用户读到的是**错的段号**。
 * ⇒ 所以这里**从合并后的回执重建聚合文本**（回执的 index 已全局重编号）。
 * ⚠ 只有在**每块都带回执**时才重建；否则退回拼块文本
 *   （此时占位符里的段号是块内的 —— 老后端没有回执字段，属既有能力边界）。
 */
export function mergeChunkedOutcomes(parts: ChunkedTranscribePart[]): ChunkTranscribeOutcome {
  const failed = parts.reduce((n, p) => n + (Number(p.outcome?.failed) || 0), 0)
  const segments: NonNullable<ChunkTranscribeOutcome['segments']> = []
  for (const p of parts) {
    for (const s of p.outcome?.segments ?? []) {
      segments.push({
        ...s,
        index: segments.length,
        startSec: s.startSec + p.offsetSec,
        endSec: s.endSec + p.offsetSec,
      })
    }
  }
  const allHaveSegments = parts.length > 0 && parts.every((p) => (p.outcome?.segments?.length ?? 0) > 0)
  const text = allHaveSegments
    ? segments
      .map((s) => {
        if (s.text) return s.text
        if (s.error) return `［第 ${s.index + 1} 段转写失败：${s.error}］`
        return ''
      })
      .filter((t) => t.length > 0)
      .join('\n')
    : parts
      .map((p) => String(p.outcome?.text ?? '').trim())
      .filter((t) => t.length > 0)
      .join('\n')
  return { text, failed, segments: segments.length ? segments : undefined }
}
