/**
 * meeting-dedup.ts — 相邻语音段之间的**重叠去重**（前端侧）。
 *
 * ── 为什么需要它（2026-10-06）──
 *
 * 用户实测反馈「录音片段重复」。追查发现真正的链路是：
 *
 *   VadSegmenter.finalizeSegment() 按 [speechStartMs-sliceMs, endMs] 取
 *   sliceBuffer 里的音频片段 → 每段独立 POST /stt/transcribe →
 *   ingestSpeechBlob 把结果**原样**存成一条 MeetingSegment →
 *   updateTranscript 用 '\n' 拼起来。
 *
 * ★ 也就是说：**整条前端链路没有任何去重**。后端 stt/incremental.go 里的
 *   mergeIncremental 写了一套去重，但前端从来没调用 IncrementalTranscriber
 *   ——它走的是逐段 sttApi.transcribe。所以那次修的是一条没人走的路径。
 *
 * 重复是怎么产生的（VadSegmenter 逐行核对后确认）：
 *   - sliceBuffer 是**累积队列**，只按 atMs >= cutoff(60s前) 滚动；
 *   - finalizeSegment 的取片窗口是 [speechStartMs - sliceMs, endMs]，
 *     其中 sliceMs(250ms) 是为了补偿 MediaRecorder 的缓冲延迟；
 *   - speechStartMs 来自 requestAnimationFrame 的能量判定。RAF 在后台标签页
 *     会被节流到 1fps 以上，语音起点的判定会**回退**；
 *   - 一旦相邻两段的取片窗口重叠，同一段音频会被转写两次，
 *     文本就变成「今天今天下午三点」。
 *
 * 这与讯飞听见/Otter 的做法一致：切片之间本来就要留重叠余量来避免断词，
 * 代价是必须在**文本侧**消解掉重叠。FunASR 官方部署矩阵里的两阶段模式
 * 同样是先流式再整段修正，而两阶段之间也必须做这一步融合。
 *
 * ── 为什么不用后端那份实现 ──
 *
 * 后端 stt/incremental.go 的 mergeIncremental 与本文件是同一套算法
 * （LCS 对齐 + anchorCoverage 闸），但 Go 与 TS 两地维护同一套阈值
 * 容易漂移。之所以仍写一份而不是共用：跨语言无法直接复用。
 * 两边的阈值常量在注释里互相标注，改一处必须同步另一处。
 *
 * ⚠⚠ 2026-10-13（§523）更正本段原文的一句错述：原文写「后端那份目前的调用方是
 * IncrementalTranscriber（**保留给将来的真流式通道**）」——「保留给将来」是错的。
 * IncrementalTranscriber 在**随手记链**上**每 3 秒就被调一次**
 * （recordingRuntime.sendSlice → /stt/transcribe-incremental），
 * 那条路的切片窗口**不重叠**（takeNewWindow 送纯新增窗口）。
 * ⇒ 「会议链走本文件、随手记链走后端那份」这个分工成立，
 *   但**不能说后端那份是死代码**，也不能把会议链的读数套到它身上：
 *   实测两边的去重在各自**真实形状**上都是**裁 0 字**
 *   （随手记链 23/23 边界落兜底；会议链 25/25 落兜底），
 *   重叠 ≥1.6s 时闸门才会触发。读数与分工见设计文档 §523。
 */

/**
 * LCS 覆盖率下限：这次识别出来的字里有多大比例是重复的。
 *
 * 取 0.6 的理由与后端一致（stt/incremental.go 的 anchorCoverage）：
 *   - 调低（0.5）会把「共享常用词的两句话」误判成重叠而吃掉真实内容；
 *   - 调高（0.7）会让真实的切片重叠因一两个字的差异漏判，重复照旧。
 * 中文短段的重叠区通常十几个字，容忍 1-2 个字增删对应 0.85+ 的覆盖率，
 * 留 0.6 是给 ASR 幻听留余量。
 *
 * ⚠ 曾降到 0.40 又撤回：那次是在**诱导数据**上测的（我在生成 TTS 时就写了
 *   两段不同文本，再称之为「ASR 的同音错误」）。真实切分数据重测后
 *   覆盖率本就落在 0.6471 / 0.9167，0.6 本来就够用，降到 0.40 没有收益只有代价。
 *   教训：**先证明「差异来自被测系统」，再把它算成被测系统的缺陷。**
 */
