/**
 * vector-index.ts — 向量索引的**可测核心**（§881）。
 *
 * ⚠ 为什么不放在 `vector.ts` 里直接测：`vector.ts` 需要 `localDbAsSql(localDB)`，
 *   那条 import 链在 node 里跑不起来（`local-db` → `sqlite-web-init` 那一族
 *   是**无扩展名** import，简报第 94 条）。
 *   ⇒ 与 §879 的 `notes-fts-backfill` 同一手法：可测的部分放进不依赖单例的模块，
 *   单例那一侧只留一层薄包装。
 *
 * ⚠ 本模块**不得** import `localDB` / 任何浏览器 API —— db 由调用方注入。
 */

import type { SqlRow } from './sqlDb.ts'

export const DIM = 1536 // OpenAI text-embedding-3-small 维度（§881 导出：判据不该硬编码这个数）

// 内存索引上限：超过后新向量只存库不加载到内存（防 OOM）。
// 50000 条 × 1536 维 × 4B ≈ 300MB，移动端可接受的上限。
// 超过此规模应切换 sqlite-vec 原生索引。
const MAX_VECTORS = 50000

export interface VectorMatch {
  noteId: string
  score: number
}

/**
 * 索引依赖的最小 DB 端口。
 * ⚠ 不用现成的 `SqlDb` 接口 —— 它的 `all` **不带泛型**，行形状在这里显式断言一次。
 */
export interface VectorDb {
  run(sql: string, params?: unknown[]): Promise<number>
  all(sql: string, params?: unknown[]): Promise<SqlRow[]>
}

/** 向量索引：内存中持有全部 note 向量用于快速检索。 */
export class VectorIndex {
  private readonly db: VectorDb
  private ids: string[] = []
  private matrix: Float32Array = new Float32Array(0)
  /**
   * §894/D64：`matrix` 能装多少条（`matrix.length / DIM`）。
   * ⚠ 必须与 `ids.length` **分开记**：几何增长意味着分配量 ≥ 实际条数，
   *   两者相等会让 add 每次都重新分配。
   * ⚠ load() 会把它重置成实际条数（load 是「按库里有多少装多少」的权威点）。
   */
  private capacity = 0
  private loaded = false

  // ⚠ 不能写成参数属性 `constructor(private readonly db: VectorDb = …)`：
  //   `node --experimental-strip-types` **不支持 parameter properties**，
  //   加载直接报 ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX ⇒ 判据跑不起来。
  //   与「裸 try 块」是同一族工具链限制。
  constructor(db: VectorDb) {
    this.db = db
  }

