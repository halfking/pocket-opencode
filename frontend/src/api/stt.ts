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
import { http, LONG_REQUEST_TIMEOUT_MS } from './http'
import { blobToBase64 } from '../utils/base64'
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
  async transcribe(opts: SttOptions): Promise<SttResult> {
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

    // 音频转写比普通 CRUD 慢一个量级（整段 base64 上传 + 推理），
    // 给到 LONG 上限；但仍必须有上限，否则录音停止链路会被拖死。
    const res = await http<{ text: string; confidence: number; costCents?: number }>(
      '/api/stt/transcribe',
      { method: 'POST', body, timeoutMs: LONG_REQUEST_TIMEOUT_MS },
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
