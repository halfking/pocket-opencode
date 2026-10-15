/**
 * speaker-diarization.ts — 增量说话人聚类
 *
 * 新 embedding 与已有 profile 比对（余弦 ≥ `SAME_SPEAKER_MIN` 视为同一人）。
 * 冷启动时显示「说话人 N」，用户标注后写入 voiceprints 库。
 *
 * ══ 阈值为什么从 0.72 改成 0.99 ══
 * 阈值必须与**特征**配套，改特征不改阈值等于把判据废掉：
 *
 *   旧特征（时域包络，音色全盲）：不同说话人的相似度实测 0.24，
 *     同一人换个说法也只有 0.5 左右 ⇒ 0.72 看起来「能用」，
 *     其实只对「说话节奏」有效。
 *   新特征（log-mel 48 维）：**任何两个语音段**的相似度都在 0.97 以上，
 *     同一人 0.9984~1.0000，异人 0.9752~0.9867。
 *     ⇒ 沿用 0.72 会把整场会议**合并成一个人**。
 *
 * ⚠ 这个 0.99 是用**合成语音**（不同基频 + 不同共振峰的谐波叠加）标定的，
 *   实测同一人最低 0.9984、异人最高 0.9867，0.99 落在两者之间。
 *   真机真人录音的区间会宽一些，**尚未按真实 ASR/录音数据校准**——
 *   拿到真机样本后应重新标定，改这一个常量即可。
 *
 * ══ 为什么合并要「又高又唯一」 ══
 * 两种错法的代价不对称：
 *   · **错误合并**（两个不同的人被认成同一人）→ 转写内容张冠李戴，
 *     纪要和行动项会把 A 的话记到 B 头上。**严重**。
 *   · **错误拆分**（一个人被拆成多个说话人）→ 只是标签多、难看，
 *     用户还能手动改名。**轻微**。
 * 所以判据只在「相似度足够高**且**明显甩开次优候选」时才合并，
 * 其余一律新建说话人。宁可多几个标签，不可错配人。
 */
import { cosineSimilarity, isUsableEmbedding } from './speaker-features.ts'
import { relabelClusters, resolveCanonical, type RelabelSample } from './speaker-relabel.ts'

export interface SpeakerProfile {
  id: string
  label: string
  embedding: Float32Array
  sampleCount: number
  /**
   * 用户是否已亲手命名。
   *
   * true ⇒ 收尾合并的**锚点**：不参与任何合并（可被吸收，自身不发起）。
   * 用户把「说话人 3」改成「张伟」就是在声明这是两个人，
   * 自动聚类把它并掉 = 推翻用户输入。
   */
  pinned?: boolean
}

