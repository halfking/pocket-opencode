/**
 * meeting-summary-merge.ts — 滚动摘要**跨轮**时的合并决策（纯函数）。
 *
 * ── 这里修的是哪一个缺陷（§402）─────────────────────────────────────
 *
 * `useLiveSummary.ts` 的滚动摘要每 30 秒 / 每 3 段跑一轮，每轮的结果
 * **整体覆盖** `liveSummary`（`liveSummary.value = toLiveSummary(result)`），
 * 并且立刻写进 DB（`updateMeeting(id, { liveSummary, summary })`）。
 *
 * `references`（智能体检索到的用户自己的笔记）就在这个整体覆盖的范围里。
 * 而服务端提示词明写：
 *
 *   「**引用必须来自工具返回的内容**：工具没返回的笔记不许写进 references，
 *     **宁可没有 references 也不要编造笔记标题**。」
 *
 * ⇒ 某一轮转写里没有值得回溯的话题时，返回空 references 是**被提示词
 *   明确鼓励的预期行为**，不是异常。
 *
 * 实测后果（`__probe402.mjs`）：
 *
 *   第 1 轮 agent 引用 2 条 → 第 2 轮返回空
 *   ⇒ `MeetingInsightPanel` 的 `v-if="references.length"` 整块静默消失
 *   ⇒ 降级提示也不出现（判据是 `!references.length && agent !== true`，
 *     而第 2 轮是一次**成功**的 agent 摘要）
 *   ⇒ 且已写进 DB，刷新页面也不会回来
 *
 * ⭐ 这就是 §223 那条教训的跨字段版本：**「没拿到」与「没有」是同一个观测**。
 *   recommendations 早已被同一条教训修过（§223 的 `recommendFallback`），
 *   references 漏了。
 *
 * ★ 同一个函数里已有先例，本修复只是把它补齐到 references：
 *   `useLiveSummary.ts:76` 对推荐写着「⚠ 空结果**不覆盖**已有推荐
 *   （一轮抖动不该抹掉此前的好结果）」—— 同一原则，同一文件，
 *   此前只落在 `recommendations` 一个字段上。
 */
import type { SummaryReference } from './meetings-store'

/**
 * 合并新一轮与上一轮的参考资料。
 *
 * 语义（与 `recommendations` 的处理逐字同构）：
 *   · 新一轮**非空** ⇒ 用新一轮（话题变了，旧的引用已不相关）
 *   · 新一轮**空**   ⇒ 保留上一轮已有的（一轮抖动不该抹掉此前的好结果）
 *
 * ⚠ `prev` 为空/缺省时返回 `[]` 而不是 `undefined`：
 *   界面判据是 `v-if="references.length"`，两者等价；
 *   统一成数组可让下游不必再判 undefined。
 *
 * ⚠ 本函数**不做跨轮累积去重**：新一轮非空就整体替换。
 *   「参考资料是否应当跨轮累积」是产品口径（待拍板），这里不替用户拍；
 *   本函数只保证「**空**不会抹掉已有」这一条无争议的性质。
 *
 * ⚠ 本模块**刻意不导出**「这一轮是换了还是留住了」的判定函数。
 *   写过一版 `resolveReferenceMerge`，被 `check:dead-features` 当场拦下
 *   （仅测试引用 = 死代码）—— 那是对的：界面此刻并不需要这句话，
 *   §30 定的原则是「宁可少说一句，不可说错一句」。
 *   真要显示时再接，届时它才有生产消费方。
 */
export function mergeSummaryReferences(
  prev: SummaryReference[] | undefined | null,
  next: SummaryReference[] | undefined | null,
): SummaryReference[] {
  if (Array.isArray(next) && next.length > 0) return next
  return Array.isArray(prev) ? prev : []
}