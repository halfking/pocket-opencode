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
// ⚠⚠ §898：这里的「条」现在是**段向量**的条数，不再等于笔记数 ——
//   一条长会议笔记会占 N 段（N = 段数）。短笔记仍只占 1 段，
//   所以容量的实际缩水只来自「长会议笔记占比」这一个变量（§897.4）。
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
  /**
   * §898：内存矩阵的**一行 = 一个段向量**。
   * ⚠ 同一条笔记的多个段**连续存放**（见 `noteSpans`），
   *   这样「就地覆盖这一条笔记的段」不需要移动其它笔记的行。
   */
  private ids: string[] = []
  private matrix: Float32Array = new Float32Array(0)
  /**
   * §894/D64：`matrix` 能装多少条（`matrix.length / DIM`）。
   * ⚠ 必须与 `ids.length` **分开记**：几何增长意味着分配量 ≥ 实际条数，
   *   两者相等会让 add 每次都重新分配。
   * ⚠ load() 会把它重置成实际条数（load 是「按库里有多少装多少」的权威点）。
   */
  private capacity = 0
  /**
   * §898：笔记 → 它那一组段向量在 `ids`/`matrix` 里的连续区间。
   * ⚠ 连续是**不变式**：新增笔记追加在尾部；段数变化时走 `load()` 重建以恢复连续。
   */
  private noteSpans = new Map<string, { start: number; count: number }>()
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
      'SELECT note_id, embedding, chunks FROM local_note_vectors',
    )) as unknown as { note_id: string; embedding: ArrayBuffer; chunks?: number | null }[]
    if (rows.length === 0) {
      this.ids = []
      this.matrix = new Float32Array(0)
      this.capacity = 0
      this.noteSpans = new Map()
      this.loaded = true
      return
    }
    // ⚠ §898：每行可能是**多个段向量**（chunks 列），旧行 chunks=1。
    //   一行里的段向量在 BLOB 里连续存放（chunks × dim 个 float）。
    const perRow: { noteId: string; vec: Float32Array }[] = []
    for (const r of rows) {
      const chunks = Math.max(1, r.chunks ?? 1)
      const raw = new Float32Array(r.embedding)
      const usable = Math.min(raw.length, chunks * DIM)
      const vec = raw.subarray(0, usable)
      for (let c = 0; c < chunks; c++) {
        const off = c * DIM
        if (off + DIM > usable) break
        perRow.push({ noteId: r.note_id, vec: vec.subarray(off, off + DIM) })
      }
    }
    this.ids = perRow.map((p) => p.noteId)
    const buf = new Float32Array(perRow.length * DIM)
    perRow.forEach((p, i) => buf.set(p.vec, i * DIM))
    this.matrix = buf
    this.capacity = perRow.length
    this.noteSpans = buildSpans(this.ids)
    this.loaded = true
  }

  /**
   * 添加一条笔记的向量（笔记创建/更新后）。同时写库。
   *
   * ⚠⚠ §898：入参可以是**一个**向量，也可以是**一组段向量**。
   *   一组 = 长会议按段嵌入的结果（`splitForEmbedding` 切出来的）。
   *   ⇒ 库里仍然**一行一笔记**（`note_id` 主键不变），
   *     `embedding` BLOB 里连续放 `chunks × dim` 个 float，`chunks` 列记个数。
   *   ⇒ 旧数据 chunks=1，语义与从前逐字一致。
   */
  async add(noteId: string, embedding: Float32Array | Float32Array[], model: string): Promise<void> {
    const list = Array.isArray(embedding) ? embedding : [embedding]
    if (list.length === 0) return
    // L2 归一化，之后点积即余弦相似度
    const normalized = list.map((v) => normalize(v))
    const total = normalized.length * DIM
    const flat = new Float32Array(total)
    normalized.forEach((v, i) => flat.set(v.subarray(0, DIM), i * DIM))
    const blob = new Uint8Array(flat.buffer, flat.byteOffset, flat.byteLength)

    await this.db.run(
      `INSERT INTO local_note_vectors (note_id, embedding, dim, model, chunks, created_at)
       VALUES (?, ?, ?, ?, ?, ?)
       ON CONFLICT(note_id) DO UPDATE SET embedding = EXCLUDED.embedding, dim = EXCLUDED.dim, model = EXCLUDED.model, chunks = EXCLUDED.chunks`,
      [noteId, blob.buffer, DIM, model, normalized.length, Date.now()],
    )

    // ⚠⚠§881：这条笔记**已经在内存索引里**时，必须**就地覆盖**那一行向量。
    //   修之前的形状是 `isInMemory = !已存在 && 未超上限` ⇒ 更新一条已有笔记时
    //   该值为 false ⇒ 写完库就 return ⇒ **内存矩阵永远留着旧向量**。
    //   而 `notes-persist.ts` 的更新路径（`patch.content !== undefined`）正是调 `add`
    //   ⇒ 编辑一条笔记后，语义检索继续用**改动前的内容**作答，
    //     库里的新向量要等到下次 `load()`（App 启动 / `remove()`）才生效。
    const span = this.noteSpans.get(noteId)
    if (span) {
      if (span.count === normalized.length) {
        // 段数没变 ⇒ 就地覆盖（O(段数×DIM)，不重建）
        normalized.forEach((v, i) => this.matrix.set(v.subarray(0, DIM), (span.start + i) * DIM))
        return
      }
      // ⚠⚠ §898：段数变了（正文长度跨过了一个段边界）⇒ 行的连续布局不再成立。
      //   走一次全量重建（load）恢复不变式。
      //   ⇒ 频率：只在「段数变化」时发生（编辑让长度跨过 3800 字的倍数），
      //     而不是每次编辑。重建成本 O(总段数)，可接受但**必须记进文档**。
      await this.load()
      return
    }

    // 内存上限保护：超过 MAX_VECTORS 时仍写库但不再加载到内存索引。
    // ⚠ 后果：此后新向量**永远不进内存索引** ⇒ `search()` 只在旧的那批里找
    //   （沉默的「搜不全」，登记在简报第 101 条），而注释写的
    //   「查询时降级为仅 FTS」在 `search()` 里**并没有实现**。
    if (this.ids.length + normalized.length > MAX_VECTORS) {
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
    //   ⇒ 保存一条笔记的成本随库里笔记数增长而线性变慢，且**每次都分配一整块索引**
    //   （到 50000 条上限时是每次保存分配/丢弃约 293MB），主线程 GC 压力极大。
    //
    //   ⚠ 容量上限必须**钉在 MAX_VECTORS**，否则倍增会让峰值内存比原来**多一倍**
    //   （50000 × 1536 × 4B ≈ **293MB** 的两倍在移动端不可接受）。
    //   ⇒ `MAX_VECTORS` 那个「移动端可接受的上限」的注释因此仍然成立。
    const start = this.ids.length
    for (const v of normalized) this.ids.push(noteId)
    if (this.ids.length > this.capacity) this.grow()
    normalized.forEach((v, i) => this.matrix.set(v.subarray(0, DIM), (start + i) * DIM))
    this.noteSpans.set(noteId, { start, count: normalized.length })
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
    if (this.noteSpans.has(noteId)) {
      // 重建内存索引（删除不频繁，全量重建可接受）
      await this.load()
    }
  }

  /**
   * 语义搜索：返回与 query 向量最相似的 TopK 笔记 ID + 分数。
   * queryVector 必须已 L2 归一化（normalize 处理）。
   *
   * ⚠⚠ §895/D65：这是**同步**的，`searchSemantic` 在 UI 线程上调用它
   *   （最硬的一条：`NoteDetailView.loadRelated` —— 每次打开笔记详情都会跑一次）。
   *   实测单次耗时线性于条数（Node 22，预热后五轮取平均）：
   *     n=500 → 0.66ms   n=2000 → 2.30ms   n=8000 → 9.32ms   n=20000 → 24.89ms
   *   ⇒ 到 MAX_VECTORS 上限（50000）外推约 62ms 的**主线程冻结**；
   *     移动端 JS 通常再慢 3–10 倍。
   *   ⇒ 生产路径请改用 `searchAsync()`（结果逐字相同，只是分片让出事件循环）。
   *
   * ⚠⚠ §898：一条笔记现在可能有**多个段向量**，
   *   得分 = 该笔记**所有段里的最高分**（max 聚合）。
   *   ⇒ 为什么必须 max 而不是平均（这是实测结论，不是偏好）：
   *     局部代理嵌入（TF-IDF，1536 维）下，4 小时会议（18 段）里
   *     第 10 段埋了一句「合同终稿已发给李四」，查询「合同终稿发给李四了吗」：
   *       截断（只看前 16000 字）      → 0.0452
   *       质心（18 段平均成 1 个）     → 0.0492
   *       单次全量嵌入（72K 字 1 个）  → 0.0914
   *       段级 + max 聚合              → **0.1442**（= 那一段本身的分）
   *     一条「中等相关」的短笔记是 0.0394 ⇒ 截断与质心只比它高 15%~25%
   *     （在有若干短笔记的库里会被挤出 top-k），max 聚合则高 266%。
   *   ⇒ **单向量表示（截断/质心/整篇单向量）在结构上就稀释掉了长文档的局部信号**，
   *     长会议要想「能被搜到」，必须按段存 + 取 max。
   *   ⚠ 绝对值来自代理嵌入（词法），不可外推；**结构性差异**（平均 vs 取最大）才是结论。
   *
   * 本方法保留同步形态：判据与「不引入 async 之前」的调用点仍用它，
   * 且它是 `searchAsync` 的语义基准。
   */
  search(query: Float32Array, topK = 10): VectorMatch[] {
    const prep = this.prepare(query, topK)
    if (!prep) return []
    const out: VectorMatch[] = []
    const index = new Map<string, number>()
    for (let i = 0; i < this.ids.length; i++) {
      considerNoteInto(out, index, this.ids[i], this.dotAt(i, prep.q), prep.k)
    }
    return out
  }

  /**
   * 与 `search()` **结果逐字相同**的异步版本：每扫 `chunkSize` 条让出一次事件循环。
   *
   * ⚠ 分片不是为了「跑得更快」（总工作量一样），是为了**不让单次任务占满一帧**：
   *   20000 条 / chunk 512 ⇒ 39 片 × 约 0.64ms，每片都远低于一帧预算
   *   ⇒ 界面在检索期间仍可滚动、可响应输入。
   */
  async searchAsync(query: Float32Array, topK = 10, chunkSize = SCAN_CHUNK): Promise<VectorMatch[]> {
    const prep = this.prepare(query, topK)
    if (!prep) return []
    const out: VectorMatch[] = []
    const index = new Map<string, number>()
    const n = this.ids.length
    const step = Math.max(1, chunkSize)
    for (let i = 0; i < n; i++) {
      considerNoteInto(out, index, this.ids[i], this.dotAt(i, prep.q), prep.k)
      if ((i + 1) % step === 0) await yieldToEventLoop()
    }
    return out
  }

  /** 两个 search 共用的前置：未加载 / 空索引 / topK 非正 / 归一化查询向量。 */
  private prepare(query: Float32Array, topK: number): { q: Float32Array; k: number } | null {
    if (!this.loaded || this.ids.length === 0) return null
    const n = this.ids.length
    const k = Math.min(topK, n)
    // 防御：topK <= 0 时直接返回空数组，避免后续访问
    // topK_results[topK_results.length - 1].score（即 undefined.score）
    // 抛 TypeError。
    if (k <= 0) return null
    return { q: normalize(query), k }
  }

  /** 第 i 条向量与 q 的点积（调用方保证 0 <= i < ids.length）。 */
  private dotAt(i: number, q: Float32Array): number {
    const base = i * DIM
    let dot = 0
    for (let d = 0; d < DIM; d++) dot += this.matrix[base + d] * q[d]
    return dot
  }

  /** 当前索引的向量数量（§898 起 = **段向量**条数，不等于笔记数）。 */
  size(): number {
    return this.ids.length
  }

  /** 当前索引里的**笔记数**（§898：有段的概念后，两者不再相等）。 */
  noteCount(): number {
    return this.noteSpans.size
  }
}