/**
 * 判定「是同一个人」的最低余弦相似度。
 *
 * ⚠ 与 log-mel 特征强绑定。换成别的特征必须重新标定，
 *   照抄 0.72 之类的旧值会让整场会议塌成一个人。
 *
 * ══ 标定依据（2026-10-07 实测，MiniMax TTS 9 音色 × 3 句）══
 *   片段 5.3~7.6 秒（接近真实 VAD 分段）：
 *     同人 min 0.9850 / 中位 0.9932；异人 p90 0.9645 / p99 0.9839 / max 0.9901
 *     ⇒ 阈值 0.990 时：**异人误并 1.2%、同人拆开 7.4%**，接近最优点
 *       （降到 0.985 虽让同人 0% 拆开，但异人误并升到 3.4% —— 那是更严重的错）
 *   片段 3.0 秒（短段，最坏情况）：
 *     ⇒ 阈值 0.990 时**同人拆开 66.7%**，异人误并 0.3%
 *
 * ⚠⚠ **分段长度是主导因素，不是阈值**。VAD 用 1.5 秒静音就切段
 *   （`vad-segmenter.ts` 的 `silenceMs: 1500`），自然语流里短段是常态。
 *   3 秒段落下一个人会被拆成好几个说话人 —— 任何单阈值都改不掉这件事
 *   （降到 0.96 能把拆开率压到 7.4%，但异人误并会升到 13.9%）。
 *
 * ⚠⚠ 「同人」与「异人」两个分布**存在重叠** ⇒ 单阈值聚类在真实多人会议里
 *   原理上就不够。要真正对齐需要二者之一：
 *   ① 用**已知参会人数**约束下的强制分配（产品已有 participants 输入）；
 *   ② 换成真实声纹模型 —— ⛔ **本仓当前没有这条路**，不要按「已具备」去排期。
 *
 *     ⚠⚠ 「原生 cap-sherpa ECAPA」只在**插件骨架**层面存在（不是可用能力）：
 *     `SherpaPlugin.java` 只有 54 行，5 个功能方法恒
 *     `reject("sherpa-onnx AAR not integrated (Phase 4)")`；iOS 无 Swift 实现、
 *     assets 无 `.onnx` 模型 ⇒ 已在 `lib/shell/capabilities.ts` 的
 *     `NOT_IMPLEMENTED_PLUGINS` 登记为「已注册但未实现」，
 *     并由 `capabilities-stub-gate` 对着 Java 源码指纹守着。
 *
 *     ⇒ 真机（含 Android）跑的**全是 Web 兜底 log-mel 48 维**
 *       （`speaker-embedding.ts` 的 `extractEmbeddingResult`：原生抛错 → 落到
 *       `logMelEmbedding`）—— 与下面这段标定依据**同一路特征**，
 *       这也是「阈值没有与特征脱钩」的原因。
 *     ⇒ 要走 ② 得先把 AAR 接进来，那是**独立立项**，不是调阈值能解决的。
 *
 * 🔒 这个常量由 `speaker-calibration.test.ts` 用**真实频谱特征**守着（**不是**真人录音：
 *   夹具是 **TTS 音频**，无房间混响、无环境噪声、无手机麦克风频响），
 *   改动它会先打红那道门。
 *   ⚠ 措辞说明：这里说的是「相对被替换掉的**时域包络**而言是真频谱」，
 *   **不是**「真人会议录音」—— 这两个含义在旧注释里混用过，别再照抄。
 *   ⚠⭐ 更要紧的是**音频来源**：真机是真人 + 真实底噪，而夹具是零噪声干声 ⇒
 *   **阈值在真机底噪下没有标定依据**，需要真机复核（见 docs/design/2026-10-11-pending-decisions-brief.md 的 #54）。
 *   详见该文件头注释与 docs/design/2026-10-07-speaker-diarization.md。
 */
export const SAME_SPEAKER_MIN = 0.99

/**
 * 最优候选必须比次优候选高出的量。
 *
 * 没有它时，一个新说话人只要**碰巧**与某一个旧 profile 高度相似，
 * 就会被并进去，即使他与另一个 profile 更像。留出这个间隔才叫「唯一」。
 */
export const AMBIGUITY_MARGIN = 0.001

/** 判定结果。`known:false` 表示「本次没能区分说话人」。 */
export interface IdentifyResult {
  profileId: string
  label: string
  isNew: boolean
  /**
   * 是否真的做了说话人判别。
   *
   * ⚠ false 时 `label` 是**兜底**（整场共用一个），不是「识别出了这个人」。
   *   混掉这两者会让调用方把「不知道是谁」当成「知道是谁」去写进数据库。
   */
  known: boolean
}

export class SpeakerDiarizer {
  private profiles: SpeakerProfile[] = []
  private nextId = 1
  private threshold: number
  /**
   * embedding 取不到时的兜底 profile（整场共用）。
   *
   * ⚠ 为什么不沿用「每次新建」的旧行为：取不到 embedding 是**全局性**的
   *   （sherpa 不可用 + webm 解码失败），不是某一段的偶发。
   *   逐段新建 ⇒ 一个人 30 段话被标成「说话人 1…30」，
   *   整份纪要全是无意义的标签。而此时我们**没有任何证据**说明是多人。
   */
  private fallback: SpeakerProfile | null = null
  /** 本次录音实际在用的特征维度；第一次拿到真向量后确定并清扫一次。 */
  private settledDim: number | null = null
  /**
   * 本次录音每段发言的声纹（按判定顺序）。
   *
   * ★ 为什么必须留住（2026-10-07）：`SAME_SPEAKER_MIN` 是按**单次发言**
   *   （1.5~4s，VAD 按 1500ms 静音切段）标定的，而该区间两个分布重叠
   *   0.0460 ⇒ 一个人被拆成好几个说话人（实测两人会议 5 个标签、膨胀 2.31×）。
   *   单次发言的信息量不足以判定身份，但**整场累积下来**就够 ——
   *   收尾时据此把碎片并回去（见 `relabel`）。
   */
  private samples: { profileId: string; embedding: Float32Array }[] = []

  constructor(threshold = SAME_SPEAKER_MIN) {
    this.threshold = threshold
  }