const ANCHOR_COVERAGE = 0.6

/**
 * LCS 自身的最小长度。与后端 `incremental.go` 的 `minFuzzyAnchor` 同值。
 *
 * ⚠ 它与 ANCHOR_COVERAGE **不是**一道闸的两面：coverage 量的是「LCS 占对齐窗口
 * 多大比例」，本常量量的是「LCS 本身够不够长」。窗口很小时两者会给出相反答案
 * （limit=4、lcsLen=3 时 coverage 过、本闸拦），那一格正是用户话被删掉的地方。
 */
const MIN_ANCHOR = 4

const MAX_OVERLAP = 40

/**
 * §914：取字符串**末尾最多 `max` 个码点**，且**只扫末尾那几十个码元**。
 *
 * ⚠⚠ 为什么需要它（这是本节修的那个二次方的根）：
 *   原实现在 `dedupeSegmentText` / `dedupeSegmentTextSoft` 里做的是
 *   `Array.from(committed)` —— 把**整段累积正文**转成码点数组，
 *   而且**每段做一次**。⇒ 单段成本 O(累积正文长度)，总量 **O(n²)**。
 *   实测（§914.3）：8 小时会议（1958 段）光去重就 **6.6 秒**、瞬时堆涨 461MB；
 *   24 小时（5876 段）**直接 OOM**（4GB 堆 / 109 秒）。
 *
 * ⚠ 为什么不能简单改成 `Array.from(committed.slice(-MAX_OVERLAP))`：
 *   `slice` 按 **UTF-16 码元**切，会把代理对（emoji 等非 BMP）切成两半
 *   ⇒ 码点数组与原来**不等价**。
 *   本函数从末尾**按码点**回扫，遇代理对整对取走 ⇒ 拿到的正是
 *   「最后 min(max, 总码点数) 个码点」，与原实现的窗口**逐字相同**。
 *
 * ⚠ 为什么只取末尾窗口就够：`lcsNetNew` 内部只用
 *   `cs.slice(cs.length - limit)`，而 `limit = min(MAX_OVERLAP, cs.length, ns.length)`
 *   ⇒ 它**永远只看末尾 ≤ MAX_OVERLAP 个码点**。
 *   ⚠ 而 `limit` 的计算用的是 `cs.length`（= 总码点数）⇒ 截断后
 *   `cs.length = min(MAX_OVERLAP, 总码点数)`，
 *   于是 `min(MAX_OVERLAP, min(MAX_OVERLAP, 总), ns.length)`
 *   **恒等于** `min(MAX_OVERLAP, 总, ns.length)` ⇒ 窗口与输出**逐字不变**。
 */
function tailCodePoints(s: string, max: number): string[] {
  const out: string[] = []
  let i = s.length
  while (i > 0 && out.length < max) {
    const code = s.charCodeAt(i - 1)
    // 低代理 ⇒ 它属于一个代理对，必须连高代理一起取
    if (code >= 0xdc00 && code <= 0xdfff && i >= 2) {
      const hi = s.charCodeAt(i - 2)
      if (hi >= 0xd800 && hi <= 0xdbff) {
        out.push(s.slice(i - 2, i))
        i -= 2
        continue
      }
    }
    out.push(s[i - 1])
    i -= 1
  }
  return out.reverse()
}

/**
 * §914：去重的内部结果 —— 「净增」与「累积正文」**分开**返回。
 *
 * ⚠⚠ 为什么必须分开：原来只有「累积正文」一个返回值，调用方靠
 *   `body.slice(prev.length)` 取出净增。
 *   ⚠ 而 `body` 是一条 **ConsString 链**（`committed + delta` 一段段拼出来的），
 *   对它 `slice` 会**强制展平整条链** ⇒ 每段一次 O(累积正文长度) 的拷贝
 *   ⇒ 总量 **O(n²)**。
 *   实测（§914.3）：只修掉「窗口那一半」之后，12 小时会议**仍然 OOM**，
 *   栈是 `String::SlowFlatten` / `NewRawTwoByteString` ⇒ 正是展平拷贝。
 * ⇒ 现在直接把净增算出来返回，调用方**不用再 slice**。
 */