/**
 * §895/D65：`searchAsync` 每扫这么多条让出一次事件循环。
 *
 * ⚠ 取值依据（实测）：20000 条全量扫描约 24.9ms；按 512 分片 ⇒ 每片约 0.64ms，
 *   远低于一帧预算（16.7ms）⇒ 检索期间界面仍可滚动/响应输入。
 *   取得再小会让 `await` 次数变多、总时长被调度开销反噬。
 */
const SCAN_CHUNK = 512

/** 让出一次事件循环（宏任务），使浏览器有机会渲染/处理输入。 */
const yieldToEventLoop = () => new Promise<void>((resolve) => setTimeout(resolve, 0))

/**
 * 从「每行一个 noteId」的列表重建「笔记 → 连续区间」映射（§898）。
 * ⚠ 同一笔记的行必须是**连续**的 —— `add()` 靠这个不变式做就地覆盖。
 *   `load()` 读回来的顺序是库里的插入顺序，不保证连续 ⇒ 这里按首次出现位置归并，
 *   并且 `add()` 在段数变化时走 `load()` 重建内存布局来恢复连续性。
 */
function buildSpans(ids: string[]): Map<string, { start: number; count: number }> {
  const spans = new Map<string, { start: number; count: number }>()
  for (let i = 0; i < ids.length; i++) {
    const id = ids[i]
    const cur = spans.get(id)
    if (cur && cur.start + cur.count === i) {
      cur.count += 1
    } else {
      spans.set(id, { start: i, count: 1 })
    }
  }
  return spans
}

