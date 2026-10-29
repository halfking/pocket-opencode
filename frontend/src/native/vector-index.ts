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

/**
 * ⚠⚠ §904/D73：这一行能不能按 `DIM` 解读？不能 ⇒ 返回原因字符串（供 warn 用），能 ⇒ null。
 *
 * ## 为什么需要它（这是本节修掉的真缺陷）
 *
 * 内存矩阵的每行宽就是 `DIM`，所以**库里的行只有「自己记的 dim 就是 DIM、
 * 且 BLOB 恰好是 DIM 的整数倍」时才能被正确解读**。而修之前，三条路径
 * （`load()` / `existingChunks()` / `add()`）**全都拿模块常量 `DIM` 无条件解读**：
 *
 * | 情形 | 修之前 | 后果 |
 * |---|---|---|
 * | 网关换成更高维的模型（3072） | `add()` 把它**截断成前一半**，`dim` 列照旧写 1536 | 索引里是**半条向量**，检索自信地给出错结果 |
 * | 网关换成更低维的模型（1024） | `add()` 把它**补零到 1536**，`dim` 列照旧写 1536 | `dim` 列**记录的不是事实** |
 * | 库里已是别的维度的行（换过设备/版本/模型，或被写坏） | `load()` 切不出完整一段 | 该笔记**静默消失**，零诊断 |
 *
 * ⚠ `dim` 列此前**只写不读**（`load()` 的 SELECT 里根本没有它），
 *   所以它写的那个常量从来没人核对过。
 *
 * ⚠ 为什么不「自动适配别的维度」：内存矩阵是 DIM 宽的，混装不同维度的向量
 *   在数学上就没有意义（点积不可比）。**正确的做法是把它们排除并出声**，
 *   而不是硬凑成 DIM —— 凑出来的向量会让搜索**自信地答错**，比搜不到更糟。
 */
function unusableRow(floatCount: number, dim: number | null | undefined): string | null {
  if (floatCount === 0) return `BLOB 为空`
  if ((dim ?? DIM) !== DIM) return `dim=${dim} ≠ ${DIM}`
  if (floatCount % DIM !== 0) return `BLOB 是 ${floatCount} 个 float，不是 ${DIM} 的整数倍`
  return null
}

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

/** A business operation can retain its original DB lease across embedding awaits. */
export interface VectorOperation {
  readonly db: { run(sql: string, params?: unknown[]): Promise<unknown>; query<T = Record<string, unknown>>(sql: string, params?: unknown[]): Promise<T[]> }
  assertCurrent(): void
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
  /** §905/D74：当前在途的 `load()`。写路径必须等它落地再改内存（见 `settleLoad()`）。 */
  private loading: Promise<void> | null = null
  /** §906/D75：内存索引的段数上限（生产 = `MAX_VECTORS`；判据可注入小值）。 */
  private readonly maxVectors: number

  // ⚠ 不能写成参数属性 `constructor(private readonly db: VectorDb = …)`：
  //   `node --experimental-strip-types` **不支持 parameter properties**，
  //   加载直接报 ERR_UNSUPPORTED_TYPESCRIPT_SYNTAX ⇒ 判据跑不起来。
  //   与「裸 try 块」是同一族工具链限制。
  // ⚠⚠ §906/D75：`maxVectors` 可注入，**生产不传**（默认就是 MAX_VECTORS）。
  //   为什么要有这个口子：`MAX_VECTORS = 50000`，一条段向量 6KB ⇒ 真库要 300MB
  //   才跑得到边界，**判据根本跑不动** ⇒ 这条「内存上限必须在读取路径上也生效」
  //   的不变式就会一直没有判据。
  //   ⇒ 与 `DIM` 导出同一个理由（§881：「判据不该硬编码这个数」）。
  constructor(db: VectorDb, maxVectors: number = MAX_VECTORS) {
    this.db = db
    this.maxVectors = maxVectors
  }

  /** 从本地库加载全部向量到内存。App 启动后调用一次。 */
  async load(operation?: VectorOperation): Promise<void> {
    operation?.assertCurrent()
    const p = this.loadFromDb(operation)
    this.loading = p
    try {
      await p
    } finally {
      if (this.loading === p) this.loading = null
    }
  }

