/**
 * cap-sherpa plugin — local speech recognition via sherpa-onnx.
 *
 * Phase 4 落地（2026-10-06）：Android 原生插件已实现双引擎——
 * zipformer（流式实时出字）与 SenseVoice（整段本地高精档）；模型首次
 * 使用时由原生层运行时下载（downloadProgress 事件播报进度），不进 APK。
 * 选型与精度实测记录见 docs/2026-10-06-local-asr-sherpa-integration.md。
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

export interface SherpaStatus {
  zipformerReady: boolean
  sensevoiceReady: boolean
  listening: boolean
  modelsDir: string
}

export interface CapSherpaPlugin {
  /** Preload a model so first recognition is fast (downloads on first use). */
  preload(model: 'zipformer' | 'sensevoice'): Promise<void>
  /** Transcribe a local audio file path (WAV/PCM 16kHz mono). */
  transcribe(audioPath: string): Promise<SherpaResult>
  /** Extract speaker embedding (ECAPA-TDNN, Phase 5; rejects for now). */
  extractEmbedding(audioPath: string): Promise<SherpaEmbeddingResult>
  /** Start VAD-gated streaming recognition; emits partial results via events. */
  startListening(): Promise<void>
  stopListening(): Promise<{ final: SherpaResult }>
  /** Model readiness / models dir (route local vs cloud decisions). */
  status(): Promise<SherpaStatus>
  /** Register listener for partial transcription / download progress (native only). */
  addListener(
    event: 'partialResult' | 'downloadProgress',
    handler: (result: any) => void,
  ): Promise<{ remove: () => void }>
}

export const sherpa = registerPluginSafely<CapSherpaPlugin>('Sherpa', {
  transcribe: () => Promise.reject(new Error('cap-sherpa not available')),
  preload: () => Promise.reject(new Error('cap-sherpa not available')),
  extractEmbedding: () => Promise.reject(new Error('cap-sherpa not available')),
  startListening: () => Promise.reject(new Error('cap-sherpa not available')),
  stopListening: () => Promise.reject(new Error('cap-sherpa not available')),
  status: () => Promise.reject(new Error('cap-sherpa not available')),
  addListener: () => Promise.reject(new Error('cap-sherpa not available')),
})
