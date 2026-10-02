/**
 * Speech-to-text scheduler.
 *
 * Implements the "local-first + cloud-fallback" strategy:
 *   1. Try on-device sherpa-onnx (Paraformer for Chinese) — native only.
 *   2. If unavailable or low-confidence, fall back to Groq Whisper Large v3
 *      Turbo via the backend (POST /api/stt/transcribe).
 *
 * On web (no native plugin), skips local and goes straight to cloud.
 */
import { sherpa } from '../native/sherpa'
import { http } from './http'
import { blobToBase64 } from '../utils/base64'

/**
 * 单段语音转写的客户端超时。
 *
 * 服务端 Transcriber 自身 120 秒。取 180 秒而不是取 120——见调用处的注释：
 * **与服务端相等等于客户端先到点**。
 */
export const STT_TRANSCRIBE_TIMEOUT_MS = 3 * 60_000
import { filenameForMimeType } from './stt-filename'
import {
  CLOUD_STT_NEED_BLOB,
  requireCloudAudioBlob,
  type SttOptions,
} from './stt-cloud.ts'

export interface SttResult {
  text: string
  confidence: number
  engine: 'local' | 'cloud'
  costCents?: number
}

export type { SttOptions }
export { CLOUD_STT_NEED_BLOB, requireCloudAudioBlob }

// 文件名推导已抽到 stt-filename.ts：单次/全量/即时三条转写路径必须共用
// 同一张 MIME→扩展名映射，否则会出现「单次能转、全量转不出」的割裂。
export { filenameForMimeType }
export const sttApi = {
  /**
   * Transcribe recorded audio with automatic fallback.
   * Pass `audioBlob` for web recordings, `audioPath` for native file paths.
   */
  async transcribe(opts: SttOptions, signal?: AbortSignal): Promise<SttResult> {
    const minConf = opts.minConfidence ?? 0.7

    // Try local sherpa-onnx first (native only, needs file path).
    if (opts.forceEngine !== 'cloud' && opts.audioPath) {
      try {
        const local = await sherpa.transcribe(opts.audioPath)
        if (local.confidence >= minConf) {
          return {
            text: local.text,
            confidence: local.confidence,
            engine: 'local',
          }
        }
      } catch {
        // Local engine not available: fall through to cloud.
      }
    }

    const audioBlob = requireCloudAudioBlob(opts)
    const base64 = await blobToBase64(audioBlob)
    const body = JSON.stringify({
      audioBase64: base64,
      filename: filenameForMimeType(audioBlob.type || 'audio/webm'),
    })

    // 音频转写比普通 CRUD 慢一个量级（整段 base64 上传 + 推理）。
    //
    // 2026-10-03：原先用的是通用的 LONG_REQUEST_TIMEOUT_MS，正好 120 秒，
    // 而服务端 Transcriber 自己就 120 秒（server.go 的 longLivedPaths
    // 注释里记着）。**相等即错**：客户端计时从请求发出开始，服务端的从
    // handler 进来开始，中间还隔着网络与鉴权，所以客户端实际总是先到点，
    // 于是「刚好用满预算」的那一档必然失败。取 3 分钟。
    // signal 透传给 http：调用方（如 useVoiceInput 的孤儿转写）据此可以
    // 在页面离开后仍跑的同时**仍被中止**。http 的 HttpOptions.signal 早就是
    // 接好的，这里只是把它从这条长任务链上暴露出来——此前这条链是整条
    // stt 链里唯一没有中止入口的环节，而它恰恰是设计上要跨页存活的。
    const res = await http<{ text: string; confidence: number; costCents?: number }>(
      '/api/stt/transcribe',
      { method: 'POST', body, timeoutMs: STT_TRANSCRIBE_TIMEOUT_MS, signal },
    )
    return {
      text: res.text,
      confidence: res.confidence,
      engine: 'cloud',
      costCents: res.costCents,
    }
  },

  /** Stream-oriented helper for the recorder widget (native only). */
  async startStreaming(): Promise<void> {
    return sherpa.startListening()
  },
  async stopStreaming() {
    const res = await sherpa.stopListening()
    return res.final ?? res
  },
}