interface DedupeParts {
  /** 去重后的累积正文（= `committed + delta`）。 */
  body: string
  /** 本次的净增（可能是空串）。 */
  delta: string
}

/**
 * §914：`dedupeSegmentText` 的内部形态（返回净增 + 累积正文）。
 *
 * ⚠ `committedTail` = `committed` 末尾 ≤ `MAX_OVERLAP` 个**码点**。
 *   传了就用它，不再去碰 `committed` 本身（热路径上 `committed` 是一条
 *   正在增长的 ConsString 链，碰它就意味着展平 ⇒ O(n²)）。
 */
function dedupeSegmentTextParts(
  committed: string,
  next: string,
  minCoverage: number,
  committedTail?: string[],
): DedupeParts {
  const n = next.trim()
  if (!committed) return { body: n, delta: n }
  if (!n) return { body: committed, delta: '' }

  // 1) 精确公共后缀/前缀（原后端策略，命中即最可信）
  // ⚠⚠ §914：`cs` 现在**只含末尾 ≤ MAX_OVERLAP 个码点**（原来是对整段累积正文
  //   做 `Array.from(committed)`，每段一次 ⇒ O(n²)）。
  //   `limit` 的取值因此仍是 `min(MAX_OVERLAP, 总码点数, ns.length)`（见 tailCodePoints 注释），
  //   窗口与输出**逐字不变**。
  const cs = committedTail ?? tailCodePoints(committed, MAX_OVERLAP)
  const ns = Array.from(n)
  const limit = Math.min(MAX_OVERLAP, cs.length, ns.length)
  for (let k = limit; k >= 2; k--) {
    if (cs.slice(cs.length - k).join('') === ns.slice(0, k).join('')) {
      const delta = ns.slice(k).join('')
      return { body: committed + delta, delta }
    }
  }

  // 2) LCS 对齐消解（重叠区内部有标点/增删时必需）
  const netNew = lcsNetNew(cs, ns, minCoverage)
  if (netNew !== null) return { body: committed + netNew, delta: netNew }

  // 3) 判为不重叠，原样拼接 —— 宁可多几个字，也不要丢真实内容
  return { body: committed + n, delta: n }
}

/**
 * §914：`dedupeSegmentTextSoft` 的内部形态（返回净增 + 累积正文）。
 *
 * **保守版**：跳过精确匹配那条更激进的路径，只走 LCS 覆盖率闸。
 * 用途：时间轴已经表明两段之间**有正常静音间隙**时，纯文本形态与切片重叠
 * 完全一样（新段以旧段结尾开头），此时若还让精确匹配命中，就会把
 * 「用户真的重复说了一遍的话」吃掉（§41 记录的固有局限）。
 *
 * ⚠ `committedTail` 的语义见 `dedupeSegmentTextParts`。
 */
function dedupeSegmentTextSoftParts(
  committed: string,
  next: string,
  minCoverage: number,
  committedTail?: string[],
): DedupeParts {
  const n = next.trim()
  if (!committed) return { body: n, delta: n }
  if (!n) return { body: committed, delta: '' }
  // #58（属主选择①，2026-10-16）：与 Go mergeIncremental 的整段重复护栏对齐。
  // 只保护双方全文相同且超窗口这一格；不把累积正文的相同后缀视为全文重复。
  // Array.from 按码点计数，避免非 BMP 字符的 UTF-16 长度提前越过 40 字边界。
  const ns = Array.from(n)
  // ⚠⚠ 注意这行 `committed === n` **必须继续用整段 committed**：
  //   那是 #58 的「全文重复」护栏，一旦换成尾窗口，
  //   条件 `ns.length > MAX_OVERLAP` 与窗口长度矛盾 ⇒ 护栏**静默失效**。
  //   ⇒ 「比较用窗口、判全文用原文」这个分工是刻意的。
  //   ⚠ V8 的字符串相等先比长度 ⇒ 长度不等时是 O(1)，不会展平链条。
  if (committed === n && ns.length > MAX_OVERLAP) return { body: committed + n, delta: n }
  // ⚠⚠ §914：这里原来也是 `Array.from(committed)`（**整段累积正文**，每段一次
  //   ⇒ O(n²)）。现在只取末尾 ≤ MAX_OVERLAP 个码点 ——
  //   而 `lcsNetNew` 本来就只用 `cs.slice(cs.length - limit)`，
  //   `limit = min(MAX_OVERLAP, cs.length, ns.length)`
  //   ⇒ 截断后 limit 不变、窗口不变、**输出逐字不变**。
  const netNew = lcsNetNew(committedTail ?? tailCodePoints(committed, MAX_OVERLAP), ns, minCoverage)
  if (netNew !== null) return { body: committed + netNew, delta: netNew }
  return { body: committed + n, delta: n }
}

