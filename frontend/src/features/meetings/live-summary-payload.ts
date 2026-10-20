/**
 * live-summary-payload.ts — §924：滚动摘要「**这一轮送出去什么**」的两个决策（纯函数）。
 *
 * ── 为什么需要它（这是本节修的那个缺陷）──
 *
 * 服务端 `buildSummaryPrompt`（backend/internal/server/server_meeting.go）拼的是
 * **「已有摘要 + 新增转写」**：
 *
 *     "已有摘要：\n%s\n\n新增转写：\n%s\n\n请更新摘要。…"
 *
 * 也就是说，**请求体里的 `segments` 在契约上就是「这一轮新增的那部分」**。
 * 而前端 `useLiveSummary.refresh()` 每 30 秒把
 * `dedupeSegments(segments.value)`（**整场会议**）当「新增转写」发过去。
 *
 * ⇒ 后果有三条：
 *   ① 请求体与服务端提示词都 **O(会议时长)**，而滚动摘要每 30 秒发一次
 *      ⇒ 一场会的**累计**流量是 **O(时长²)**；
 *   ② 服务端那条**一次性 chat** 路径（`llmMeetingSummary`）**不截断**转写
 *      ⇒ 提示词随会议线性变长 ⇒ 45 秒预算（`summaryBudget`）迟早被撑爆
 *      ⇒ 回落浏览器内 `/api/llm/chat`，而那条路有 32000 **字节**闸
 *      ⇒ §916 的修法是「裁尾」⇒ 长会议上摘要**只剩最后那 37 分钟**，且**不报错**；
 *   ③ 「新增转写」这个标签本身是**假的** —— 模型每轮都被告知「这是新内容」。
 *
 * ⇒ 修法：**只送真的新增段**。服务端契约本来就这么写 ⇒ **服务端零改动**。
 *
 * ── 同一轮的第二个请求（推荐）──
 *
 * `/api/meetings/{id}/recommend` 服务端把转写截到 `maxRunes = 2000` 个**码元**
 * （`server_meeting.go`），而它自己的注释写明了意图：
 *
 *     「把预算花在**最新的若干段**上就够了，summary 已经承载了全局信息。」
 *
 * ⇒ 前端送整场会议，服务端只留尾部 ⇒ 中间那些段**白传了一遍**（每轮一次）。
 * ⇒ 本模块第二个函数按「**够服务端截断用**」的量送尾部 ⇒
 *   服务端截出来的提示词**逐字不变**，而请求体变成常数大小。
 *
 * ⚠⚠ 为什么提成独立模块（这是本节能写行为门的前提）：
 *   `useLiveSummary.ts` 的 import **无扩展名**（`'../api/meetings'`）
 *   ⇒ Node 的 ESM 解析器要求显式扩展名 ⇒ 任何 `node --test` 都 import 不了它
 *   ⇒ 决策内联在 composable 里 ⇒ 它一行行为门都没有
 *   ⇒ 与 §418 把 `shouldRefreshSummary` 提出来、§52 把 `planRefine` 提出来
 *     是**同一个理由**（§418 那道门的前言原话：「决策内联 ⇒ 无行为断言 ⇒ 改坏无声」）。
 */

/** 段最少要有的身份字段。 */
export interface SummarySegmentLike {
  /**
   * 段的稳定 id。
   *
   * ⚠ 为什么按 **id** 而不是按下标切增量（这是本模块最容易做错的一处）：
   *   `segments` 这条 ref **会被整份替换**，不只是追加 ——
   *   `recordingRuntime.ts` 的 `seedSegments` 是 `this.segments.value = [...stored]`
   *   （从库里把整场会议灌回来），`freshSession` 那条是 `this.segments.value = []`。
   *   ⇒ 按下标切（`slice(lastCount)`）在「整份替换」之后会**切到新数组的中间**，
   *     于是新会议的前半段被**静默跳过** ⇒ 摘要永远少一半内容。
   *   ⇒ id 是稳定的（`MeetingSegment.id`），与数组位置无关。
   */
  id: string
}

/** 推荐链要用的段：除了身份，还要有正文（用来量尾部）。 */
export interface RecommendSegmentLike extends SummarySegmentLike {
  text: string
}

