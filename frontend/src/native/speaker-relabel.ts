/**
 * speaker-relabel.ts — 收尾时合并「被过度拆开」的说话人（纯逻辑，零 I/O）
 *
 * ══ 要解决的问题（2026-10-07 实测）══
 * ⛔⛔ **§597 勘误：原文写的是「真实人声谱 27 段」，那是错的。**
 *   仓内唯一能复现这批读数的数据是 `real-speech-embeddings.json`，
 *   而它 `_provenance.produced` 逐字写着「MiniMax TTS」、`voices` 列的是 9 个 TTS 音色
 *   ⇒ **那是合成语音，不是真人**。详见 §597.4。
 *   ⚠ 而下方那张「阈值 × 标签数」表**连 TTS 夹具也复现不出来**（§597.1），
 *   其数据来源已不在本仓 ⇒ **本常量 `RELABEL_MERGE_MIN` 的原始标定数据不可核销**。
 * 生产链路里声纹是**逐 VAD 段**算的（`ingest-speech.ts` 的
 * `extractEmbeddingResult(opts.blob)`），而 VAD 用 1500ms 静音切段
 * ⇒ 每次判定只看 **1.5~4 秒**的一次发言。实测该区间：
 *
 *   同人 min 0.9441 / 异人 max 0.9901 ⇒ **分离窗口 −0.0460（重叠）**
 *   两人会议（6 句）→ 屏幕上 **5 个「说话人 N」**；四人会议 → 7 个
 *   整体标签膨胀 **2.31×**，错合并 **0**
 *
 * 「错合并 0」是刻意的（张冠李戴 ≫ 标签多），代价就是标签膨胀。
 *
 * ══ 三条走不通的路（都实测过，别再走）══
 * 1. **调阈值**：0.97 → 22 标签但错合并 2；0.95 → 17 标签错合并 3。
 *    分布重叠时降阈值就是拿「张冠李戴」换「标签少」。
 * 2. **改特征参数**（15 组：mel 16/24/32/40、FFT 256/512/1024、
 *    F_MIN/F_MAX、仅均值、+Δ、去逐帧 CMVN）：窗口从 −0.0460 最多只到
 *    **−0.0393**（mel=16），**没有任何一组能让它转正**。
 * 3. **k-means 式二次重分配**：改写 **0/44 段**。原因是结构性的 ——
 *    贪心判错时会自建一个**单元素簇**，而单元素簇的质心就是它自己，
 *    余弦恒为 1.0 ⇒ 每个错分点都「完美匹配自己」，
 *    **重分配没有任何压力去合并它们**。这不是实现问题，是死路。
 *
 * ══ 唯一有效的杠杆：把「一次发言」换成「一个聚类的全部发言」══
 * 3.0s 不够长，但一个聚类整场累积下来的发言够长。
 * 把聚类成员的声纹取平均（= 长窗口统计的廉价代理），
 * 再做**单链凝聚**：反复合并最接近的两簇，每次合并后重算均值。
 * 实测（同一批 27 段）：
 *
 *   阈值   基线标签/错合并        合并后标签/错合并
 *   0.99   30(2.31×) 错0          30(2.31×) 错0     ← 长窗口也到不了 0.99
 *   0.97   30(2.31×) 错0          15(1.15×) 错1
 *   0.95   30(2.31×) 错0          12(0.92×) 错1
 *   0.90   30(2.31×) 错0          10(0.77×) 错2
 *
 * ⛔⛔⛔ **§597 实测：这张表在当前代码 + 当前夹具上复现不出来**（勿据此调参）：
 *   · **分母对不上**：30 / 2.31 = 12.99 ≈ 13，而夹具真值只有 **9** 个音色 ⇒ 表量的不是它；
 *   · **0.99 那行被实测打掉**：注释称「30→30，0 次合并」，而实测 0.99 在
 *     `long` bank 上合并了 1 次（10→9）、在 `short` 上合并了 1 次（20→19）；
 *   · **原文 0.97 与 0.95 两行都标「← 采用」自相矛盾**（常量只有 0.97 一个值）；
 *   · 已排除三个候选来源：`long` 全量 27 段（1.11×/10 标签）、`short` 全量 27 段（2.22×/20 标签）、
 *     以及 `speaker-relabel.test.ts` 里那 5 个 `PLANS`（只 6~8 段、只用 `short` bank）。
 *   ⇒ **一个生产常量的唯一依据是一张不可复现的表** —— 这是比「常数取错了」更该先修的问题。
 *
 *   两人会议 5→2、四人 7→3、三人 8→3（真值 2/4/3）
 *
 * ⚠ **代价写在明处**（§597 已把这两行按实测改掉，原文保留于上）：
 *   · 上表称「1 例错合并 / 2.31×→0.92×」；而**实测** `RELABEL_MERGE_MIN = 0.97` 在
 *     TTS 夹具 `long` bank 上**错并 2 个**（混人簇 `{A,D,I}`、`{B,F}`）、膨胀 0.67×
 *     ⇒ **代价被低估了一倍**；且 `:45` 那句「真人更接近、错合并会更多」被**证伪**：
 *     真人中文实测**零错并**（§595），夹具 2 个 ⇒ **方向正好相反**。这个交换是否划算，取决于产品更怕
 *   「张冠李戴」还是「认不出谁是谁」——本模块选了后者更可接受，
 *   因为**用户可以手动改名，而错配的纪要是已经污染过的**。
 *
 * ⚠⚠ **本阈值同样是 TTS 标定的，不是真人会议录音**（真人更接近、错合并会更多）。
 *   与 `speaker-diarization.ts` 的 `SAME_SPEAKER_MIN` 面临同一个未闭合的缺口：
 *   拿到真机样本后两个常量都要重标。
 *
 * ══ 两条不可越过的安全线 ══
 * 1. **用户改过名的 profile 是锚点，永不参与合并**。
 *    他亲手把「说话人 3」改成「张伟」就是在声明这是两个人；
 *    自动聚类把它并掉 = 推翻用户输入。
 * 2. **只合并自动标签**。回落标签（`known:false`）没有声纹，不进候选。
 */
