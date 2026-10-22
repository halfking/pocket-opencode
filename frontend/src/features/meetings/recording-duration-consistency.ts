/**
 * 录音时长一致性：把「墙钟说的多久」和「音频真的有那么多」放在同一处比对。
 *
 * ## 为什么需要这个模块（§803 / §804）
 *
 * 录音链路里**每个**时长数字都是 `Date.now()` 的差（`recordingRuntime.ts:384/388`
 * 的 `elapsedMs`、写进 DB 的 `durationMs`，以及 `vad-segmenter.ts:102/137/253` 的
 * 各段 `endMs`）。⇒ **§785 那次「62 秒进度只存下 57.36 秒 PCM」在运行时没有任何
 * 量能与它矛盾**：不是没人注意到，是没有可注意到的东西。
 *
 * ⚠⚠ **这里刻意不拿 `elapsedMs` 对照分段 `endMs`**：两者同出 `Date.now() - startTime`
 * （且各自持有独立的 `startTime`），相减只得到两个墙钟读数之差，对「实际抓到多少
 * 音频」**零信息量** —— 那是恒等式护栏，能通过变异自证却永远测不到真缺陷。
 *
 * ## 为什么比对是零代价的（§804）
 *
 * `wav-encode.ts:28` 的 `decodeAudioData` 早就把整个 webm 解开了，`buf.duration`
 * 在那一行就在手上，却只把重采样后的 WAV 返回出去、**把时长丢掉**。
 * 且那个 WAV 是 44 字节头 + `samples.length * 2`，**字节数自带动长** ⇒
 * `真实时长秒 = (byteLength - 44) / 2 / 16000`，**不需要任何额外解码**。
 *
 *
 * ⚠ 护栏量的是「墙钟 vs 音频」，而**墙钟本身也可能是陈旧的** —— 见 §812 与本文件末尾的
 * `resolveWallClockDurationMs`。
 * 判定阈值与「报警但不阻断」是**产品口径**：本模块只给读数与判定，不抛错、
 * 不改写任何状态；调用方（`meeting-recording-finalize.ts`）只 `console.warn`。
 */

/** wFormatTag：1 = PCM（定长，帧数线性），3 = IEEE float（定长），0xFFFE = EXTENSIBLE。 */
const WAVE_FORMAT_PCM = 1
const WAVE_FORMAT_IEEE_FLOAT = 3
const WAVE_FORMAT_EXTENSIBLE = 0xfffe
const WAV_HEADER_MIN_BYTES = 44
const WAV_BYTES_PER_SAMPLE = 2
/** `wav-encode.ts` 的 TARGET_SAMPLE_RATE，产出恒为 16kHz。 */
const WAV_SAMPLE_RATE = 16000

function readTag(b: Uint8Array, at: number): string {
  if (at + 4 > b.byteLength) return ''
  return String.fromCharCode(b[at], b[at + 1], b[at + 2], b[at + 3])
}

/**
 * 从 PCM WAV 的**字节**算真实音频时长（秒）。
 *
 * ⚠⚠ **不能假设头长是 44 字节。** 第一版写成 `(byteLength - 44) / 2 / 16000`，
 * 而本仓真实产物上就是错的：`acoustic-speaker-mic.wav` 的 chunk 布局是
 * `fmt `(16) → **`LIST`(26)** → `data`，`data` 偏移 **78** 而非 44 ⇒ 朴素公式
 * 读出 57.36106s，真值 57.36000s（`LIST` 的 26 字节被当成了音频数据）。
 * 那个偏差只有 ~1ms，被容差吃掉了，但**它是错的**，且换个来源（metadata 更大的
 * LIST/更多 chunk）会成比例放大。⇒ 逐 chunk 走并读 `fmt ` 的真实参数。
 *
 * 返回 `null` 表示「这批字节的时长我算不出来」，而不是返回 0、也不是按 PCM 公式硬算：
 * 0 会被读成「录了 0 秒」，那和「按错误的算法算出一个自信的数字」**都是假读数**
 * （§803.1 同型：没有那个量，不是那个量为 0）。
 *
 * ⚠⚠ **只认「时长可由帧数直接算出」的编码**（PCM / IEEE float，含 EXTENSIBLE 包这两种）。
 * `dataSize / (bits/8 × ch × sr)` 这个式子**只对定长 PCM 成立**：Opus-in-WAV 的
 * `bitsPerSample` 是**占位值 16**、ADPCM 的「帧」是压缩块，二者套这个式子会算出
 * 一个**看起来完全合理、实则完全错误**的秒数（实测两者都返回 10s）。⇒ 认不出就 null。
 *
 * 走字节而不是 `AudioBuffer.duration`，是因为本函数要在 node 测试里跑，
 * 也不需要浏览器环境 —— 那正是 §803 说「真值从来没被量过」时缺的东西。
 */