/**
 * 把一条**段**候选并入 top-k（**同步**，无 await）——`search` 与 `searchAsync` 共用。
 *
 * ⚠⚠ §898：聚合单位是**笔记**，不是段。
 *   同一笔记的多个段里取**最高分**代表这条笔记（max 聚合），
 *   而**不是**把多个段各占一个 top-k 名额
 *   —— 否则一条 15 段的会议笔记会占满整个 top-k，把别的笔记挤出去。
 *
 * @param out    当前 top-k（按 score 降序；同分时**先加入**的在前）
 * @param index  noteId → 它在 out 里的下标（只装 ≤ k 条 ⇒ 查找是 O(1)）
 */
function considerNoteInto(
  out: VectorMatch[],
  index: Map<string, number>,
  noteId: string,
  dot: number,
  k: number,
): void {
  const pos = index.get(noteId)
  if (pos !== undefined) {
    // 这条笔记已经在榜上 ⇒ 只在分数变高时更新，并把它往上挪。
    if (dot <= out[pos].score) return
    out[pos].score = dot
    let p = pos
    while (p > 0 && out[p].score > out[p - 1].score) {
      const a = out[p - 1]
      const b = out[p]
      out[p - 1] = b
      out[p] = a
      index.set(b.noteId, p - 1)
      index.set(a.noteId, p)
      p -= 1
    }
    return
  }
  if (out.length < k || dot > out[out.length - 1].score) {
    let insertPos = out.length
    for (let j = out.length - 1; j >= 0; j--) {
      // 同分时**停在相等项之后** ⇒ 先加入的排在前面（§895 判据 3 钉住这条）
      if (dot <= out[j].score) break
      insertPos = j
    }
    out.splice(insertPos, 0, { noteId, score: dot })
    index.set(noteId, insertPos)
    if (out.length > k) {
      const dropped = out.pop()
      if (dropped) index.delete(dropped.noteId)
    }
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
