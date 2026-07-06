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

export interface SttResult {
  text: string
  confidence: number
  engine: 'local' | 'cloud'
  costCents?: number
}

export interface SttOptions {
  /** Audio blob from MediaRecorder (web). */
  audioBlob?: Blob
  /** File path for native sherpa-onnx (Capacitor Android). */
  audioPath?: string
  /** Force a specific engine. */
  forceEngine?: 'local' | 'cloud'
  /** Confidence below which we retry on cloud. Default 0.7. */
  minConfidence?: number
}

/** Convert a Blob to base64 string (without data: prefix). */
function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onloadend = () => {
      const dataUrl = reader.result as string
      const base64 = dataUrl.split(',')[1] || ''
      resolve(base64)
    }
    reader.onerror = reject
    reader.readAsDataURL(blob)
  })
}

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

    // Cloud fallback via pocketd -> Groq Whisper Large v3 Turbo.
    // Send audio as base64 JSON (works in both web and native).
    let body: string
    if (opts.audioBlob) {
      const base64 = await blobToBase64(opts.audioBlob)
      body = JSON.stringify({ audio: base64, mimeType: opts.audioBlob.type || 'audio/webm' })
    } else if (opts.audioPath) {
      body = JSON.stringify({ audioPath: opts.audioPath })
    } else {
      throw new Error('sttApi.transcribe: provide audioBlob or audioPath')
    }

    const res = await http<{ text: string; confidence: number; costCents?: number }>(
      '/api/stt/transcribe',
      { method: 'POST', body },
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