export function wavDurationSec(wav: ArrayBuffer | Uint8Array): number | null {
  const b = wav instanceof Uint8Array ? wav : new Uint8Array(wav)
  if (b.byteLength < WAV_HEADER_MIN_BYTES) return null
  if (readTag(b, 0) !== 'RIFF' || readTag(b, 8) !== 'WAVE') return null

  let audioFormat = 0
  let fmtBodyAt = -1
  let sampleRate = 0
  let channels = 0
  let bitsPerSample = 0
  let dataBytes: number | null = null

  // chunk 布局：4 字节 id + 4 字节小端长度 + 载荷，长度为奇数时还有一个补齐字节。
  let off = 12
  while (off + 8 <= b.byteLength) {
    const id = readTag(b, off)
    const size = b[off + 4] | (b[off + 5] << 8) | (b[off + 6] << 16) | (b[off + 7] << 24)
    const body = off + 8
    if (id === 'fmt ' && body + 16 <= b.byteLength) {
      audioFormat = b[body] | (b[body + 1] << 8)
      // EXTENSIBLE(0xFFFE) 的真实编码藏在 SubFormat GUID 的头两个字节（fmt 体 +24）
      if (audioFormat === WAVE_FORMAT_EXTENSIBLE && body + 26 <= b.byteLength) {
        audioFormat = b[body + 24] | (b[body + 25] << 8)
      }
      fmtBodyAt = body
      channels = b[body + 2] | (b[body + 3] << 8)
      sampleRate = b[body + 4] | (b[body + 5] << 8) | (b[body + 6] << 16) | (b[body + 7] << 24)
      bitsPerSample = b[body + 14] | (b[body + 15] << 8)
    } else if (id === 'data') {
      // 声明长度可能超出实际字节（流式封装常见），取实际可用的那段。
      dataBytes = Math.max(0, Math.min(size, b.byteLength - body))
      break
    }
    off = body + size + (size & 1)
  }

  // ⚠ 定长编码才谈得上「帧数 × 1/采样率 = 秒数」。其它编码（Opus/ADPCM/…）的
  // dataSize 与采样数没有这个线性关系，硬算就是**自信地编一个数**。⇒ 认不出就 null。
  if (audioFormat !== WAVE_FORMAT_PCM && audioFormat !== WAVE_FORMAT_IEEE_FLOAT) return null
  if (fmtBodyAt < 0) return null
  if (dataBytes == null || sampleRate <= 0 || channels <= 0 || bitsPerSample <= 0) return null
  const bytesPerFrame = (bitsPerSample / 8) * channels
  if (bytesPerFrame <= 0 || dataBytes === 0) return null
  return dataBytes / bytesPerFrame / sampleRate
}

/**
 * 容差 = `max(绝对下限, 相对份额 × 墙钟)`。
 *
 * ⚠ 相对份额意味着**长录音里的小额绝对丢失会被放过**（20 分钟丢 4s = 0.33% < 2% ⇒ 不报）。
 * 这是刻意的取舍：按绝对值报警，长会议几乎必然刷屏；按相对值报警，一次性把阈值
 * 抬到「长会议丢满 2% 才报」。本模块只 `console.warn`，宁可漏报也不要训练人忽略报警。
 * 这个取舍由 `__tests__` 里那条显式用例钉住，改口径必须同时改那条用例。
 */
