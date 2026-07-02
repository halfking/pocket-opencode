/**
 * vector.ts — 🦞 龙虾记忆检索：本地向量索引与语义搜索
 *
 * MVP 策略（纯 JS 暴力余弦）：
 *   调研已证实 10,000 条 × 1536 维的纯 JS 点积只需 ~37ms，对个人笔记
 *   场景完全够用（见 AUDIT_FIX_REPORT 引用的基准数据）。无需 sqlite-vec
 *   或 chromem-go。当数据量增长到 ~5 万条时，再切换到 sqlite-vec 原生
 *   索引或 chromem-go（Phase B）。
 *
 * 向量以 Float32Array 序列化为 BLOB 存 local_note_vectors 表。查询时
 * 一次性 load 全部向量到内存做点积——10k 条 × 1536 维 × 4B ≈ 61MB，
 * 移动端可接受。
 *
 * 数据流：
 *   笔记文本 → pocketd /api/embed → 嵌入 API → 向量回传 → 存本地
 *   查询时：query 文本 → pocketd /api/embed → 向量 → 本地点积 → TopK
 */
import { localDB } from './local-db'

const DIM = 1536 // OpenAI text-embedding-3-small 维度
// 内存索引上限：超过后新向量只存库不加载到内存（防 OOM）。
// 50000 条 × 1536 维 × 4B ≈ 300MB，移动端可接受的上限。
// 超过此规模应切换 sqlite-vec 原生索引。
const MAX_VECTORS = 50000

export interface VectorMatch {
  noteId: string
  score: number
}

/** 向量索引：内存中持有全部 note 向量用于快速检索。 */
class VectorIndex {
  private ids: string[] = []
  private matrix: Float32Array = new Float32Array(0)
  private loaded = false

  /** 从本地库加载全部向量到内存。App 启动后调用一次。 */
  async load(): Promise<void> {
    const rows = await localDB.query<{ note_id: string; embedding: ArrayBuffer }>(
      'SELECT note_id, embedding FROM local_note_vectors',
    )
    if (rows.length === 0) {
      this.ids = []
      this.matrix = new Float32Array(0)
      this.loaded = true
      return
    }
    this.ids = rows.map((r) => r.note_id)
    // 把每条 BLOB 拼成一个连续 Float32Array，便于 SIMD 友好的点积
    const buf = new Float32Array(rows.length * DIM)
    rows.forEach((r, i) => {
      const v = new Float32Array(r.embedding)
      buf.set(v.subarray(0, Math.min(v.length, DIM)), i * DIM)
    })
    this.matrix = buf
    this.loaded = true
  }

  /** 添加一条向量（笔记创建/更新后）。同时写库。 */
  async add(noteId: string, embedding: Float32Array, model: string): Promise<void> {
    // 内存上限保护：超过 MAX_VECTORS 时仍写库但不再加载到内存索引
    // （查询时降级为仅 FTS，提示用户切换 sqlite-vec 或清理旧笔记）
    const isInMemory = this.ids.indexOf(noteId) < 0 && this.ids.length < MAX_VECTORS

    // L2 归一化，之后点积即余弦相似度
    const normalized = normalize(embedding)
    const blob = new Uint8Array(normalized.buffer, normalized.byteOffset, normalized.byteLength)

    await localDB.run(
      `INSERT INTO local_note_vectors (note_id, embedding, dim, model, created_at)
       VALUES (?, ?, ?, ?, ?)
       ON CONFLICT(note_id) DO UPDATE SET embedding = EXCLUDED.embedding, dim = EXCLUDED.dim, model = EXCLUDED.model`,
      [noteId, blob.buffer, normalized.length, model, Date.now()],
    )

    if (!isInMemory) {
      if (this.ids.length >= MAX_VECTORS) {
        console.warn(`[vector] 已达内存索引上限 ${MAX_VECTORS}，新向量仅存库不加载到内存。考虑启用 sqlite-vec。`)
      }
      return
    }

    // 增量更新内存索引
    this.ids.push(noteId)
    const newSize = (this.ids.length) * DIM
    const newMatrix = new Float32Array(newSize)
    newMatrix.set(this.matrix)
    newMatrix.set(normalized, newSize - DIM)
    this.matrix = newMatrix
  }

  /** 删除一条向量（笔记删除时）。 */
  async remove(noteId: string): Promise<void> {
    await localDB.run('DELETE FROM local_note_vectors WHERE note_id = ?', [noteId])
    const idx = this.ids.indexOf(noteId)
    if (idx >= 0) {
      // 重建内存索引（删除不频繁，全量重建可接受）
      await this.load()
    }
  }

  /**
   * 语义搜索：返回与 query 向量最相似的 TopK 笔记 ID + 分数。
   * queryVector 必须已 L2 归一化（normalize 处理）。
   */
  search(query: Float32Array, topK = 10): VectorMatch[] {
    if (!this.loaded || this.ids.length === 0) return []

    const q = normalize(query)
    const n = this.ids.length
    const k = Math.min(topK, n)

    // 暴力点积（归一化后 = 余弦相似度）。n 条 × DIM 维。
    // 使用 top-k 选择避免全量排序：O(n*k) vs O(n*log n)
    const topK_results: VectorMatch[] = []

    for (let i = 0; i < n; i++) {
      let dot = 0
      const base = i * DIM
      for (let d = 0; d < DIM; d++) {
        dot += this.matrix[base + d] * q[d]
      }

      // 插入排序维护 top-k：只在分数足够高时插入
      if (topK_results.length < k || dot > topK_results[topK_results.length - 1].score) {
        const match: VectorMatch = { noteId: this.ids[i], score: dot }
        let insertPos = topK_results.length
        // 找到插入位置（降序）
        for (let j = topK_results.length - 1; j >= 0; j--) {
          if (dot <= topK_results[j].score) break
          insertPos = j
        }
        topK_results.splice(insertPos, 0, match)
        if (topK_results.length > k) topK_results.pop()
      }
    }

    return topK_results
  }

  /** 当前索引的向量数量。 */
  size(): number {
    return this.ids.length
  }
}

/** L2 归一化，使点积等于余弦相似度。 */
function normalize(v: Float32Array): Float32Array {
  let norm = 0
  for (let i = 0; i < v.length; i++) norm += v[i] * v[i]
  norm = Math.sqrt(norm)
  if (norm === 0) return v
  const out = new Float32Array(v.length)
  for (let i = 0; i < v.length; i++) out[i] = v[i] / norm
  return out
}

/** 向量索引单例，全 App 共享。 */
export const vectorIndex = new VectorIndex()

/**
 * 把 ArrayBuffer（从 SQLite BLOB 读出）转 Float32Array。
 * 处理字节序：SQLite BLOB 按 native（小端）存，TypedArray 直接读即可。
 */
export function blobToFloat32(buf: ArrayBuffer): Float32Array {
  return new Float32Array(buf)
}