import { cosineSimilarity, isUsableEmbedding } from './speaker-features.ts'
import { isPlaceholderSpeaker } from '../features/meetings/meeting-roster-homophone.ts'

/**
 * 合并阈值。
 *
 * ⚠ 与 `SAME_SPEAKER_MIN`（0.99）**故意不同**，两者量纲一样但含义不同：
 *   · 0.99 = 「这一次发言 vs 一次发言」够不够像（同人判别）
 *   · 0.97 = 「一个聚类的全部发言 vs 另一个聚类的全部发言」够不够像
 *     （聚类合并）。后者的统计量本身就稳得多，窗口宽 6 倍。
 *   照抄 0.99 会让这一步完全空转（实测 30→30，0 次合并）。
 *
 * ══ 为什么是 0.97 而不是更激进的 0.95（2026-10-07 噪声实测）══
 * 干净 TTS 上 0.95 看起来更好（膨胀 0.92× vs 1.15×，两人会议都是 5→2）。
 * 但那是在**没有环境干扰**的条件下选的，而真实会议有人说话、有空调。
 * 把**别的说话人的真实语音**当背景加进去（babble，最贴近会议场景）后：
 *
 *   环境          0.95                0.97
 *   干净      0.91×  错合并 1      1.18×  错合并 1
 *   babble 20dB  0.91×  错 1        0.91×  错 1
 *   babble 15dB  0.82×  **错 2**    1.00×  错 1
 *   babble 10dB  0.55×  **错 4**    0.91×  错 1
 *
 * 病因：babble 把**别的音色物理地混进了本片段**，同人 min 与异人 max
 * 一起塌向 1.0（10 dB 时异人 max 达 1.0000）⇒ 绝对阈值面前「人人达标」。
 * 白噪则完全不吃（40→0 dB 恒为 0.91×/错 1）——**问题出在语音干扰，不是底噪**。
 *
 * ⚠ 干净数据上两者错合并数**相同（都是 1）**，只是位置相反：
 *   0.95 把四人会议错并（错合并方向）；0.97 让人数三人会议欠合并（安全方向）。
 *   按「错合并 ≫ 欠合并」的代价不对称（见文件头与 speaker-diarization.ts §8），
 *   欠合并才是该选的那一侧。
 *
 * ⚠⚠ 5 dB babble 档**没有任何阈值能救**（0.97~0.99 全是 4~5 个错合并），
 *   但那里的错合并在**合并之前就已经有 4 个** ⇒ 失效的是实时贪心判定本身
 *   （`SAME_SPEAKER_MIN`），不是这一步。该档视为超出工作范围。
 *
 * ⚠ **仍然是 TTS 标定**，真人会议更接近。`RELABEL_MERGE_MIN` 的下界
 *   由 `speaker-relabel.test.ts` 的常量地板门守着，理由见那里。
 */
export const RELABEL_MERGE_MIN = 0.97

/** 一次 `identify` 的记录。 */
export interface RelabelSample {
  profileId: string
  embedding: Float32Array
  /**
   * 用户是否已亲手命名。
   *
   * true ⇒ 锚点：**不参与任何合并**（可被吸收，但自身不发起合并）。
   */
  pinned?: boolean
}