const DEFAULT_TOLERANCE_MS = 500
const DEFAULT_RELATIVE = 0.02

export type ToWav = (blob: Blob) => Promise<ArrayBuffer | null>

/**
 * 给 `toWav` 套一层「顺手量时长」的包装，透传原返回。
 *
 * 为什么要提成函数而不是把 6 行胶水直接写在 `finalizeRecording` 里：
 * `finalizeRecording` 依赖 localDB + HTTP，**在 node 里跑不到**（§52 当初就是因为
 * 「决策埋在跑不到的函数体里、一行行为门都没有」才把 `planRefine` 提成纯函数）。
 * 把「测量 + 透传」放进这里，就能被单测覆盖；调用方只剩一行构造。
 *
 * 透传语义刻意保持原样：`null`（不需转码）、`bytes`（转码成功）都原封不动返回，
 * 不改变任何调用方拿到的结果形状——本模块**只观察，不干预**。
 */
export function createMeasuringToWav(
  toWav: ToWav,
  onMeasured: (sec: number | null) => void,
): ToWav {
  return async (blob: Blob) => {
    const bytes = await toWav(blob)
    onMeasured(bytes ? wavDurationSec(bytes) : null)
    return bytes
  }
}

export interface DurationDrift {
  /** 是否在容差内。`true` = 无异常。 */
  ok: boolean
  /** 墙钟与真实音频之差（正 = 墙钟更长，即**音频比说的短**）。null 表示量不到真值。 */
  driftMs: number | null
  /** 真实时长占墙钟的比例。null 表示量不到真值。 */
  ratio: number | null
  /** 实际生效的容差（ms），写进读数方便复核。 */
  toleranceMs: number
  /** 判定依据的机器可读标签。 */
  reason: 'within-tolerance' | 'audio-shorter' | 'audio-longer' | 'no-ground-truth'
}

/**
 * 比对墙钟时长与真实音频时长。
 *
 * ⚠ `actualSec = null`（转码失败 / 没转码 / 字节不是合法 WAV）时一律判 `ok` 且
 * `reason = 'no-ground-truth'` —— **量不到不等于没问题**（§803 的静默缺口）。
 * 这条刻意不做「保守报警」，因为它会在每次转码失败时刷噪声，训练人忽略报警；
 * 真正的处理是让转码失败本身可见，不是让每一次失败都喊一次时长不符。
 */
export function assessDurationDrift(
  elapsedMs: number,
  actualSec: number | null,
  opts: { toleranceMs?: number; relative?: number } = {},
): DurationDrift {
  const relative = opts.relative ?? DEFAULT_RELATIVE
  const toleranceMs = opts.toleranceMs ?? Math.max(DEFAULT_TOLERANCE_MS, elapsedMs * relative)

  if (actualSec == null || !Number.isFinite(actualSec) || elapsedMs <= 0) {
    return { ok: true, driftMs: null, ratio: null, toleranceMs, reason: 'no-ground-truth' }
  }

  const actualMs = actualSec * 1000
  const driftMs = Math.round(elapsedMs - actualMs)
  if (Math.abs(driftMs) <= toleranceMs) {
    return { ok: true, driftMs, ratio: actualMs / elapsedMs, toleranceMs, reason: 'within-tolerance' }
  }
  return {
    ok: false,
    driftMs,
    ratio: actualMs / elapsedMs,
    toleranceMs,
    reason: driftMs > 0 ? 'audio-shorter' : 'audio-longer',
  }
}

/**
 * 取「该写进 DB 的墙钟时长」。**在写入时刻重算，不读 interval 的缓存值。**
 *
 * ⚠⚠ §812 的由来：`recordingRuntime` 的 `elapsedMs` 只由 `setInterval(200ms)` 更新，
 * 而页面隐藏时浏览器会**节流定时器**；与此同时 `onHidden` **只弹 toast、并不停止录音**
 * （只有 nativeMode 才早退）⇒ 后台录音继续、缓存值却可能过期数秒到数分钟。
 * 旧代码 `const durationMs = this.elapsedMs.value` 把这个**陈旧缓存**写进了库。
 *
 * ⚠ 这还会连带影响本模块的护栏：它比的就是这个数 ⇒ 墙钟本身不准，护栏就跟着不准。
 * （本仓另一条路径 `recordingRuntime.ts:1166` 本来就直接重算，两处口径此前不一致。）
 *
 * @param now        写入时刻的 `Date.now()`
 * @param startTime  本次录音的起点（`Date.now()` 口径）
 * @param fallbackMs 缓存值，仅在「无法重算」时使用
 * @param isPaused   暂停态下墙钟本就不该继续走 ⇒ 只能退回缓存值
 */
