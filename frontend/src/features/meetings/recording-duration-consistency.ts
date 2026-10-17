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
 * `reason = 'no-ground-truth'` —— **量不到不等于没问题**（§803.4 的静默缺口）。
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