/**
 * §914：从「上一次的末尾窗口 + 本次净增」滚出新的末尾窗口。
 *
 * ⚠ 为什么这样滚是**精确**的：新窗口必须是「累积正文最后 ≤ max 个码点」。
 *   而 `末尾窗口(累积正文 + delta)` 恒等于 `末尾窗口(末尾窗口(累积正文) + delta)`
 *   —— 只要窗口本身已经包含了正文最后 max 个码点。
 *   （delta 比窗口长时，新窗口全来自 delta；delta 比窗口短时，
 *   新窗口 = 旧窗口的尾部 + delta，而旧窗口的尾部就是正文对应的尾部。）
 */
function rollTail(prevTail: string, delta: string): string {
  if (!delta) return prevTail
  return tailCodePoints(prevTail + delta, MAX_OVERLAP).join('')
}

/**
 * 把新一段文本接到已有文本后，消解两段的重叠。
 *
 * 与后端 mergeIncremental 同契约：
 *   - 误去重的代价是**丢掉真实内容**（用户永远发现不了）；
 *   - 漏去重的代价是多几个重复字（用户一眼看见）。
 *   所以所有阈值偏保守，宁可少裁。
 *
 * ⚠ 注意「相邻且只差一个字 ⇒ 判成切片重叠」是**已知且被接受的**行为：
 *   ASR 对同一段音频两次转写给出同音错字（「张伟 / 张薇」）时，
 *   这里会把后一段裁成「薇」。§132 查清了这个残片的根因是
 *   **人工段与 ASR 段同形、无法分辨**，并用 `origin` 字段解决；
 *   两段都是 ASR 时的这个残片是**有意保留**的取舍，不是待修缺陷。
 */
export function dedupeSegmentText(committed: string, next: string, minCoverage = ANCHOR_COVERAGE): string {
  return dedupeSegmentTextParts(committed, next, minCoverage).body
}

/**
 * LCS 对齐求 next 相对 committed 的净增后缀；不构成重叠时返回 null。
 *
 * 只看 committed 尾部与 next 头部的窗口，不做全文对齐：
 * 切片重叠必然出现在边界，在文中任意位置找重叠反而会误删
 * 「用户真的重复说的话」。
 */