export function resolveWallClockDurationMs(
  now: number,
  startTime: number,
  fallbackMs: number,
  isPaused: boolean,
): number {
  if (isPaused) return fallbackMs
  if (!(startTime > 0) || !Number.isFinite(now)) return fallbackMs
  return Math.max(0, now - startTime)
}

// ---------------------------------------------------------------------------
// §955：护栏的**输入**从哪来 —— 原生链与 Web 链的墙钟**不同源**
// ---------------------------------------------------------------------------

/**
 * §955：原生采集窗口的快照。
 *
 * ⚠⚠ **为什么原生链需要另一个墙钟**：
 *   护栏比的是「墙钟 vs 真实音频」⇒ **两个数必须同源**，否则差的是**系统偏移**
 *   而不是「音频丢了多少」。
 *   原生链的墙钟有两个候选：
 *     ① JS 墙钟（`Date.now() - startTime`）—— 它的起点是 **JS 侧**的
 *        `start()`，而原生服务是**先**开始采音、`start()` 才 resolve
 *        （`BackgroundMicPlugin.start` 等的是服务回报 `reportStart(true)`）
 *        ⇒ 两者起点**不同相位** ⇒ 差一个**未实测的启动延迟**
 *        ⇒ 短录音会超过容差（500 ms）⇒ **假警报**；
 *     ② **原生采集窗口**（原生侧每片带的 `endMs`，相对原生 `captureStart`）
 *        —— 它与盘上的音频**同源**（那些片就是盘上的音频）⇒ 可比。
 *   ⇒ ⇒ 护栏在原生链上必须用 ②。
 */
export interface NativeCaptureSnapshot {
  /** 本次是否走了原生后台采集。 */
  used: boolean
  /**
   * 原生采集窗口（毫秒）。
   *
   * ⚠ `null` = **不可信**：窗口拿不准时宁可判「量不到」，也不拿一个错基的数去比。
   *   （§955 当时的理由是「切过设备 ⇒ 原生时间轴回卷 ⇒ 窗口错基」；
   *   **那个理由已被 §956 消灭** ⇒ 现在不可信只剩两种原因：
   *   ① 切过设备而**时间轴不精确**（拿不到采集原点 / 被钳位过）；
   *   ② 压根没采到片（窗口恒 0）。）
   */
  windowMs: number | null
  /**
   * §957：**轮次之间的空隙**（毫秒）—— 切换设备期间**没有音频**的那段墙钟。
   *
   * ⚠ `null` = **量不到**（窗口不可信时，空隙同样不可信）。
   * ⚠ `0` = 实测**没有**空隙（单轮录音就是 0）——
   *   「量不到」与「量到是 0」必须能分开（§803.1）。
   *
   * ⚠⚠ **护栏要比的墙钟要把它扣掉**：盘上的音频里**没有**那段时间
   *   ⇒ 不扣 ⇒ 护栏会把它当成「音频丢了」（**假警报**）。
   */
  gapMs: number | null
}

/** 护栏的墙钟来源（写进告警文案：排查的人要知道拿的是哪个钟）。 */
export type DurationGuardSource = 'js-wall-clock' | 'native-window' | 'native-untrusted'