  /**
   * 灌入历史声纹。
   *
   * 只在这里丢弃**零向量**（提取失败写进库的行）。
   *
   * ⚠ 维度不符的**不在这里丢**：原生 cap-sherpa 的 ECAPA 是 192/512 维，
   *   Web 频谱特征是 48 维，两者都可能合法存在于同一个库里，
   *   而「当前这次的维度」要到第一次真正拿到 embedding 才知道
   *   （见 `identify` 里的 `settledDim` 清扫）。在这里按固定维度筛会
   *   把合法的原生声纹一起删掉。
   *
   * @returns 被丢弃的行数（写入日志用）。
   */
  loadProfiles(profiles: SpeakerProfile[]): number {
    const usable: SpeakerProfile[] = []
    let dropped = 0
    for (const p of profiles) {
      if (isUsableEmbedding(p.embedding)) usable.push({ ...p })
      else dropped++
    }
    this.profiles = usable
    this.settledDim = null
    // 换库即换一场会：上次的声纹不能带进这次的合并判定。
    this.samples = []
    const nums = usable
      .map((p) => parseInt(p.label.replace(/\D/g, ''), 10))
      .filter((n) => !isNaN(n))
    this.nextId = nums.length > 0 ? Math.max(...nums) + 1 : 1
    return dropped
  }

  /**
   * 清扫一次「已确定不属于当前特征空间」的 profile。
   *
   * 为什么必须清：维度不同 ⇒ 永远比不过阈值 ⇒ 每段都判成新说话人。
   * 旧版写进 `local_voiceprints` 的 32 维包络特征正是这种死重 ——
   * 不清掉，用户升级后每开一次录就会凭空多出一批陌生说话人。
   */
  private settleDimension(dim: number): void {
    if (this.settledDim === dim) return
    this.settledDim = dim
    this.profiles = this.profiles.filter((p) => p.embedding.length === dim)
  }

  /**
   * 识别说话人。
   *
   * @param embedding 声纹向量；`null`/不可用 ⇒ 不做判别，走整场共用的兜底标签。
   */
  identify(embedding: Float32Array | null | undefined): IdentifyResult {
    if (!isUsableEmbedding(embedding)) return this.fallbackIdentify()
    this.settleDimension(embedding.length)

    // ⚠ 这里**不需要**再判维度：`settleDimension` 已在上面把 profiles 统一成
    //   当前维度（历史库里 32 维包络、192/512 维 ECAPA 都在那一步被清掉）。
    //   加一道 `isComparable` 是不可达的死代码 —— 它永远为真，
    //   而「不可达 + 无门禁覆盖」正是死代码的两种典型形态。
    //   不变式由 speaker-diarization.test.ts 的
    //   「清扫后所有 profile 与本次特征同维度」盯着。
    let best: SpeakerProfile | null = null
    let bestSim = -Infinity
    let runnerUpSim = -Infinity
    for (const p of this.profiles) {
      const sim = cosineSimilarity(embedding, p.embedding)
      if (sim > bestSim) {
        runnerUpSim = bestSim
        bestSim = sim
        best = p
      } else if (sim > runnerUpSim) {
        runnerUpSim = sim
      }
    }

    const unambiguous = bestSim - runnerUpSim >= AMBIGUITY_MARGIN
    if (best && bestSim >= this.threshold && unambiguous) {
      // 增量更新 embedding（滑动平均）。⚠ 维度必须先验一致：
      //   旧实现按 best.embedding.length 遍历 embedding，若不等长会
      //   多读出 undefined ⇒ NaN 污染该 profile 的**每一个**后续样本。
      this.mergeInto(best, embedding)
      best.sampleCount++
      this.samples.push({ profileId: best.id, embedding: new Float32Array(embedding) })
      return { profileId: best.id, label: best.label, isNew: false, known: true }
    }

    return this.createProfile(embedding)
  }

  /**
   * 本次录音已记录的声纹样本数。
   *
   * 诊断用：它是 `relabel()` 唯一的数据来源。读数为 0 ⇒ 合并永远空转
   * （音频到不了、或 `identify` 根本没记录），
   * 那时「合并没效果」不是阈值问题，是压根没料可合。
   */
  get recordedSampleCount(): number {
    return this.samples.length
  }