  /**
   * ⚠⚠ §905/D74：在途的 `load()` 落地之前，写路径**不许改内存索引**。
   *
   * 缺陷：`load()` 是「读库 → **整体重建** `ids`/`matrix`/`noteSpans`」，
   * 而它读的快照是**调用时**取的、结果晚到。若这期间 `add()` 落地，
   * 新向量会被随后到达的 `load()` **整体覆盖掉** ——
   * 实测（Node 22，生产 `VectorIndex`）：
   *     load() 在途 → add('D') 写库并进内存（size 2、D 搜得到）
   *     → load() 落地，用**旧快照**重建 ⇒ size 掉回 1、**D 搜不到了**
   *   而 D **在库里** ⇒ 这条笔记要等到**下次冷启动**才能被搜到，全程零诊断。
   *
   * ⇒ 修法不是「合并」（那要判断新旧），而是**串行化**：
   *   写路径在动内存之前先 `await` 在途的 `load()`，
   *   于是每次内存变更前状态都是「已装载且无在途重建」。
   *   ⚠ 注意 `add()` 里的 `db.run()` **故意留在 await 之前** ——
   *     落库不能被一次慢 `load()` 挡住（那会让保存笔记变慢）；
   *     被挡的只有内存变更，而内存变更本来就该等重建结束。
   */
  private async settleLoad(): Promise<void> {
    while (this.loading) await this.loading
  }