function lcsNetNew(cs: string[], ns: string[], minCoverage = ANCHOR_COVERAGE): string | null {
  const limit = Math.min(MAX_OVERLAP, cs.length, ns.length)
  if (limit < 2) return null // 比 2 还短的对齐窗口无意义（单个字不能算重叠）
  const tail = cs.slice(cs.length - limit)
  const head = ns.slice(0, limit)

  const n = limit
  const dp: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(n + 1).fill(0))
  for (let i = 1; i <= n; i++) {
    for (let j = 1; j <= n; j++) {
      if (tail[i - 1] === head[j - 1]) dp[i][j] = dp[i - 1][j - 1] + 1
      else dp[i][j] = Math.max(dp[i - 1][j], dp[i][j - 1])
    }
  }
  const lcsLen = dp[n][n]
  if (lcsLen / n < minCoverage) return null

  // ★ 第二道闸：LCS 自身必须够长（与后端 minFuzzyAnchor 同值 4）。
  //
  // ⚠⚠ 本行此前被**删掉过**，理由写在旧注释里：「变异测试证明它冗余 ——
  //   只把 MIN_ANCHOR 放宽到 1 而 coverage 保持 0.6 时，15 条用例全绿」。
  // **那个结论是错的**，2026-10-11 用真函数双跑证伪（178 条按参数构造的语料）：
  // Go 基准 mergeIncremental 与本实现分叉 5 条，**全部**落在
  //   limit ∈ {3,4} 且 lcsLen ∈ {2,3} 这一格 ——
  //   即 coverage（lcsLen/limit ≥ 0.6）放行、而本闸会拦下的那一格。
  //   两道闸只在这一格不等价；15 条用例没覆盖到它，所以「全绿」只证明
  //   语料有洞，不证明闸冗余。
  //
  // 后果不是多几个重复字，而是**删掉用户没说过的话**：
  //   committed='甲云山' next='云甲山后续内容'
  //     Go 基准 → '甲云山云甲山后续内容'（判重叠不足，保留重复）
  //     本实现 → '甲云山云后续内容'（把重叠区里的「甲」「山」删了）
  // ⇒ 这与本文件头部「误去重的代价是丢掉真实内容，用户永远发现不了」直接冲突。
  if (lcsLen < MIN_ANCHOR) return null

  // 回溯标记 head 中被 LCS 选为重复的下标，其余是净增。
  // 单次回溯到底，保证 matched 集合自洽（LCS 可能有多种最优解）。
  const matched = new Array<boolean>(n + 1).fill(false)
  let i = n
  let j = n
  while (i > 0 && j > 0) {
    if (tail[i - 1] === head[j - 1]) {
      matched[j] = true
      i--
      j--
    } else if (dp[i - 1][j] >= dp[i][j - 1]) {
      i--
    } else {
      j--
    }
  }
  const out: string[] = []
  for (let k = 1; k <= n; k++) if (!matched[k]) out.push(head[k - 1])
  // 窗口外的 next 尾部一定是新内容。
  if (ns.length > limit) out.push(...ns.slice(limit))
  return out.join('')
}

/** 相邻两段的时间轴判据入参（毫秒）。缺失/非法都按「拿不到」处理。 */
export interface AdjacencyTiming {
  prevEndMs?: number
  nextStartMs?: number
}

/**
 * 两段在**时间轴上**是否重叠：下一段起点落在上一段结尾之前。
 *
 * ★ 为什么需要它：`dedupe('今天我们讨论了预算和排期', '预算和排期都要再确认一次')`
 *   在纯文本形态上和切片重叠**完全一样**（新段以旧段结尾开头），无从区分。
 *   时间戳能区分：正常切分两段之间有 silenceMs(1500ms) 静音间隙，
 *   而 RAF 节流导致 speechStartMs 回退时，下一段起点会落到上一段结尾**之前**。
 *
 * ⚠ 拿不到时间戳（NaN / undefined）时返回 **false**，即「不下结论」：
 *   调用方据此退回纯文本判据，而不是假装两段不重叠。
 */
export function likelyAudioOverlap(t: AdjacencyTiming): boolean {
  const { prevEndMs, nextStartMs } = t
  if (!Number.isFinite(prevEndMs) || !Number.isFinite(nextStartMs)) return false
  return (nextStartMs as number) < (prevEndMs as number)
}

/** 一步去重的结果。`dedupeSegments` 与 `renderTranscript` 共用它（§3）。 */
export interface DedupeStep {
  /** 本段去重后是否还剩可呈现的内容。 */
  added: boolean
  /** 本段应当呈现的正文（净增；零净增且确认重叠时为空串）。 */
  text: string
  /** 去重后的累积正文，供下一段对齐。 */
  body: string
  /**
   * §914：本次的**净增**（`body` 相对 `prev` 多出来的那部分）。
   *
   * ⚠ 为什么要有它：原来调用方靠 `body.slice(prev.length)` 取净增，
   *   而 `body` 是 ConsString 链 ⇒ `slice` 会**强制展平整条链** ⇒ O(n²)。
   * ⇒ 现在由本函数**直接算好**返回，调用方不用再 slice。
   */
  delta: string
}

