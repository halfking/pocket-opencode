/**
 * speaker-embedding.ts — 声纹 embedding 提取
 *
 * 原生：cap-sherpa ECAPA-TDNN（Android）
 * Web 兜底：频谱特征向量（32 维），用于增量聚类
 */
import { sherpa } from './sherpa'

const EMBED_DIM = 32

/** 从音频 blob 提取 embedding，优先原生 ECAPA */
export async function extractEmbedding(blob: Blob): Promise<Float32Array> {
  try {
    const path = URL.createObjectURL(blob)
    try {
      const result = await sherpa.extractEmbedding(path)
      URL.revokeObjectURL(path)
      return result.embedding
    } catch {
      URL.revokeObjectURL(path)
    }
  } catch { /* fall through */ }
  return extractWebEmbedding(blob)
}

/** Web 兜底：OfflineAudioContext 解码 + 频谱 bin 均值 */
export async function extractWebEmbedding(blob: Blob): Promise<Float32Array> {
  const arrayBuffer = await blob.arrayBuffer()
  const ctx = new OfflineAudioContext(1, 16000, 16000)
  try {
    const audioBuffer = await ctx.decodeAudioData(arrayBuffer.slice(0))
    return spectrumEmbedding(audioBuffer)
  } catch {
    // webm 可能无法直接 decode，用随机稳定 hash 兜底（同 blob 同向量）
    return hashEmbedding(await blob.arrayBuffer())
  }
}

function spectrumEmbedding(buffer: AudioBuffer): Float32Array {
  const data = buffer.getChannelData(0)
  const bins = new Float32Array(EMBED_DIM)
  const chunk = Math.max(1, Math.floor(data.length / EMBED_DIM))
  for (let b = 0; b < EMBED_DIM; b++) {
    let sum = 0
    const start = b * chunk
    const end = Math.min(start + chunk, data.length)
    for (let i = start; i < end; i++) sum += Math.abs(data[i])
    bins[b] = sum / (end - start)
  }
  return normalizeL2(bins)
}

function hashEmbedding(buf: ArrayBuffer): Float32Array {
  const view = new Uint8Array(buf)
  const bins = new Float32Array(EMBED_DIM)
  for (let i = 0; i < view.length; i++) {
    bins[i % EMBED_DIM] += view[i] / 255
  }
  return normalizeL2(bins)
}

function normalizeL2(v: Float32Array): Float32Array {
  let norm = 0
  for (let i = 0; i < v.length; i++) norm += v[i] * v[i]
  norm = Math.sqrt(norm) || 1
  for (let i = 0; i < v.length; i++) v[i] /= norm
  return v
}

export function cosineSimilarity(a: Float32Array, b: Float32Array): number {
  const len = Math.min(a.length, b.length)
  let dot = 0
  for (let i = 0; i < len; i++) dot += a[i] * b[i]
  return dot
}

export function embeddingToBlob(v: Float32Array): Uint8Array {
  return new Uint8Array(v.buffer, v.byteOffset, v.byteLength)
}

export function blobToEmbedding(buf: Uint8Array | ArrayBuffer): Float32Array {
  const arr = buf instanceof Uint8Array ? buf : new Uint8Array(buf)
  return new Float32Array(arr.buffer, arr.byteOffset, arr.byteLength / 4)
}