  private async loadFromDb(operation?: VectorOperation): Promise<void> {
    const all = operation ? operation.db.query.bind(operation.db) : this.db.all.bind(this.db)
    const rows = (await all(
      'SELECT note_id, embedding, dim, chunks, chunk_hashes FROM local_note_vectors',
    )) as unknown as {
      note_id: string
      embedding: ArrayBuffer
      dim?: number | null
      chunks?: number | null
      chunk_hashes?: string | null
    }[]
    operation?.assertCurrent()
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
    // ⚠⚠ §904/D73：内存矩阵的**每行宽就是 DIM**，所以一行只有「恰好是 DIM 的整数倍、
    //   且自己记的 dim 就是 DIM」时才可能被正确解读。**此前这里用模块常量 DIM
    //   无条件解读每一行，`dim` 列根本没进 SELECT** ⇒ 非 DIM 维的行要么
    //   **静默消失**（dim 小：切不出完整一段，被 `break` 掉）、
    //   要么**静默取到错的片段**（dim 大：拿前 DIM 个 float 当一条向量）。
    //   两者都零诊断 ⇒ 那条笔记永久搜不到，或**自信地给出错误结果**。
    const perRow: { noteId: string; vec: Float32Array }[] = []
    const unusable: string[] = []
    let droppedSegments = 0
    for (const r of rows) {
      const raw = blobToFloat32(r.embedding)
      const why = unusableRow(raw.length, r.dim)
      if (why) {
        unusable.push(`${r.note_id}(${why})`)
        continue
      }
      const chunks = Math.max(1, r.chunks ?? 1)
      const usable = Math.min(raw.length, chunks * DIM)
      const vec = raw.subarray(0, usable)
      for (let c = 0; c < chunks; c++) {
        // ⚠⚠ §906/D75：这里**也必须**卡 `MAX_VECTORS`，否则「移动端可接受的上限」是假的。
        //   修之前上限只在 `add()` 里查，而 `add()` 的「段数变化 ⇒ load()」分支
        //   **在上限检查之前就 return 了** ⇒ 在容量已满时编辑一条长会议笔记，
        //   就会静默建出**超限**的矩阵。
        //   实测（Node 22 + 真 node:sqlite，库里 50001 行 = 上限 +1）：
        //     load() 用时 875ms、size()=50001、矩阵 **293MB**、**零 warning**
        //     ⇒ 「293MB 是移动端可接受的上限」这个论证当时**并不成立**。
        if (perRow.length >= this.maxVectors) { droppedSegments++; break }
        const off = c * DIM
        if (off + DIM > usable) break
        perRow.push({ noteId: r.note_id, vec: vec.subarray(off, off + DIM) })
      }
      if (droppedSegments > 0) break
    }
    if (droppedSegments > 0) {
      // ⚠ 同 §904「只出声、不阻断」：超限的段**留在库里**（数据没丢），
      //   但这一次不进内存索引 ⇒ 它们的笔记本次会话搜不到。
      //   ⚠ 保留的取舍是**按库里的行顺序取前 MAX_VECTORS 段**（不是按相关度，
      //   也不排除长会议）—— 这是有意为之的简单口径，不假装是 LRU。
      console.warn(
        `[vector] 内存索引上限 ${this.maxVectors} 段，库里更多 ⇒ 本次只载入前 ${this.maxVectors} 段，` +
        `其余 ${droppedSegments} 段留在库里但**本次会话搜不到**（数据没丢）。` +
        `考虑清理旧笔记或启用 sqlite-vec。`,
      )
    }
    // ⚠ §883 的「只出声、不阻断」：坏行**排除出索引**（不能让错的向量参与点积），
    //   但必须**汇总成一条 warn** —— 逐行 warn 会把真正的问题淹在日志里。
    if (unusable.length > 0) {
      console.warn(
        `[vector] ${unusable.length} 行向量无法解读，已排除出内存索引（这些笔记将搜不到）：` +
        `${unusable.slice(0, 5).join('、')}${unusable.length > 5 ? ` 等 ${unusable.length} 条` : ''}。` +
        `常见原因：换过 embedding 模型（维度不同）、或 BLOB 被写坏。`,
      )
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
   * 取某条笔记**已存**的段向量与它们的哈希（§899 增量重嵌用）。
   *
   * ⚠ 返回的顺序与 `chunks` 列一致；哈希与向量**一一对应**。
   * ⚠ 没有任何记录时返回 null（调用方据此「全部重嵌」）。
   * ⚠ `model` 也会返回：换了 embedding 模型时**不能复用**旧向量
   *   （不同模型的向量不在同一个空间里，混用会让相似度失去意义）。
   */
  async existingChunks(noteId: string, operation?: VectorOperation): Promise<{ hashes: string[]; vectors: Float32Array[]; model: string } | null> {
    operation?.assertCurrent()
    const all = operation ? operation.db.query.bind(operation.db) : this.db.all.bind(this.db)
    const rows = (await all(
      'SELECT embedding, dim, model, chunks, chunk_hashes FROM local_note_vectors WHERE note_id = ?',
      [noteId],
    )) as unknown as {
      embedding: ArrayBuffer
      dim: number
      model: string
      chunks?: number | null
      chunk_hashes?: string | null
    }[]
    operation?.assertCurrent()
    if (rows.length === 0) return null
    const r = rows[0]
    const raw = blobToFloat32(r.embedding)
    // ⚠⚠ §904/D73：维度对不上的行**绝不能拿来做「复用」** ——
    //   它要么切不出段（复用 0 条），要么切出**错位**的一段（复用错向量）。
    //   按本函数既有的「没有可复用的」形态返回 ⇒ 调用方全部重嵌。
    if (unusableRow(raw.length, r.dim)) {
      return { hashes: [], vectors: [], model: r.model }
    }
    const chunks = Math.max(1, r.chunks ?? 1)
    const vectors: Float32Array[] = []
    for (let c = 0; c < chunks; c++) {
      const off = c * DIM
      if (off + DIM > raw.length) break
      vectors.push(raw.slice(off, off + DIM))
    }
    const hashes = (r.chunk_hashes ?? '').split(',').filter(Boolean)
    // ⚠ 哈希与向量必须**等长**才能一一对应；不等长就当「没有可复用的」⇒ 全部重嵌。
    if (hashes.length !== vectors.length) {
      return { hashes: [], vectors: [], model: r.model }
    }
    return { hashes, vectors, model: r.model }
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
  async add(
    noteId: string,
    embedding: Float32Array | Float32Array[],
    model: string,
    hashes?: string[],
    operation?: VectorOperation,
  ): Promise<void> {
    operation?.assertCurrent()
    const list = Array.isArray(embedding) ? embedding : [embedding]
    if (list.length === 0) return
    // ⚠⚠ §904/D73：**拒绝**把维度不对的向量写进库。
    //   修之前的形状：`flat = new Float32Array(段数 × DIM)` + `set(v.subarray(0, DIM))`
    //   ⇒ 更高维的向量被**静默截断成前一半**、更低维的被**静默补零**，
    //     而 `dim` 列两种情况都写成常量 `DIM` ⇒ 这一列**记录的不是事实**。
    //   实测（Node 22 + 真 node:sqlite，production add()）：
    //     3072 维入参 ⇒ 库里 dim=1536、BLOB 6144 字节（应为 12288）
    //     1024 维入参 ⇒ 库里 dim=1536、BLOB 6144 字节（应为 4096）
    //   ⇒ 后果是索引里混着不同模型的向量，而 `search()` 会**自信地给出错结果**
    //     （比「搜不到」更糟：用户无法分辨哪个结果是错的）。
    //   按 §883「只出声、不阻断」：出声 + 不写库，**不**让保存笔记整条失败。
    const bad = list.find((v) => v.length !== DIM)
    if (bad) {
      console.warn(
        `[vector] 拒绝写入 ${model} 的向量：维度 ${bad.length} ≠ 索引维度 ${DIM}。` +
        `该笔记这次不会进语义索引（笔记本身已保存）。` +
        `若整个 workspace 都这样，说明网关的 embedding 模型换了维度 —— 需要重建索引。`,
      )
      return
    }
    // L2 归一化，之后点积即余弦相似度
    const normalized = list.map((v) => normalize(v))
    const total = normalized.length * DIM
    const flat = new Float32Array(total)
    normalized.forEach((v, i) => flat.set(v.subarray(0, DIM), i * DIM))
    // ⚠⚠ 审计修复（2026-10-29）：这里原来传的是 `blob.buffer`（ArrayBuffer）。
    //   Web 的 SQLite 驱动只接受 TypedArray 一类的**视图**，遇到 ArrayBuffer
    //   直接报 `Wrong API use : tried to bind a value of an unknown type
    //   ([object ArrayBuffer])` ⇒ Web 上语义索引**永远写不进去**。
    //   原生驱动两者都收，但统一传视图最安全：
    //   `new Uint8Array(flat.buffer)` 会复制一份（与 flat.byteOffset 无关），
    //   字节内容与原 BLOB **逐字节相同** ⇒ 旧数据不需要迁移。
    const blob = new Uint8Array(flat.buffer.slice(0))

    // ⚠ §899：哈希与向量**一一对应**；长度对不上就写空串（⇒ 下次一律重嵌，安全侧）。
    const hashText = hashes && hashes.length === normalized.length ? hashes.join(',') : ''
    await (operation?.db ?? this.db).run(
      `INSERT INTO local_note_vectors (note_id, embedding, dim, model, chunks, chunk_hashes, created_at)
       VALUES (?, ?, ?, ?, ?, ?, ?)
       ON CONFLICT(note_id) DO UPDATE SET embedding = EXCLUDED.embedding, dim = EXCLUDED.dim, model = EXCLUDED.model, chunks = EXCLUDED.chunks, chunk_hashes = EXCLUDED.chunk_hashes`,
      [noteId, blob, DIM, model, normalized.length, hashText, Date.now()],
    )

    // ⚠ §905：落库**不等**（上面的 db.run 已经完成了），但内存变更必须等
    //   在途的 load() 落地 —— 否则会被它的旧快照整体覆盖掉。
    await this.settleLoad()
    operation?.assertCurrent()

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
        this.markPopulated()
        return
      }
      // ⚠⚠ §898：段数变了（正文长度跨过了一个段边界）⇒ 行的连续布局不再成立。
      //   走一次全量重建（load）恢复不变式。
      //   ⇒ 频率：只在「段数变化」时发生（编辑让长度跨过 3800 字的倍数），
      //     而不是每次编辑。重建成本 O(总段数)，可接受但**必须记进文档**。
      await this.load(operation)
      return
    }

    // 内存上限保护：超过 MAX_VECTORS 时仍写库但不再加载到内存索引。
    // ⚠ 后果：此后新向量**永远不进内存索引** ⇒ `search()` 只在旧的那批里找
    //   （沉默的「搜不全」，登记在简报第 101 条），而注释写的
    //   「查询时降级为仅 FTS」在 `search()` 里**并没有实现**。
    if (this.ids.length + normalized.length > this.maxVectors) {
      console.warn(`[vector] 已达内存索引上限 ${this.maxVectors}，新向量仅存库不加载到内存。考虑启用 sqlite-vec。`)
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
    this.markPopulated()
  }

  /**
   * ⚠⚠ §903/D72：内存索引**只要真的被填过**，就算「已装载」。
   *
   * 修之前的形状：`loaded` 只在 `load()` 成功时置 true，而 `prepare()` 拿
   * `!this.loaded` 当闸 ⇒ 一旦 `load()` 抛错（比如 §902 那条被吞掉的 ALTER 失败
   * 让 `SELECT chunks` 报 `no such column`），**整场会话的语义检索就死了**：
   *   向量**已经落库**、`add()` 也确实把新向量塞进了 `ids`/`matrix`/`noteSpans`，
   *   但 `search()` / `searchAsync()` 因为 `loaded === false` 恒返回空
   *   ⇒ 用户看到的是「搜不到」，界面上没有任何错误（`lobster-init.ts:87` 只 warn）。
   *   实测（Node 22，复刻 §902 的失败形态）：load 抛 → add 一条 → 搜索恒 0 命中。
   *
   * ⚠ 为什么 `!this.loaded` 这个闸本来就没价值：`prepare()` 的**下一句**已经是
   *   `this.ids.length === 0` ⇒ 「索引是空的」本来就挡住了空索引。
   *   `loaded` 只在「有内容却 loaded=false」时才有话说，而那正是上面那个坏状态。
   *   ⇒ 这里保留 `loaded` 字段（它仍记录「是否装载过」），
   *     但凡**真的写进内存索引**就地置位，不让一个启动期的失败锁死整场会话。
   */
  private markPopulated(): void {
    this.loaded = true
  }

  /** 容量翻倍并搬一次（摊还后每次 add 的拷贝是常数级）。 */
  private grow(): void {
    const next = Math.min(this.maxVectors, Math.max(64, this.capacity * 2))
    if (next <= this.capacity) return
    const m = new Float32Array(next * DIM)
    m.set(this.matrix)
    this.matrix = m
    this.capacity = next
  }

  /** 删除一条向量（笔记删除时）。 */
  async remove(noteId: string, operation?: VectorOperation): Promise<void> {
    operation?.assertCurrent()
    await (operation?.db ?? this.db).run('DELETE FROM local_note_vectors WHERE note_id = ?', [noteId])
    await this.settleLoad()
    operation?.assertCurrent()
    if (this.noteSpans.has(noteId)) {
      // 重建内存索引（删除不频繁，全量重建可接受）
      await this.load(operation)
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
 * 把 SQLite BLOB 读出的字节转 Float32Array。
 *
 * ⚠⚠ 审计修复（2026-10-29）：不同驱动回读 BLOB 的形状**不一样** ——
 *   原生 Capacitor 插件回 `ArrayBuffer`，Web 的 jeep-sqlite/SQL.js 回
 *   `Uint8Array`。原实现直接 `new Float32Array(buf)`：传 `Uint8Array` 时
 *   会被当成「类数组」逐元素拷贝，**字节数被砍成 4 倍**
 *   （1536 维 ⇒ 读回来 384 维）⇒ 维度对不上 ⇒ 那一行被判不可用而
 *   **静默丢弃**。
 *   ⇒ 现在按「字节视图」统一处理：对齐到 4 字节边界后按小端 float32 读。
 *
 * 处理字节序：SQLite BLOB 按 native（小端）存，TypedArray 直接读即可。
 */
export function blobToFloat32(buf: ArrayBuffer | ArrayBufferView): Float32Array {
  const bytes = ArrayBuffer.isView(buf)
    ? new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength)
    : new Uint8Array(buf)
  const aligned = bytes.byteLength - (bytes.byteLength % 4)
  // 复制一份，保证 Float32Array 的 4 字节对齐要求成立。
  return new Float32Array(bytes.slice(0, aligned).buffer)
}