export interface RelabelMerge {
  /** 存活的聚类（标签沿用它） */
  kept: string
  /** 被吸收的聚类 */
  dropped: string
  /** 两者聚类均值的余弦 */
  sim: number
}

export interface RelabelCluster {
  /** 存活的 profileId（标签沿用它） */
  id: string
  /** 合并后的成员数 */
  size: number
  /** 合并后重算的均值向量（已 L2 归一化） */
  mean: Float32Array
  /** 用户是否已命名（锚点） */
  pinned: boolean
}

export interface RelabelResult {
  /** profileId → 合并后的权威 profileId（未参与合并的自己指向自己） */
  canonicalOf: Map<string, string>
  /** 实际发生的合并，按发生顺序 */
  merges: RelabelMerge[]
  /** 收敛后的聚类（含均值）。暴露出来是为了让「合并后均值被重算过」可断言。 */
  clusters: RelabelCluster[]
}

/** L2 归一化；零向量原样返回（由调用方的可用性判定负责拒收）。 */
function normalize(v: Float32Array): Float32Array {
  let n = 0
  for (let i = 0; i < v.length; i++) n += v[i] * v[i]
  n = Math.sqrt(n)
  if (!n) return new Float32Array(v)
  const o = new Float32Array(v.length)
  for (let i = 0; i < v.length; i++) o[i] = v[i] / n
  return o
}

interface Cluster {
  id: string
  members: Float32Array[]
  mean: Float32Array
  pinned: boolean
  firstAt: number
}

function meanOf(vs: Float32Array[]): Float32Array {
  const dim = vs[0].length
  const acc = new Float64Array(dim)
  for (const v of vs) for (let i = 0; i < dim; i++) acc[i] += v[i]
  const o = new Float32Array(dim)
  for (let i = 0; i < dim; i++) o[i] = acc[i] / vs.length
  return normalize(o)
}

/**
 * 合并被过度拆开的说话人聚类。
 *
 * 单链凝聚：每轮取「聚类均值余弦最大」的一对，达标就合并并重算均值。
 *
 * ⚠ **只有未命名的（pinned=false）聚类能发起合并**。锚点可以被吸收，
 *   但两个锚点之间永不合并 —— 见文件头「安全线 1」。
 *
 * @param threshold 合并阈值；默认 {@link RELABEL_MERGE_MIN}
 */
export function relabelClusters(
  samples: readonly RelabelSample[],
  threshold: number = RELABEL_MERGE_MIN,
): RelabelResult {
  const canonicalOf = new Map<string, string>()
  const merges: RelabelMerge[] = []
  // ── 建初始簇（按首次出现顺序，保证结果确定）──
  const byId = new Map<string, Cluster>()
  samples.forEach((s, i) => {
    // ⚠ 判据是 `isUsableEmbedding` 而不是 `length === 0`：
    //   **全零向量**（长度对、值全 0）混进 meanOf 后会得到一个合法的
    //   「半个平均值」，于是这个本该被丢弃的样本反而建出一个能参与合并的簇。
    //   生产侧正是靠 `isUsableEmbedding` 挡这一刀（见 speaker-features.ts
    //   头注释：提取失败曾被伪装成合法空向量，导致每次失败都算一个新说话人）。
    if (!isUsableEmbedding(s.embedding)) {
      canonicalOf.set(s.profileId, s.profileId)
      return
    }
    let c = byId.get(s.profileId)
    if (!c) {
      c = { id: s.profileId, members: [], mean: new Float32Array(0), pinned: false, firstAt: i }
      byId.set(s.profileId, c)
    }
    c.members.push(normalize(s.embedding))
    // pinned 只会从 false 变 true（同名用户后改的名也算锚点）
    if (s.pinned) c.pinned = true
  })
  for (const c of byId.values()) c.mean = meanOf(c.members)
  for (const c of byId.values()) canonicalOf.set(c.id, c.id)

  // ── 单链凝聚 ──
  const alive = new Set(byId.values())
  for (;;) {
    let bi: Cluster | null = null
    let bj: Cluster | null = null
    let bs = -Infinity
    for (const a of alive) {
      for (const b of alive) {
        if (a === b) continue
        // ★ 安全线 1：合并只能由**未命名**的一侧发起。
        //   两个都 pinned ⇒ 跳过（用户亲手声明过他们是不同的人）。
        //   恰好一个 pinned ⇒ 允许 pinned 吸收对方（他只是碎片）。
        if (a.pinned && b.pinned) continue
        // 维度不同的簇不可比，cosineSimilarity 返回 -1，不会成为最优对。
        const s = cosineSimilarity(a.mean, b.mean)
        if (s > bs) { bs = s; bi = a; bj = b }
      }
    }
    if (!bi || !bj || bs < threshold) break

    // ★ 存活方：锚点**永远**存活。
    //   早先只按「成员多的一侧留」选，成员数相同时按先出现 ——
    //   于是先出现的未命名碎片会吞掉用户亲手命名的 profile
    //   （两个簇各 2 段时 p1 赢过 p2），直接推翻用户输入。
    //   现在：恰好一侧是锚点 ⇒ 锚点留；两侧都不是 ⇒ 多的留、同数取先出现的。
    const [keep, drop] = bi.pinned !== bj.pinned
      ? (bi.pinned ? [bi, bj] : [bj, bi])
      : bi.members.length !== bj.members.length
        ? (bi.members.length > bj.members.length ? [bi, bj] : [bj, bi])
        : (bi.firstAt <= bj.firstAt ? [bi, bj] : [bj, bi])
    keep.members = [...keep.members, ...drop.members]
    keep.mean = meanOf(keep.members)
    keep.pinned = keep.pinned || drop.pinned
    alive.delete(drop)
    canonicalOf.set(drop.id, keep.id)
    merges.push({ kept: keep.id, dropped: drop.id, sim: bs })
  }

  return {
    canonicalOf,
    merges,
    clusters: [...alive].map((c) => ({
      id: c.id, size: c.members.length, mean: c.mean, pinned: c.pinned,
    })),
  }
}