/**
 * 走一步「本段相对上一段的去重」，**只算不渲染**。
 *
 * ⚠ 两个出口（`dedupeSegments` 给 LLM、`renderTranscript` 给界面）曾把这段
 *   「净增」逻辑各抄一遍，于是同一个缺陷能同时存在于两个出口。
 *   现在合并成这一个函数，meeting-dedup-asr.test.ts 钉住两者一致。
 *
 * ★ 「零净增段要不要丢」只在**时间轴确认重叠**时才丢：
 *   缺时间戳时若无条件丢，「李四单独说『可行』」这种真实短句会被整段吃掉
 *   —— 那是丢内容，不是去重。
 */
export function dedupeStep(input: {
  /** 已输出的累积正文。 */
  prev: string
  /**
   * §914：`prev` 的**末尾 ≤ MAX_OVERLAP 个码点**（字符串形态）。
   *
   * ⚠ 传了就用它做 LCS 窗口，**不再去碰 `prev` 本身** ——
   *   热路径上 `prev` 是一条正在增长的 ConsString 链，
   *   碰它（`Array.from` / `charCodeAt`）就会**展平整条链** ⇒ O(n²)。
   *   ⇒ 这一对参数（`prev` 全文 + `prevTail` 窗口）就是「比较用窗口、
   *   判全文用原文」这个分工的落地。
   */
  prevTail?: string
  /** 本段 ASR 原文。 */
  raw: string
  prevEndMs?: number
  startMs?: number
  /** 人工段不参与裁剪（§132），但仍是后续重叠的基准。 */
  manual?: boolean
}): DedupeStep {
  const { prev, raw, prevEndMs, startMs, manual = false } = input
  const n = raw.trim()
  if (!prev) return { added: !!n, text: n, body: n, delta: n }
  if (!n) return { added: false, text: '', body: prev, delta: '' }
  if (manual) return { added: true, text: n, body: prev + n, delta: n }

  const hasTiming = Number.isFinite(prevEndMs) && Number.isFinite(startMs)
  const overlapByTiming = hasTiming && likelyAudioOverlap({ prevEndMs, nextStartMs: startMs })
  // 拿不到时间戳时按「有重叠」处理：不去重的代价（重复照旧显示）远小于
  // 漏去重时用户看到的东西消失。★ 这条 `: true` 分支有专属靶子钉住。
  //
  // ⚠ §683 实测（2026-10-15，逐一核了三个生产调用点的段来源）：**这条分支在生产里走不到。**
  //   `hasTiming` 需要「前一段的 endMs 或本段的 startMs 缺失」，而实测每个生产者都带齐了：
  //   vad-segmenter.ts:246 / ingest-speech.ts:57 / meeting-utterance.ts:36（自己算出 endMs）
  //   / meeting-final-transcript.ts:403 / meetings-store.ts:321（DB 列 `end_ms INTEGER NOT NULL`
  //   见 native/schema.ts:333）。
  //   ⇒ 叠加 §634（会议链相邻段时间轴不重叠 ⇒ overlapByTiming 恒 false），
  //   **`dedupeSegmentText`（精确档）在生产里不可达，`dedupeSegmentTextSoft` 是唯一活路径。**
  //
  // ⇒ 所以本分支是**防御性代码，不是活路径**：留着是对的（将来若有链路漏了 endMs，
  // 应当按「保守=去重」而不是按「放行」），但**它的靶子钉的是一个生产上不存在的状态**。
  // ⇒ ⚠ 「所有生产者都带齐时间戳」这个不变量**当前没有任何判据钉住** —— 哪天新增一条链路
  //   漏了 endMs，这里会**静默**切到精确档，而不会有任何测试在生产形状上发现。登记为 #61。
  const useExact = hasTiming ? overlapByTiming : true
  // §914：窗口从 `prevTail` 取（热路径不再碰 `prev` 这条 ConsString 链），
  //   净增由 parts 版本**直接给出**（不再 `body.slice(prev.length)`）。
  const tail = input.prevTail !== undefined
    ? tailCodePoints(input.prevTail, MAX_OVERLAP)
    : tailCodePoints(prev, MAX_OVERLAP)
  const parts = useExact
    ? dedupeSegmentTextParts(prev, n, ANCHOR_COVERAGE, tail)
    : dedupeSegmentTextSoftParts(prev, n, ANCHOR_COVERAGE, tail)
  const { body, delta } = parts
  const added = delta ? true : !overlapByTiming
  return {
    added,
    // ★ 保留却没有净增时（缺时间轴 ⇒ 不下结论 ⇒ 不丢段），正文必须回退到**原文**。
    //   否则会渲染出空的 `[李四] ` —— 那既丢了内容，又让人以为该段是空的。
    text: added ? delta || n : '',
    body,
    delta,
  }
}

