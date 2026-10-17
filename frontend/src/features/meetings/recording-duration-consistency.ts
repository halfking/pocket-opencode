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
 * 判定阈值与「报警但不阻断」是**产品口径**：本模块只给读数与判定，不抛错、
 * 不改写任何状态；调用方（`meeting-recording-finalize.ts`）只 `console.warn`。
 */

const WAV_HEADER_BYTES = 44
const WAV_BYTES_PER_SAMPLE = 2
/** `wav-encode.ts` 的 TARGET_SAMPLE_RATE，产出恒为 16kHz。 */
const WAV_SAMPLE_RATE = 16000

/**
 * 从 16kHz/16-bit/单声道 WAV 的**字节**算真实音频时长（秒）。
 *
 * 走字节而不是 `AudioBuffer.duration`，是因为本函数要在 node 测试里跑，
 * 也不需要浏览器环境 —— 那正是 §803 说「真值从来没被量过」时缺的东西。
 *
 * 返回 `null` 表示「这批字节不是可读的 16k PCM WAV」，而不是返回 0：
 * 0 会被读成「录了 0 秒」，那是**假读数**（§803.1 同型：没有那个量，不是那个量为 0）。
 */
export function wavDurationSec(wav: ArrayBuffer | Uint8Array): number | null {
  const bytes = wav instanceof Uint8Array ? wav : new Uint8Array(wav)
  if (bytes.byteLength <= WAV_HEADER_BYTES) return null
  const dataBytes = bytes.byteLength - WAV_HEADER_BYTES
  if (dataBytes % WAV_BYTES_PER_SAMPLE !== 0) return null
  return dataBytes / WAV_BYTES_PER_SAMPLE / WAV_SAMPLE_RATE
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