  /** 从本地库加载全部向量到内存。App 启动后调用一次。 */
  async load(): Promise<void> {
    const rows = (await this.db.all(
      'SELECT note_id, embedding FROM local_note_vectors',
    )) as unknown as { note_id: string; embedding: ArrayBuffer }[]
    if (rows.length === 0) {
      this.ids = []
      this.matrix = new Float32Array(0)
      this.capacity = 0
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
    this.capacity = rows.length
    this.loaded = true
  }

  /** 添加一条向量（笔记创建/更新后）。同时写库。 */
  async add(noteId: string, embedding: Float32Array, model: string): Promise<void> {
    // L2 归一化，之后点积即余弦相似度
    const normalized = normalize(embedding)
    const blob = new Uint8Array(normalized.buffer, normalized.byteOffset, normalized.byteLength)

    await this.db.run(
      `INSERT INTO local_note_vectors (note_id, embedding, dim, model, created_at)
       VALUES (?, ?, ?, ?, ?)
       ON CONFLICT(note_id) DO UPDATE SET embedding = EXCLUDED.embedding, dim = EXCLUDED.dim, model = EXCLUDED.model`,
      [noteId, blob.buffer, normalized.length, model, Date.now()],
    )

    // ⚠⚠§881：这条笔记**已经在内存索引里**时，必须**就地覆盖**那一行向量。
    //   修之前的形状是 `isInMemory = !已存在 && 未超上限` ⇒ 更新一条已有笔记时
    //   该值为 false ⇒ 写完库就 return ⇒ **内存矩阵永远留着旧向量**。
    //   而 `notes-persist.ts` 的更新路径（`patch.content !== undefined`）正是调 `add`
    //   ⇒ 编辑一条笔记后，语义检索继续用**改动前的内容**作答，
    //     库里的新向量要等到下次 `load()`（App 启动 / `remove()`）才生效。
    const existing = this.ids.indexOf(noteId)
    if (existing >= 0) {
      this.matrix.set(normalized.subarray(0, DIM), existing * DIM)
      return
    }

    // 内存上限保护：超过 MAX_VECTORS 时仍写库但不再加载到内存索引。
    // ⚠ 后果：此后新向量**永远不进内存索引** ⇒ `search()` 只在旧的那批里找
    //   （沉默的「搜不全」，登记在简报第 101 条），而注释写的
    //   「查询时降级为仅 FTS」在 `search()` 里**并没有实现**。
    if (this.ids.length >= MAX_VECTORS) {
      console.warn(`[vector] 已达内存索引上限 ${MAX_VECTORS}，新向量仅存库不加载到内存。考虑启用 sqlite-vec。`)
      return
    }

    // ⚠⚠ §894/D64：容量与实际长度**分离**，容量按几何级增长。
    //
    //   原实现每次 add 都 `new Float32Array(本条数 × DIM)` 并整段拷一遍旧的
    //   ⇒ 单次 add 的成本与**当前条数成正比**，而且每次都丢弃一块同等大小的旧缓冲。
    //   实测（Node 22，预热后测边际成本，三轮取平均）：
    //     n=200 → 0.076ms    n=800  → 0.277ms
    //     n=2000→ 1.368ms    n=4000 → 2.969ms      ⇒ 明确线性于 n
    //   ⇒ 保存一条笔记的成本随库里笔记数增长而线性变慢，且**每次保存都分配一整块索引**
    //   （到 50000 条上限时是每次保存分配/丢弃约 293MB），主线程 GC 压力极大。
    //
    //   ⚠ 容量上限必须**钉在 MAX_VECTORS**，否则倍增会让峰值内存比原来多一倍
    //   （50000 × 1536 × 4B ≈ 293MB 的两倍在移动端不可接受）。
    this.ids.push(noteId)
    if (this.ids.length > this.capacity) this.grow()
    this.matrix.set(normalized.subarray(0, DIM), (this.ids.length - 1) * DIM)
  }

  /** 容量翻倍并搬一次（摊还后每次 add 的拷贝是常数级）。 */
  private grow(): void {
    const next = Math.min(MAX_VECTORS, Math.max(64, this.capacity * 2))
    if (next <= this.capacity) return
    const m = new Float32Array(next * DIM)
    m.set(this.matrix)
    this.matrix = m
    this.capacity = next
  }

  /** 删除一条向量（笔记删除时）。 */
  async remove(noteId: string): Promise<void> {
    await this.db.run('DELETE FROM local_note_vectors WHERE note_id = ?', [noteId])
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

    // 防御：topK <= 0 时直接返回空数组，避免后续访问
    // topK_results[topK_results.length - 1].score（即 undefined.score）
    // 抛 TypeError。
    if (k <= 0) return []

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
export function normalize(v: Float32Array): Float32Array {
  let norm = 0
  for (let i = 0; i < v.length; i++) norm += v[i] * v[i]
  norm = Math.sqrt(norm)
  if (norm === 0) return v
  const out = new Float32Array(v.length)
  for (let i = 0; i < v.length; i++) out[i] = v[i] / norm
  return out
}

/**
 * 把 ArrayBuffer（从 SQLite BLOB 读出）转 Float32Array。
 * 处理字节序：SQLite BLOB 按 native（小端）存，TypedArray 直接读即可。
 */
export function blobToFloat32(buf: ArrayBuffer): Float32Array {
  return new Float32Array(buf)
}
