/**
 * Web 端录音容器的 WAV 转码（2026-10-05 网关音频端点轮新增）。
 *
 * 为什么必须转：MediaRecorder 在 WebView/浏览器里的默认产物是
 * webm/opus。LLM 网关的小米 MiMo ASR 桥接只接受 wav/mp3（上游实测
 * 400 "input_audio.format must be one of: wav, mp3"），web 端的云端
 * 转写在网关通道上实际不可用。sherpa 本地引擎只覆盖 native，web 端
 * 没有本地兜底——不转码就没有可用路径。
 *
 * 方案：AudioContext.decodeAudioData 解码任意容器 → 重采样 16kHz 单声道
 * → 16-bit PCM WAV（ASR 的标准输入形态，体积也小于原始 webm/opus 的
 * 场景不多，但换来全通道兼容：智谱 glm-asr 同样只收 wav/mp3）。
 *
 * 失败语义：decodeAudioData 解不了（罕见容器/损坏数据）时抛错，由调用
 * 方决定回退原 blob 上传——不比现状差。
 */

const TARGET_SAMPLE_RATE = 16000

/** 把任意音频 Blob 转成 16kHz 单声道 16-bit PCM WAV Blob。 */
export async function encodeWav16kMono(input: Blob): Promise<Blob> {
  const AudioCtx: typeof AudioContext =
    window.AudioContext ??
    (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext
  if (!AudioCtx) throw new Error('no AudioContext available for wav transcode')
  const ctx = new AudioCtx()
  try {
    const buf = await ctx.decodeAudioData(await input.arrayBuffer())
    const ratio = buf.sampleRate / TARGET_SAMPLE_RATE
    // 混合到单声道再重采样：逐目标帧取源区间平均，避免通道分离。
    const channels = buf.numberOfChannels
    const mono = new Float32Array(Math.floor(buf.length / ratio) || 1)
    for (let i = 0; i < mono.length; i++) {
      const start = Math.floor(i * ratio)
      const end = Math.min(buf.length, Math.floor((i + 1) * ratio))
      let sum = 0
      let n = 0
      for (let c = 0; c < channels; c++) {
        const ch = buf.getChannelData(c)
        for (let j = start; j < end; j++) {
          sum += ch[j]
          n++
        }
      }
      mono[i] = n > 0 ? sum / n : 0
    }
    return new Blob([encodeWav(mono, TARGET_SAMPLE_RATE)], { type: 'audio/wav' })
  } finally {
    void ctx.close()
  }
}

/** Float32 单声道 → 16-bit PCM WAV 容器。 */
function encodeWav(samples: Float32Array, sampleRate: number): ArrayBuffer {
  const bytesPerSample = 2
  const dataSize = samples.length * bytesPerSample
  const buffer = new ArrayBuffer(44 + dataSize)
  const view = new DataView(buffer)
  const writeStr = (offset: number, s: string) => {
    for (let i = 0; i < s.length; i++) view.setUint8(offset + i, s.charCodeAt(i))
  }
  writeStr(0, 'RIFF')
  view.setUint32(4, 36 + dataSize, true)
  writeStr(8, 'WAVE')
  writeStr(12, 'fmt ')
  view.setUint32(16, 16, true)
  view.setUint16(20, 1, true) // PCM
  view.setUint16(22, 1, true) // mono
  view.setUint32(24, sampleRate, true)
  view.setUint32(28, sampleRate * bytesPerSample, true)
  view.setUint16(32, bytesPerSample, true)
  view.setUint16(34, 16, true)
  writeStr(36, 'data')
  view.setUint32(40, dataSize, true)
  let offset = 44
  for (let i = 0; i < samples.length; i++, offset += 2) {
    const s = Math.max(-1, Math.min(1, samples[i]))
    view.setInt16(offset, s < 0 ? s * 0x8000 : s * 0x7fff, true)
  }
  return buffer
}

/** webm/ogg 等网关不支持容器的判定（wav/mp3/flac/m4a 直传）。 */
export function needsWavTranscode(mimeType: string): boolean {
  const t = (mimeType || '').toLowerCase().split(';', 1)[0].trim()
  return t === 'audio/webm' || t === 'video/webm' || t === 'audio/ogg' || t === '' || t === 'application/octet-stream'
}

/**
 * §819：转码失败**必须留痕**。
 *
 * 降级行为一行不改（仍是「退回原 blob 上传」，见下），只是把失败本身说出来：
 * 此前这里是空 catch 块，失败被整个吞掉 ⇒ 后果只呈现为「网关拒收 webm」
 * ⇒ 排查的人会去查网关，而真问题在**转码这一步**（§818 在会议整段重转那条路上
 * 查到的同一个洞；那条路已修，这 4 个 `ensureGatewayCompatible` 调用点是同一类）。
 *
 * ⚠ 触达面与 §813 查到的现状一致：**只到开发者 console**。
 *   要不要升到用户可见属产品口径，待属主拍板，本处不代拍。
 *
 * ⚠ 不会刷屏：本函数的调用方都是「一段录音结束时调一次」
 *   （`useVoiceInput` / `useVoiceRecording` 都在 `onstop` 里转整段），
 *   **不是逐片调用** ⇒ 每次失败一条，不会把设备 console 冲垮。
 */
export interface TranscodeFailureInfo {
  /** 原容器的 MIME（退回上传的就是它）。 */
  mimeType: string
  /** 原容器字节数。 */
  bytes: number
  /** 原始异常，便于区分「解码失败」与「环境没有 AudioContext」。 */
  error: unknown
}

/** 转码失败时的通报回调；不传则走默认的 `console.warn`。 */
export type TranscodeFailureReporter = (info: TranscodeFailureInfo) => void

const defaultTranscodeFailureReporter: TranscodeFailureReporter = (info) => {
  const why = info.error instanceof Error ? info.error.message : String(info.error)
  console.warn(
    `[wav-encode] 转码失败：${info.bytes} 字节 ${info.mimeType || '(无 MIME)'} 未能转成 WAV`
    + `（${why}）。已退回原容器上传 —— 网关只收 wav/mp3，本次转写很可能随之失败。`,
  )
}

/** 组合入口：需要转码且成功 → wav Blob；否则原样返回（含转码失败的回退）。 */
export async function ensureGatewayCompatible(
  blob: Blob,
  onTranscodeFailure: TranscodeFailureReporter = defaultTranscodeFailureReporter,
): Promise<Blob> {
  if (!needsWavTranscode(blob.type)) return blob
  try {
    return await encodeWav16kMono(blob)
  } catch (e) {
    // 解码不了就原样上传（external OpenAI 通道仍支持 webm），不比现状差。
    // ★§819 失败要出声：行为不变，可见性变。
    try {
      onTranscodeFailure({ mimeType: blob.type, bytes: blob.size, error: e })
    } catch {
      /* 通报本身失败不得反过来影响降级 */
    }
    return blob
  }
}

/**
 * `refetchFullTranscript` 的 `toWav` 契约形态（§50）。
 *
 * 为什么放在这里而不是藏在收尾模块里：这是**网关音频适配**的一部分，
 * 收尾模块只是它的一个调用方。放这里意味着
 * ① 随手记语音路径与会议录音路径共用同一个判据（`needsWavTranscode`），
 * ② 它是导出函数、可被单测直接验，而不是只能靠源码扫描看它「在不在」。
 *
 * 返回 null 表示「这个容器本来就合规，不要转」——调用方据此保留原 blob。
 */
export async function toGatewayWavBytes(blob: Blob): Promise<ArrayBuffer | null> {
  if (!needsWavTranscode(blob.type)) return null
  const wav = await encodeWav16kMono(blob)
  return await wav.arrayBuffer()
}