export interface DurationGuardInput {
  /**
   * 拿来比的墙钟（毫秒）。
   *
   * ⚠ **0 = 量不到** ⇒ `assessDurationDrift` 判 `no-ground-truth`
   *   （「没有那个量」与「那个量为 0」必须能分开 —— §803.1）。
   */
  wallClockMs: number
  /** 真实音频时长（秒）；`null` = 量不到。 */
  audioSec: number | null
  source: DurationGuardSource
  /**
   * §957：被扣掉的**切换空隙**（毫秒）。
   *
   * ⚠ `null` = 量不到（原生链但窗口不可信）；`0` = 没有空隙。
   * ⚠ 这不是「丢音频」——它是「那段时间**本来就没录**」：
   *   丢音频仍然会体现在 `driftMs` 里（空隙只扣轮次之间的那段）。
   */
  coverageGapMs: number | null
}

/**
 * §957：原生采集窗口**可不可信**（**纯函数** ⇒ node 里可直接测）。
 *
 * ⚠⚠ **规则变了（理由也变了）**：
 *   §955 的规则是「轮次 > 1 ⇒ 窗口不可信」，理由是「切过设备 ⇒ 原生时间轴**回卷**
 *   ⇒ 窗口错基」。
 *   ⇒ **那个理由已被 §956 消灭**（各轮已被精确重基到同一根会话时间轴）
 *   ⇒ 若还照旧，护栏在原生链跨设备时将**永远**判「量不到」。
 *
 * ⇒ 新规则：
 *   ① **单轮**（`runs <= 1`）⇒ **可信**（原生循环自身单调 ⇒ 窗口就是准的）
 *      —— 与 §955 一字不差 ⇒ **单轮行为不变**；
 *   ② **多轮** ⇒ 只有当会话时间轴**精确**（每片都带采集原点、从未被钳位）
 *      时才可信 ⇒ 因为只有精确时才分得清「切换空隙」与「丢片」
 *      ⇒ 护栏扣掉空隙才不会把真丢音频放过去。
 *   ⇒ 多轮且不精确 ⇒ **不可信**（与 §955 同一条 ⇒ 判「量不到」，宁可瞎也不误报）。
 */
export function nativeCaptureWindowTrustable(opts: {
  /** 这一场录音里原生服务启动过几轮。 */
  runs: number
  /** 会话时间轴是否精确（`isNativeTimelineExact`）。 */
  timelineExact: boolean
}): boolean {
  if (!Number.isFinite(opts.runs) || opts.runs <= 1) return true
  return opts.timelineExact === true
}

/**
 * §955：决定护栏拿**哪个墙钟**去比（**纯函数** ⇒ node 里可直接测）。
 *
 * ⚠⚠ **三条分支，每一条都有明确理由**：
 *   ① **不是原生链**（`nativeCapture` 缺省，或 `used` 为假）⇒ 用 **JS 墙钟**
 *      ⇒ **Web / iOS 链的行为与 §805 以来一字未变**。
 *   ② **原生链且窗口可信** ⇒ 用**原生采集窗口** ⇒ 与盘上音频**同源** ⇒ 护栏**真的能判**。
 *      ⚠⚠ **§957**：窗口里含**切换空隙**（那段时间没有音频）⇒ **要扣掉**
 *      ⇒ 扣完才是「真正录到音的那段墙钟」⇒ 与实测音频**同口径**。
 *   ③ **原生链但窗口不可信** ⇒ **不拿 JS 墙钟顶替**
 *      ⇒ 宁可判「量不到」（`wallClockMs = 0` ⇒ `no-ground-truth`），
 *      也不要拿一个**不同源**的墙钟制造**结构性系统偏移**（那会让短录音假警报）。
 *
 * ⚠ 为什么**不能**在③里退回 JS 墙钟：JS 墙钟与原生音频**不同源**，
 *   它的读数里带着一个**未实测的启动延迟** ⇒ 拿它比 ⇒ 短录音（容差只有 500 ms）
 *   几乎必然越界 ⇒ **每次都报「音频比墙钟短」** ⇒ 噪声 ⇒ 训练人忽略报警（§805.3）。
 */
