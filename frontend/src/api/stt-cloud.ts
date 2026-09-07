/**
 * Pure cloud-STT contract helpers.
 *
 * Kept dependency-free so node --test can load it directly (stt.ts pulls
 * in the sherpa plugin and the http/auth cycle, which node cannot resolve).
 */

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

export const CLOUD_STT_NEED_BLOB =
  '本地语音识别未完成时，需要可上传的音频数据才能使用云端转写'

/** Cloud STT requires the actual blob; a blob: URL is not a native file path. */
export function requireCloudAudioBlob(opts: SttOptions): Blob {
  if (opts.audioBlob) return opts.audioBlob
  if (opts.audioPath) throw new Error(CLOUD_STT_NEED_BLOB)
  throw new Error('sttApi.transcribe: provide audioBlob or audioPath')
}
