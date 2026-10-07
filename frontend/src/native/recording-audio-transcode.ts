/**
 * 录音分片 → 16k 单声道 WAV（前端转码）。
 *
 * ## 为什么需要它（2026-10-05 真机实测）
 *
 * 真机（Redmi 2411DRN47C / WebView 126）的 MediaRecorder 实测：
 *
 *   MediaRecorder.isTypeSupported('audio/wav')  === false   ← 录不出 wav
 *   pickSupportedRecorderMime() 的第一候选         === 'audio/webm;codecs=opus'
 *
 * 而网关（llm.kxpms.cn）对 webm 的答复是硬拒：
 *
 *   400 invalid_audio_request: audio format "webm" is not supported
 *   by the chat-audio bridge (supported: mp3, wav)
 *
 * 对照实验（同一段音频，只换容器）：webm → 400，wav → 200 且转写正确。
 * ⇒ 真机上「录音 → 转写」**恒失败**，界面只显示「转写失败，将在下一段重试」。
 *
 * 后端不转码是有据的：`internal/stt/full.go` 只在能解析的容器（WAV/裸 PCM）
 * 上切段，webm 的 cluster 边界不能按字节偏移推断。所以这一步放在前端——
 * 我们拿到的是**同一份字节**，只是换个容器交给上游。
 *
 * ## 为什么不能「逐片独立转码」——这是本文件最要紧的设计约束
 *
 * MediaRecorder 每 3 秒派发一个 webm blob。实测这些分片**除第一片外全部无法解码**：
 *
 *   片0  22330 B  decodable=true    ← 含 EBML 头 + 初始化段
 *   片1    700 B  decodable=false   UnsupportedError
 *   片2    700 B  decodable=false
 *   片3    111 B  decodable=false
 *   全部拼接 23841 B  decodable=true (9.48s)
 *
 * WebM 是流式容器：初始化段只在开头出现一次。所以逐片 decodeAudioData
 * 在第 2 片就会抛 UnsupportedError——**这条路不成立**。
 *
 * ## 实际方案：滚动累计 + 时间窗切片
 *
 * 保留全部原始分片（就是 `this.chunks` 本来就要留的东西，不额外占内存），
 * 每来一片就把**累计容器**重新解码一次，再按 `[已发送秒数, 当前总秒数)`
 * 切出**新增**的那一段打成 WAV。
 *
 * 为什么不发累计音频：即时转写的服务端会话会累积文本，重发旧内容会得到
 * 「今天今天下午三点」这类重复。只发新增窗口，服务端既有去重逻辑就仍然成立。
 *
 * 复杂度是 O(n²) 解码，但实测单片解码 3~9 秒音频 < 30ms，
 * 且每 3 秒才触发一次，一场十几分钟的录音完全可接受。
 */

const TARGET_SAMPLE_RATE = 16000

/** 16k 单声道 PCM16 WAV 的文件头（44 字节）。 */
function writeWavHeader(view: DataView, dataBytes: number): void {
  const ascii = (offset: number, s: string) => {
    for (let i = 0; i < s.length; i++) view.setUint8(offset + i, s.charCodeAt(i))
  }
  ascii(0, 'RIFF')
  view.setUint32(4, 36 + dataBytes, true)
  ascii(8, 'WAVE')
  ascii(12, 'fmt ')
  view.setUint32(16, 16, true)          // fmt chunk size
  view.setUint16(20, 1, true)           // PCM
  view.setUint16(22, 1, true)           // mono
  view.setUint32(24, TARGET_SAMPLE_RATE, true)
  view.setUint32(28, TARGET_SAMPLE_RATE * 2, true)  // byte rate
  view.setUint16(32, 2, true)           // block align
  view.setUint16(34, 16, true)          // bits per sample
  ascii(36, 'data')
  view.setUint32(40, dataBytes, true)
}

/**
 * 线性插值重采样 + 打 WAV。
 *
 * 用线性插值而不是 `OfflineAudioContext` 重采样：前者是纯函数、可单测，
 * 且不需要再开一个 AudioContext（真机上多开上下文有被系统回收的风险）。
 * 16k↔48k 是 3:1 整比，线性插值的误差在这个场景下不影响可懂度。
 */
export function pcmToWavBytes(samples: Float32Array, sampleRate: number): ArrayBuffer {
  const ratio = sampleRate / TARGET_SAMPLE_RATE
  const outLength = Math.max(0, Math.floor(samples.length / ratio))
  const bytes = new ArrayBuffer(44 + outLength * 2)
  const view = new DataView(bytes)
  writeWavHeader(view, outLength * 2)
  for (let i = 0; i < outLength; i++) {
    const pos = i * ratio
    const i0 = Math.floor(pos)
    const frac = pos - i0
    const a = samples[i0] ?? 0
    const b = samples[i0 + 1] ?? a
    const s = Math.max(-1, Math.min(1, a + (b - a) * frac))
    view.setInt16(44 + i * 2, s < 0 ? s * 0x8000 : s * 0x7fff, true)
  }
  return bytes
}

/**
 * 从 Float32 PCM 里切出 `[fromSec, toSec)` 并打成 WAV。
 * 越界自动收敛到可用范围，返回 0 长度而不是抛错（静音片段是正常情况）。
 */
export function sliceToWavBytes(
  samples: Float32Array,
  sampleRate: number,
  fromSec: number,
  toSec: number,
): ArrayBuffer {
  const start = Math.max(0, Math.floor(fromSec * sampleRate))
  const end = Math.min(samples.length, Math.floor(toSec * sampleRate))
  if (end <= start) return pcmToWavBytes(new Float32Array(0), sampleRate)
  return pcmToWavBytes(samples.subarray(start, end), sampleRate)
}