/**
 * 对一段会议转写做**相邻去重**，返回可安全用于展示的文本。
 *
 * 与 dedupeSegmentText 的区别：这里处理的是「已经排好序的全部段落」，
 * 用于生成 transcript 全文；相邻两段之间的重叠都要消解。
 *
 * ⚠️ 只消解**相邻**两段：更远的段落即便相似也是用户真的重复说了。
 */
export function dedupeTranscriptParagraphs(texts: string[]): string {
  if (texts.length <= 1) return texts.join('')
  let out = texts[0] ?? ''
  for (let k = 1; k < texts.length; k++) {
    out = dedupeSegmentText(out, texts[k] ?? '')
  }
  return out
}

/**
 * 返回**去重后**的 segments 副本，供 LLM 消费（摘要 / 推荐 / 待办）。
 *
 * ── 为什么需要它（2026-10-06）──
 *
 * 重复文本不只污染「转写全文」，还会顺着 segments 流进 LLM：
 * `useLiveSummary` 把 segments 直接 POST 给 `/summary` 与 `/recommend`，
 * 于是「今天今天下午三点」这种重复会出现在摘要、关键点、行动项里，
 * 还会让 topicShift 的主题漂移判断失准。
 * 用户看到的「质量不行」不只是转写那一行，是整条内容链路。
 *
 * 返回**新对象**而不是原地改：segments 是 ref 里的响应式数组，
 * 原地改会触发无谓的 watch / 重新渲染。
 *
 * `startMs` 保持不变——时间戳是真实采集的，不该被去重逻辑篡改。
 *
 * ⚠ `origin` 缺省按 **asr** 处理（享受去重）。反过来把未知当 manual
 *   会让去重**静默失效**，那比多裁一次危险得多。
 */
export function dedupeSegments<T extends {
  text: string
  startMs?: number
  endMs?: number
  origin?: 'asr' | 'manual'
}>(segments: T[]): T[] {
  if (segments.length <= 1) return segments.slice()
  const out: T[] = []
  let prevBody = ''
  // §914：累积正文的**末尾窗口**，增量维护（O(净增) 每段）。
  //   没有它，去重就得去读 `prevBody` —— 而那是一条越来越长的 ConsString 链，
  //   读它就意味着展平 ⇒ O(n²)。实测 8 小时会议 6.6 秒、24 小时 OOM。
  let prevTail = ''
  let prevEndMs: number | undefined
  for (const s of segments) {
    const step = dedupeStep({
      prev: prevBody,
      prevTail,
      raw: s.text ?? '',
      prevEndMs,
      startMs: s.startMs,
      manual: s.origin === 'manual',
    })
    if (step.added) out.push({ ...s, text: step.text })
    prevBody = step.body
    prevTail = rollTail(prevTail, step.delta)
    prevEndMs = Number.isFinite(s.endMs) ? s.endMs : undefined
  }
  return out
}

