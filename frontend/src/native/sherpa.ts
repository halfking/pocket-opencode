/**
 * cap-sherpa plugin — local speech recognition via sherpa-onnx.
 *
 * Wraps the native Android plugin that bundles sherpa-onnx Paraformer /
 * SenseVoice models for on-device Chinese/English ASR. See
 * docs/2026-07-02-android-stt-evaluation.md for the model selection.
 *
 * Falls back to unsupported on web; callers should use stt.ts which
 * transparently falls back to cloud (Groq Whisper) when local is absent.
 */
import { registerPluginSafely } from './util'

export interface SherpaResult {
  text: string
  confidence: number           // 0..1
  rtf: number                  // real-time factor achieved
  engine: 'paraformer' | 'sensevoice' | 'whisper-base' | 'zipformer'
}

export interface SherpaEmbeddingResult {
  embedding: Float32Array
  dim: number
}

export interface SherpaPartialResult {
  text: string
  isFinal: boolean
  startMs: number
  endMs: number
}

export interface CapSherpaPlugin {
  /** Preload a model so first recognition is fast. */
  preload(model: 'paraformer' | 'sensevoice' | 'whisper-base'): Promise<void>
  /** Transcribe a local audio file path (WAV/PCM 16kHz mono). */
  transcribe(audioPath: string): Promise<SherpaResult>
  /** Extract speaker embedding (ECAPA-TDNN). */
  extractEmbedding(audioPath: string): Promise<SherpaEmbeddingResult>
  /** Start VAD-gated streaming recognition; emits partial results via events. */
  startListening(): Promise<void>
  stopListening(): Promise<{ final: SherpaResult }>
  /** Register listener for partial transcription (native only). */
  addListener(
    event: 'partialResult',
    handler: (result: SherpaPartialResult) => void,
  ): Promise<{ remove: () => void }>
}

export const sherpa = registerPluginSafely<CapSherpaPlugin>('Sherpa', {
  transcribe: () => Promise.reject(new Error('cap-sherpa not available')),
  preload: () => Promise.reject(new Error('cap-sherpa not available')),
  extractEmbedding: () => Promise.reject(new Error('cap-sherpa not available')),
  startListening: () => Promise.reject(new Error('cap-sherpa not available')),
  stopListening: () => Promise.reject(new Error('cap-sherpa not available')),
  addListener: () => Promise.reject(new Error('cap-sherpa not available')),
})