/**
 * 把 canonicalOf 沿链压平。
 *
 * ⚠ 必须压平：`canonicalOf` 是**逐次**写入的，一次三连合并
 *   （A→B，B→C）会让 A 仍指向已被吸收的 B。
 *   不压平 ⇒ A 的段被贴上 B 的标签，而 B 已经是死簇。
 */
export function resolveCanonical(
  canonicalOf: Map<string, string>,
  profileId: string,
): string {
  const seen = new Set<string>()
  let cur = profileId
  while (canonicalOf.has(cur) && canonicalOf.get(cur) !== cur) {
    if (seen.has(cur)) return cur          // 环（不该发生）时不要死循环
    seen.add(cur)
    cur = canonicalOf.get(cur)!
  }
  return cur
}

// ────────────────────────────────────────────────────────────────────
// 接线层的决策（纯函数）——把「哪些段该改标签」从 runtime 里搬出来
// ────────────────────────────────────────────────────────────────────

export interface RelabelUpdate { segId: string; label: string }

export interface RelabelPlan {
  /**
   * segId → 合并后的 profileId。
   *
   * ★ **无论标签是否被改写，都要重写**：它表达的是声学归属，
   *   后续用户按人改名（`labelSpeaker`）要能覆盖到这一段。
   */
  remap: Map<string, string>
  /** 需要回贴标签的段。**只含仍是占位标签的那些**（见下）。 */
  updates: RelabelUpdate[]
}

/**
 * 决定「合并之后，哪些分段要改说话人标签」。
 *
 * ★ **只回贴仍是占位标签（`说话人 N`）的段**。
 *   用户中途亲手改过名的段，它的标签是他自己的输入 ——
 *   自动聚类无权覆盖（§40 刚把「用户改过的名字被收尾吞掉」修回来，
 *   这次不能再犯一遍）。
 *
 * ⚠ 抽成纯函数而不是留在 runtime 里：runtime 拖进 vue / capacitor，
 *   node 里跑不到，于是「只改占位标签」这条决定会**一行行为门都没有**。
 *   这与 `planRefine` / `buildRefineBaseSegments` 是同一条理由。
 */
export function planSpeakerRelabel(opts: {
  /** 现状：segId → profileId */
  segmentProfiles: Iterable<readonly [string, string]>
  canonicalOf: ReadonlyMap<string, string>
  /** 合并后 profileId 对应的显示名 */
  labelOf: (profileId: string) => string | undefined
  /** 分段当前的标签（用来判断是不是占位） */
  currentLabelOf: (segId: string) => string | null | undefined
}): RelabelPlan {
  const remap = new Map<string, string>()
  const updates: RelabelUpdate[] = []
  for (const [segId, pid] of opts.segmentProfiles) {
    const next = resolveCanonical(new Map(opts.canonicalOf), pid)
    if (next === pid) continue
    const label = opts.labelOf(next)
    if (!label) continue              // 目标 profile 已不在库里：不动，留着比写空强
    remap.set(segId, next)
    if (!isPlaceholderSpeaker(opts.currentLabelOf(segId))) continue
    updates.push({ segId, label })
  }
  return { remap, updates }
}