/**
 * 把段落列表渲染成 transcript 全文，**相邻段之间做重叠去重**。
 *
 * 为什么逐段保留 `[说话人]` 前缀、而不是先合并全文再拼前缀：
 * 去重要在**正文**层面做（重叠的音对应的是同一个说话人），但说话人标签
 * 必须留在它自己那一段上。所以逐段处理：对每段，与「已输出的累积正文」
 * 比较，裁掉重复部分，只追加净增。
 *
 * 只与**累积正文**比较是安全的：dedupeSegmentText 只看尾/头窗口，
 * 不会回头改写已输出的内容。
 *
 * ⚠ 零净增段不渲染成空行 —— 否则界面上会出现一条只有 `[A]` 的空条。
 *
 * ── §401：这里曾经**没有** `origin`，人工段因此被当成 ASR 重转写裁掉 ──
 *
 * 缺陷形态：`MeetingSegment.origin` 是**必填**字段，而 `dedupeSegments`
 * 认它、`renderTranscript` 的入参类型里**结构性没有它** ⇒ 豁免只在
 * 一部分出口生效。实测（§401.3）：
 *
 *   dedupeSegments   → ['张伟', '张伟下周三交对比表']      ← 完整
 *   renderTranscript → '[李四] 张伟\n[我] 下周三交对比表'    ← 裁掉了「张伟」
 *
 * ⭐ 要命的不对称：`MeetingDetailView.displaySegments`（界面分段列表）
 * 走的是 `dedupeSegments`，所以**界面上是完整的**；而被裁的是
 * `local_meetings.transcript` 派生列与 `api/meetings.ts` 的 `fallbackRefine`
 * prompt。⇒ 用户看得见的没问题，**喂给 LLM 的少了一截**，且那条
 * `fallbackRefine` 的注释自己写着「这条路径的产出质量直接决定用户看到什么」。
 *
 * 现在与 `dedupeSegments` **同一口径**（`origin` 缺省按 asr，见其注释）。
 * 判据见 `__tests__/render-transcript-manual-origin.test.mjs`。
 */
export function renderTranscript(
  segments: Array<{
    speakerLabel: string | null
    text: string
    startMs?: number
    endMs?: number
    /** §132：人工段享受豁免（§401 补齐——此前本函数完全不认这个字段）。 */
    origin?: 'asr' | 'manual'
  }>,
): string {
  const lines: string[] = []
  let prevBody = ''
  // §914：同上（`dedupeSegments` 的注释）—— 末尾窗口增量维护，
  //   不去读那条越来越长的 ConsString 链。
  let prevTail = ''
  let prevEndMs: number | undefined
  for (const s of segments) {
    const step = dedupeStep({
      prev: prevBody,
      prevTail,
      raw: s.text ?? '',
      prevEndMs,
      startMs: s.startMs,
      // ★ §401：与 dedupeSegments 同一豁免。缺省按 asr（去重）处理——
      //   反过来把未知当 manual 会让去重静默失效，那比多裁一次危险得多。
      manual: s.origin === 'manual',
    })
    if (step.added) {
      // ⚠ §427：说话人**必须有兜底**，否则 null/undefined 会原样拼进
      //   `[null] …` / `[undefined] …`，而这条函数有 5 个出口：
      //   ① `local_meetings.transcript` 派生列（**用户可见**）
      //   ② ③ api/meetings.ts 的 fallbackSummarize / fallbackRecommend 提示词
      //   ④ ⑤ fallbackRefine 提示词（§54 说它是**高频**路径）
      //
      //   null 是**真会出现的**：`buildRefineBaseSegments` 的第三条路径
      //   （采纳整段重转但说话人挂不回去）显式造 `speakerLabel: null`。
      //
      //   兜底词沿用本仓**另外两处**已经在用的那个，不新造：
      //   · Go `segmentsToText`：`if speaker == "" { speaker = "说话人" }`
      //   · `MeetingDetailView.onSummarize`（§427 已改成直接复用本函数）
      //   ⇒ 三个出口此前两个有、一个没有，这正是它开始漂的原因。
      // ⚠ 只对「空」兜底，**不顺手 trim 真名**：非空说话人必须原样输出。
      //   第一版写成 `String(...).trim() || '说话人'` ⇒ 把 ` 王总 ` 变成 `王总`，
      //   那是**超出缺陷范围**的行为改变（判据 §427 #3 把它抓了出来）。
      //   trim 只用来**判空**。
      const rawSpeaker = String(s.speakerLabel ?? '')
      const speaker = rawSpeaker.trim() ? rawSpeaker : '说话人'
      lines.push(`[${speaker}] ${step.text}`)
    }
    prevBody = step.body
    prevTail = rollTail(prevTail, step.delta)
    prevEndMs = Number.isFinite(s.endMs) ? s.endMs : undefined
  }
  return lines.join('\n')
}