export function resolveDurationGuardInput(opts: {
  /** 调用方拿到的墙钟（JS 侧 `Date.now() - startTime`）。 */
  elapsedMs: number
  /** 原生采集窗口快照；缺省 = 本次没有原生采集（⇒ 走①）。 */
  nativeCapture?: NativeCaptureSnapshot | null
  /** 实测音频总时长（秒）。 */
  measuredAudioSec?: number | null
}): DurationGuardInput {
  const audioSec = (
    typeof opts.measuredAudioSec === 'number'
    && Number.isFinite(opts.measuredAudioSec)
    && opts.measuredAudioSec > 0
  ) ? opts.measuredAudioSec : null

  const native = opts.nativeCapture
  if (!native || !native.used) {
    return { wallClockMs: opts.elapsedMs, audioSec, source: 'js-wall-clock', coverageGapMs: 0 }
  }
  if (typeof native.windowMs === 'number' && Number.isFinite(native.windowMs) && native.windowMs > 0) {
    // ⚠⚠ §957：扣掉**切换空隙**（那段时间没有音频）。
    //   ⚠ 扣减只认「有限的非负数」⇒ 非法值一律当 0（不得让窗口变成负数）。
    const gapMs = (
      typeof native.gapMs === 'number'
      && Number.isFinite(native.gapMs)
      && native.gapMs > 0
    ) ? native.gapMs : 0
    const wallClockMs = Math.max(0, native.windowMs - gapMs)
    return { wallClockMs, audioSec, source: 'native-window', coverageGapMs: gapMs }
  }
  return { wallClockMs: 0, audioSec, source: 'native-untrusted', coverageGapMs: null }
}

/**
 * §955：原生采集窗口在**收到新一片**之后是多少（**纯函数** ⇒ 可测）。
 *
 * ⚠⚠ 为什么不把它写成 runtime 里的一行赋值：
 *   `recordingRuntime.ts` 在 node 里跑不到（它 import vue / store / api）
 *   ⇒ 决策写在它体内 ⇒ 一行行为门都没有（§52 纪律：决策必须能测）。
 *   ⇒ 「窗口怎么累积」是**决策**（要不要单调？非法读数怎么办？）⇒ 提到纯函数里。
 *
 * ⚠ **必须单调不减**：原生侧每片的 `endMs` 相对它自己的 `captureStart`
 *   ⇒ 正常情况下单调增长；
 *   但**中途切过设备**时它会**回卷**（新的一轮从 0 重新开始）
 *   ⇒ 那时整个窗口是**错基**的 —— 那件事由 runtime 单独标成「不可信」，
 *   **不靠**这个函数来救（函数只保证「不因为一个乱序/非法读数而变小」）。
 */
export function nativeCaptureWindowAfterPart(currentMs: number, partEndMs: number): number {
  if (!Number.isFinite(partEndMs) || partEndMs < 0) return currentMs
  return partEndMs > currentMs ? partEndMs : currentMs
}

/**
 * §805.3 的诊断出口：把「护栏没判成异常」这件事的**原因**说出来。
 *
 * ⚠ 只在**有异常**时用（`finalizeRecording` 的告警文案里带上它）⇒
 *   「量不到」这件事本身**不喊**（§805.3：不要让每一次失败都喊一次时长不符）。
 */
export function describeDurationGuardSource(source: DurationGuardSource): string {
  switch (source) {
    case 'native-window':
      return '原生采集窗口（与盘上音频同源）'
    case 'native-untrusted':
      // ⚠⚠ §957：**理由换了**。§955 写的是「中途切过设备，原生时间轴回卷」——
      //   那个理由已被 §956 消灭（各轮已被精确重基）⇒ 照抄会把人引向错误的排查方向。
      return '原生采集窗口不可信（切过设备且会话时间轴不精确：拿不到采集原点或时间轴被钳位过）'
    default:
      return 'JS 墙钟'
  }
}

/**
 * §957：把「切换空隙」单独说成一句话（**纯函数** ⇒ 判据能直接断言它说了什么）。
 *
 * ⚠⚠ **为什么要有这一句**：空隙是「那段时间**本来就没录**」，
 *   它**不是**丢音频 ⇒ 不该混进「时长不符」的告警里 ⇒ 但也**不能吞掉**
 *   （用户确实少了一截录音）。
 * ⇒ 现状只到**开发者 console**（§813 的口径）；要不要升为用户可见
 *   属**产品口径**（待属主拍板，本节不代拍）。
 *
 * @returns `null` = 没有空隙（不需要留痕）。
 */
