/**
 * speaker-embedding.ts — 声纹 embedding 提取（**I/O 层**）
 *
 * 原生：cap-sherpa ECAPA-TDNN（Android）
 * Web 兜底：log-mel 频谱特征（48 维，见 speaker-features.ts）
 *
 * ⚠ 本文件刻意只做 I/O，所有特征计算都在 `speaker-features.ts`（零 import），
 *   所以那个纯模块能在 Node 里被门禁真的执行到。
 */
import { sherpa } from './sherpa'
import {
  blobToEmbedding,
  cosineSimilarity,
  embeddingToBlob,
  isComparable,
  isUsableEmbedding,
  logMelEmbedding,
} from './speaker-features.ts'

/**
 * 提取结果。**刻意是判别联合类型**，不是 `Float32Array`。
 *
 * 为什么：原实现返回 `Promise<Float32Array>`，调用方只能靠
 * 「拿到空数组」来猜失败，而 `ingest-speech.ts` 干脆写成
 * `.catch(() => new Float32Array())` —— 失败被伪装成一个**合法但无意义**的向量，
 * 于是它在聚类里表现为「与所有人都不像」⇒ 每段都新建一个说话人。
 * 类型必须能**表达「没有特征」**，否则调用方迟早会把它压回成空数组。
 */
export type EmbeddingResult =
  | { ok: true; embedding: Float32Array; source: 'native' | 'spectral' }
  | { ok: false; reason: 'sherpa-failed' | 'decode-failed' | 'audio-empty' }

/** 从音频 blob 提取 embedding，优先原生 ECAPA。 */
export async function extractEmbeddingResult(blob: Blob): Promise<EmbeddingResult> {
  try {
    const path = URL.createObjectURL(blob)
    try {
      const result = await sherpa.extractEmbedding(path)
      if (isUsableEmbedding(result.embedding)) {
        return { ok: true, embedding: result.embedding, source: 'native' }
      }
    } finally {
      URL.revokeObjectURL(path)
    }
  } catch { /* 原生不可用，走 Web 兜底 */ }

  let pcm: Float32Array
  try {
    pcm = await decodeToPcm(blob)
  } catch {
    return { ok: false, reason: 'decode-failed' }
  }
  if (pcm.length === 0) return { ok: false, reason: 'audio-empty' }
  const embedding = logMelEmbedding(pcm)
  // 短于一帧 ⇒ logMelEmbedding 返回零向量 ⇒ 同样是「没有特征」。
  if (!isUsableEmbedding(embedding)) return { ok: false, reason: 'audio-empty' }
  return { ok: true, embedding, source: 'spectral' }
}

/**
 * 兼容入口：拿不到时返回**空数组**。
 *
 * ⚠ 调用方若需要区分「失败」与「成功」，必须改用 `extractEmbeddingResult`。
 *   本函数保留是因为 `voiceprints-store.enrollFromAudio` 用的是它的返回值
 *   当作要**落库**的向量——空数组会被 `isUsableEmbedding` 挡在库外，
 *   不会污染声纹库。
 */
export async function extractEmbedding(blob: Blob): Promise<Float32Array> {
  const r = await extractEmbeddingResult(blob)
  return r.ok ? r.embedding : new Float32Array(0)
}

/** Web 兜底的解码：OfflineAudioContext → 单声道 PCM。 */
export async function decodeToPcm(blob: Blob): Promise<Float32Array> {
  const arrayBuffer = await blob.arrayBuffer()
  const ctx = new OfflineAudioContext(1, 16000, 16000)
  const audioBuffer = await ctx.decodeAudioData(arrayBuffer.slice(0))
  return audioBuffer.getChannelData(0)
}

export {
  blobToEmbedding,
  cosineSimilarity,
  embeddingToBlob,
  isComparable,
  isUsableEmbedding,
  logMelEmbedding,
}