  /**
   * 收尾合并：把被过度拆开的说话人并回去，并把死簇从 `profiles` 里摘掉。
   *
   * @returns profileId → 合并后的权威 profileId。空表 = 本场没有可合并的碎片。
   *
   * ⚠ **必须在所有分段落库之后调用**（`recordingRuntime.stop()` 里
   *   等 `inFlightSegments` 清空之后）。提前调用会漏掉最后几段发言，
   *   而那几段往往正是让某个聚类变得可判定的关键。
   */
  relabel(threshold?: number): Map<string, string> {
    if (this.samples.length < 2) return new Map()
    const pinnedOf = new Map(this.profiles.map((p) => [p.id, p.pinned === true]))
    const samples: RelabelSample[] = this.samples.map((s) => ({
      profileId: s.profileId,
      embedding: s.embedding,
      pinned: pinnedOf.get(s.profileId) === true,
    }))
    const { canonicalOf, merges, clusters } = relabelClusters(samples, threshold)
    if (merges.length === 0) return new Map()

    // 死簇必须从 profiles 里摘掉，否则 `syncSpeakers()` 仍会把它们
    // 列给用户选 —— 合并做了，界面却看不出来，等于没做。
    const dead = new Set(merges.map((m) => m.dropped))
    this.profiles = this.profiles.filter((p) => !dead.has(p.id))

    // ⚠⚠ **只更新 sampleCount，绝不把存活方的 embedding 换成聚类均值。**
    //   曾这么做过，随即被门禁逮到：合并后同一个人的新发言对合并质心的
    //   实测余弦是 0.9970 / 0.9912 / **0.9895**（阈值 0.99）⇒ 立刻又分裂。
    //   根因是**两套统计量的标定基准不同**：
    //     · `SAME_SPEAKER_MIN = 0.99` 按「单次发言 vs 单次发言」标定；
    //     · 聚类均值是**长窗口**统计（3~9 段），单次发言去比它系统性偏低。
    //   这不是可达性假设问题：diarizer 在**同一 session 内换场录音时会被复用**
    //   （`recordingRuntime` 的 `if (freshSession || !this.diarizer)`），
    //   「录完合并 → 再录一场」就会踩到。
    //   ⇒ 合并只负责**分组**；比对基准保持「单次发言的滑动平均」不变。
    for (const c of clusters) {
      const target = this.profiles.find((p) => p.id === c.id)
      if (target) target.sampleCount = c.size
    }
    this.samples = []
    return canonicalOf
  }

  /** 滑动平均合并；维度不等长时直接放弃更新（保留原向量，胜过写成 NaN）。 */
  private mergeInto(target: SpeakerProfile, embedding: Float32Array): void {
    if (target.embedding.length !== embedding.length) return
    const alpha = 0.3
    for (let i = 0; i < target.embedding.length; i++) {
      target.embedding[i] = target.embedding[i] * (1 - alpha) + embedding[i] * alpha
    }
    const norm = Math.hypot(...Array.from(target.embedding))
    if (norm > 0) {
      for (let i = 0; i < target.embedding.length; i++) target.embedding[i] /= norm
    }
  }

  private createProfile(embedding: Float32Array): IdentifyResult {
    const id = `sp-${Date.now()}-${this.nextId}`
    const label = `说话人 ${this.nextId}`
    this.nextId++
    this.profiles.push({ id, label, embedding: new Float32Array(embedding), sampleCount: 1 })
    this.samples.push({ profileId: id, embedding: new Float32Array(embedding) })
    return { profileId: id, label, isNew: true, known: true }
  }

  /** 拿不到特征时的兜底：整场共用一个标签，且**不**进 profiles。 */
  private fallbackIdentify(): IdentifyResult {
    if (!this.fallback) {
      this.fallback = { id: 'sp-unknown', label: '说话人 1', embedding: new Float32Array(0), sampleCount: 0 }
    }
    this.fallback.sampleCount++
    return { profileId: this.fallback.id, label: this.fallback.label, isNew: false, known: false }
  }

  /** 用户标注：将 profile 绑定到显示名。**同时把它钉成合并锚点。 */
  labelProfile(profileId: string, displayName: string) {
    if (this.fallback && this.fallback.id === profileId) this.fallback.label = displayName
    const p = this.profiles.find((x) => x.id === profileId)
    if (p) {
      p.label = displayName
      // ⚠ 只有**用户主动改名**才会走到这里（`recordingRuntime.labelSpeaker`
      //   是唯一的调用点）⇒ 这就是「用户声明过这是谁」的信号。
      //   收尾合并据此不把他并进别人，也不把他并掉（见 SpeakerProfile.pinned）。
      p.pinned = true
    }
  }

  getProfiles(): SpeakerProfile[] {
    return [...this.profiles]
  }

  getProfile(profileId: string): SpeakerProfile | undefined {
    return this.profiles.find((p) => p.id === profileId)
  }
}