export function describeCoverageGap(gapMs: number | null): string | null {
  if (gapMs == null || !Number.isFinite(gapMs) || gapMs <= 0) return null
  const sec = Math.round(gapMs / 100) / 10
  return (
    `切换设备期间有 ${Math.round(gapMs)}ms（${sec}s）没有录音`
    + '（这段时间本来就没在采音，不算音频丢失；已从时长一致性比对里扣除）'
  )
}

/**
 * 换设备后续接录音时，**把计时起点往前挪**，让累计时长接着走。
 *
 * ⚠⚠ §814 的由来：`recordingRuntime.start()` 里 `this.startTime = Date.now()` 是
 * **无条件**执行的（只有 `elapsedMs = 0` 受 `freshSession` 保护）。于是 `switchDevice`
 * 里旧写法 `elapsedMs.value = keptElapsed` 只能维持 **≤200ms** ——
 * 下一个 interval tick 就按新起点把它覆盖成「切换之后」的时长，**累计时长就此丢失**。
 * ⇒ 连 §812 的 `durationMs` 也会跟着少算切换前的那一截。
 *
 * 挪起点等于让 interval（`Date.now() - startTime`）与写入时刻的重算**都**从
 * `keptElapsed` 自然续上 ⇒ 一行修好两个消费者。
 */
export function carriedStartTime(now: number, keptElapsedMs: number): number {
  const elapsed = Number.isFinite(keptElapsedMs) && keptElapsedMs > 0 ? keptElapsedMs : 0
  return now - elapsed
}

/** `refetchFullTranscript` 返回值的**最小**形状（本模块只读这三个字段）。 */
export interface RefetchOutcomeLike {
  applied: boolean
  reason?: string
  transcodeFailed?: boolean
  transcodeError?: string
}

/**
 * §818：把「转码失败 / 整段重转失败」翻译成一条**诊断文案**，null = 一切正常。
 *
 * ⚠⚠ **为什么必须是纯函数而不是写在 `finalizeRecording` 里**：那个函数依赖
 * localDB + HTTP，**在 node 里跑不到**（§52 的老问题：决策埋在跑不到的函数体里，
 * 一行行为门都没有）。第一版修复就把它写在调用点 ⇒ 源码扫描判据对着 `console.warn`
 * 匹配，**把 warn 改成 `void 0 && console.warn` 照样全绿** ——
 * 判据贴在语法形状上，对「warn 变死代码」这个最可能的变异零牙。
 *
 * ⇒ 决策搬到这里，就能用真行为用例盯住；调用点只剩「拿到文案就 warn」这一行。
 *
 * 两条信号刻意**分开**表述：转码失败是**根因**，`reason` 是它的**下游症状**
 * （退回原容器 → 网关拒收 → `empty-transcript` / `transcribe-failed`）。
 * 只报症状的话，排查的人会去查网关，而真问题在转码。
 */
export function finalizeFailureNotice(outcome: RefetchOutcomeLike): string | null {
  const notes: string[] = []
  if (outcome.transcodeFailed) {
    notes.push(
      '转码失败：录音未能转成 WAV，已退回原容器上传'
      + `（${outcome.transcodeError || '无异常信息'}）。`
      + '网关只收 wav/mp3 ⇒ 整段重转很可能随之失败。',
    )
  }
  // ⚠ 判据刻意是 `!applied` **而不是** `!applied && reason`：
  //   `refetchFullTranscript` 当前每条失败路径都带 `reason`，但那不是类型强制
  //   （`reason?: string`）⇒ 将来谁加一条不带 reason 的失败路径，文案就会整个消失。
  //   失败本身必须出声，reason 只是让它更好定位。
  if (!outcome.applied) {
    notes.push(`整段重转未生效，已降级为分段文本：${outcome.reason || '未给出原因'}`)
  }
  if (notes.length === 0) return null
  return notes.join(' ')
}