/**
 * 推荐链送出的**最少正文码元数**。
 *
 * ⚠ 服务端 `maxRunes = 2000` 是按**渲染后**的转写算的，而渲染会**多出**
 *   每段一行 `[说话人] ` 前缀 ⇒ 渲染长度 ≥ 正文长度。
 * ⇒ 这里取 **2 倍余量**：客户端送的正文 ≥ 4000 码元 ⇒ 渲染 ≥ 4000
 *   ⇒ 服务端取最后 2000 个渲染码元时，**取到的一定是同一段内容**
 *   ⇒ 服务端拼出来的提示词**逐字不变**（与送整场会议时一致），
 *   而服务端把上限调大到 4000 时客户端仍然覆盖得到。
 */
export const RECOMMEND_MIN_TEXT_RUNES = 4000

export interface SummarySelection<T> {
  /** 本轮要送出去的段。 */
  segments: T[]
  /**
   * 本轮成功后应记下的「已并入摘要」的段 id（去重、保序）。
   *
   * ⚠ 语义是「**此刻数组里有的**都算已并入」，而不是「并集」：
   *   若数组被整份替换（`seedSegments`），新数组里的 id 都不在旧集合里
   *   ⇒ 它们会被当作新增段重发 ⇒ 宁可重算一遍，也不要少算。
   */
  sentIds: string[]
  /**
   * 本轮走的是「增量」还是「全量」。
   *
   * ⚠ 「没有新增段」时仍然送全量：强制刷新（换场/手动）必须能出摘要，
   *   而此时**内容没变**，重算的收益是「换个措辞」，成本是一次 LLM 调用。
   *   这个分支在正常录音里很少走到（30 秒一轮、每轮约 2 段新增）。
   */
  mode: 'delta' | 'all'
}

/**
 * 选出「摘要这一轮要送的那几段」（真增量）。
 *
 * @param cleanSegments 已去重的全量段（`dedupeSegments` 的产物）
 * @param sentIds       已经并入当前摘要的段 id
 */
export function selectSummarySegments<T extends SummarySegmentLike>(
  cleanSegments: T[],
  sentIds: Iterable<string>,
): SummarySelection<T> {
  const sent = new Set(sentIds)
  const fresh = cleanSegments.filter((s) => !sent.has(s.id))

  const nextSentIds: string[] = []
  const seen = new Set<string>()
  for (const s of cleanSegments) {
    if (seen.has(s.id)) continue
    seen.add(s.id)
    nextSentIds.push(s.id)
  }

  if (fresh.length === 0) {
    return { segments: cleanSegments, sentIds: nextSentIds, mode: 'all' }
  }
  return { segments: fresh, sentIds: nextSentIds, mode: 'delta' }
}

/**
 * 选出「推荐这一轮要送的那几段」（**有界的尾部**）。
 *
 * ⚠ 与摘要那条**刻意不同**：推荐不能送增量。
 *   增量的体量是「这一轮新说了多少」（正常一轮约 2 段），
 *   而服务端要的是**末尾 2000 个渲染码元**的上下文
 *   ⇒ 送增量等于把模型的上下文从 2000 码元砍到 200 码元 ⇒ **行为变更**。
 *   ⇒ 这里送「够服务端截断用的尾部」⇒ 服务端提示词**逐字不变**。
 *
 * @param cleanSegments 已去重的全量段
 * @param minTextRunes  至少要覆盖多少正文码元（默认留 2 倍余量）
 */
export function selectRecommendSegments<T extends RecommendSegmentLike>(
  cleanSegments: T[],
  minTextRunes: number = RECOMMEND_MIN_TEXT_RUNES,
): T[] {
  if (cleanSegments.length === 0) return cleanSegments
  let runes = 0
  let cut = cleanSegments.length
  for (let i = cleanSegments.length - 1; i >= 0; i--) {
    runes += countRunes(cleanSegments[i].text)
    cut = i
    if (runes >= minTextRunes) break
  }
  return cleanSegments.slice(cut)
}

/**
 * 码元数（不是 UTF-16 长度，也不是字节数）。
 *
 * ⚠ 与服务端 `[]rune(transcript)` 的口径必须一致 ⇒ 代理对**只算一个**。
 *   （`meeting-dedup.ts` 的 `tailCodePoints` 是同款逻辑，两处各自一份，
 *   因为一个在 Go 侧一个在 TS 侧，跨语言没法共用。）
 */
function countRunes(s: string): number {
  let n = 0
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i)
    // 低代理 ⇒ 它属于一个代理对 ⇒ 跳过（高代理那一轮已经算过）
    if (c >= 0xdc00 && c <= 0xdfff && i > 0) {
      const hi = s.charCodeAt(i - 1)
      if (hi >= 0xd800 && hi <= 0xdbff) continue
    }
    n++
  }
  return n
}
