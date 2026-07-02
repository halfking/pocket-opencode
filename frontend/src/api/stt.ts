/**
 * Speech-to-text scheduler.
 *
 * Implements the "local-first + cloud-fallback" strategy from
 * docs/2026-07-02-android-stt-evaluation.md:
 *
 *   1. Try on-device sherpa-onnx (Paraformer for Chinese).
 *   2. If unavailable or low-confidence, fall back to Groq Whisper Large v3
 *      Turbo via the backend (POST /api/stt/transcribe).
 *
 * The decision also considers device tier (see stores/device.ts): on low-end
 * devices we may prefer cloud by default to save battery.
 */
import { sherpa, type SherpaResult } from '../native/sherpa'
import { http } from './http'

export interface SttResult {
  text: string
  confidence: number
  engine: 'local' | 'cloud'
  /** Cloud cost indicator for telemetry; 0 for local. */
  costCents?: number
}

export interface SttOptions {
  /** Force a specific engine; otherwise auto-decides. */
  forceEngine?: 'local' | 'cloud'
  /** Confidence below which we retry on cloud. Default 0.7. */
  minConfidence?: number
  audioPath: string
}

export const sttApi = {
  /** Transcribe a recorded audio file with automatic fallback. */
  async transcribe(opts: SttOptions): Promise<SttResult> {
    const minConf = opts.minConfidence ?? 0.7

    // Try local first unless forced to cloud.
    if (opts.forceEngine !== 'cloud') {
      try {
        const local = await sherpa.transcribe(opts.audioPath)
        if (local.confidence >= minConf) {
          return {
            text: local.text,
            confidence: local.confidence,
            engine: 'local',
          }
        }
        // Low confidence: fall through to cloud.
      } catch {
        // Local engine not available: fall through to cloud.
      }
    }

    // Cloud fallback via pocketd → Groq Whisper Large v3 Turbo.
    const res = await http<{ text: string; confidence: number; costCents?: number }>(
      '/api/stt/transcribe',
      {
        method: 'POST',
        body: JSON.stringify({ audioPath: opts.audioPath }),
      },
    )
    return {
      text: res.text,
      confidence: res.confidence,
      engine: 'cloud',
      costCents: res.costCents,
    }
  },

  /** Stream-oriented helper for the recorder widget (delegates to sherpa native). */
  async startStreaming(): Promise<void> {
    return sherpa.startListening()
  },
  async stopStreaming(): Promise<SherpaResult> {
    const res = await sherpa.stopListening()
    // sherpa.stopListening 返回 { final: SherpaResult }，展开
    return res.final ?? res
  },
}