/** ArrayBuffer → base64。分块转换：12 秒 PCM 实测一次性展开会爆栈。 */
export function arrayBufferToBase64(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer)
  let binary = ''
  const CHUNK = 32768
  for (let i = 0; i < bytes.length; i += CHUNK) {
    binary += String.fromCharCode.apply(null, bytes.subarray(i, i + CHUNK) as unknown as number[])
  }
  return btoa(binary)
}

export type DecodeContext = {
  decode: (arrayBuffer: ArrayBuffer) => Promise<{ samples: Float32Array; sampleRate: number; durationSec: number }>
  dispose?: () => void
}

/**
 * 滚动音频解码器。
 *
 * 用法：每来一个 MediaRecorder 分片就 `push()`，再 `takeNewWindow()` 取出自
 * 上次取用之后新增的那段 WAV。**取用游标由本类维护**，调用方不需要自己记秒数。
 */
export class RollingWebmDecoder {
  private parts: Blob[] = []
  private context: DecodeContext | null = null
  private sentUpToSec = 0
  private disposed = false

  /**
   * 注入解码实现（测试用；生产不传，走 AudioContext）。
   *
   * 做成构造参数而不是测试专用的 `__test_inject` 方法：解码是这个类里唯一
   * 需要真 WebM 才能验的部分，而 WebM 夹具在 Node 里造不出来。
   *
   * ⚠️ 这里刻意**不用** TS 参数属性（`constructor(private readonly x)`）：
   * 那是 node 的 strip-only 模式唯一不支持的语法，会让本模块**无法被单测直接
   * import**（实测 `TypeScript parameter property is not supported in
   * strip-only mode`）。本仓已有 8 处 mjs 测试直接 import .ts 的先例，
   * 断掉这条路等于把纯函数也降级成「只能测契约文本」。
   */
  private readonly decodeImpl?: DecodeContext['decode']

  constructor(decodeImpl?: DecodeContext['decode']) {
    this.decodeImpl = decodeImpl
  }

  // ⚠️ 这里**刻意不提供** `durationSec` 之类的 getter。
  // 初版给 `sentUpToSec` 加过一个叫 durationSec 的 getter，注释写
  // 「当前累计音频的总时长」——但它返回的其实是**已取用的游标**，
  // 与「累计音频有多长」不是一回事（解码器还没跑到的那段也算时长）。
  // 全仓无调用方，却是个给后人的雷：先读注释的人会拿它去算窗口，
  // 于是把「还没发的音频」当成「已发的」。宁可不提供。

  push(part: Blob): void {
    if (this.disposed) return
    this.parts.push(part)
  }

  private ensureContext(): DecodeContext {
    if (this.decodeImpl) {
      // 注入模式：不建 AudioContext（Node 里根本没有）。
      if (!this.context) this.context = { decode: this.decodeImpl }
      return this.context
    }
    if (this.context) return this.context
    const scope = globalThis as unknown as {
      AudioContext?: typeof AudioContext
      webkitAudioContext?: typeof AudioContext
    }
    const Ctor = scope.AudioContext ?? scope.webkitAudioContext
    if (!Ctor) throw new Error('NO_AUDIO_CONTEXT')
    const ctx = new Ctor()
    this.context = {
      decode: async (arrayBuffer) => {
        const decoded = await ctx.decodeAudioData(arrayBuffer.slice(0))
        // 取第 0 声道：录音约束是 channelCount:1，但真机可能无视它。
        return {
          samples: decoded.getChannelData(0),
          sampleRate: decoded.sampleRate,
          durationSec: decoded.duration,
        }
      },
      dispose: () => { try { void ctx.close() } catch { /* 已关闭 */ } },
    }
    return this.context
  }

  /**
   * 解码累计音频并切出**新增**窗口。
   *
   * @returns WAV 字节；无新增内容时返回 null（**不是** 0 长度 WAV——
   *   0 长度会让上游回 502 empty transcript，触发一次无意义的错误展示）。
   */
  async takeNewWindow(minWindowSec = 0.15): Promise<ArrayBuffer | null> {
    if (this.disposed || !this.parts.length) return null
    const ctx = this.ensureContext()
    const blob = new Blob(this.parts, { type: 'audio/webm' })
    const decoded = await ctx.decode(await blob.arrayBuffer())
    const total = decoded.durationSec
    if (total - this.sentUpToSec < minWindowSec) {
      // 还没攒够新内容：把游标推到末尾，避免下次重复算同一段。
      this.sentUpToSec = total
      return null
    }
    const from = this.sentUpToSec
    this.sentUpToSec = total
    return sliceToWavBytes(decoded.samples, decoded.sampleRate, from, total)
  }

  /** 停止录音后的整段音频（走 transcribe-full 兜底）。 */
  async takeFull(): Promise<ArrayBuffer | null> {
    if (this.disposed || !this.parts.length) return null
    const ctx = this.ensureContext()
    const blob = new Blob(this.parts, { type: 'audio/webm' })
    const decoded = await ctx.decode(await blob.arrayBuffer())
    return pcmToWavBytes(decoded.samples, decoded.sampleRate)
  }

  /** 全部原始分片拼成的 webm（保留原格式时用）。 */
  fullBlob(): Blob {
    return new Blob(this.parts, { type: 'audio/webm' })
  }

  dispose(): void {
    this.disposed = true
    this.parts = []
    this.context?.dispose?.()
    this.context = null
  }
}
