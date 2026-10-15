/**
 * live-summary-gate.ts — 「这一轮滚动摘要要不要跑」这条决策（§418）。
 *
 * ⚠ 为什么不放在 `useLiveSummary.ts` 里：**那一条 import 无扩展名**，
 *   Node 的 ESM 解析器要求显式扩展名 ⇒ 任何 `node --test` 都 import 不了它
 *   （与 `flashcardIo.test.ts` 被登记进 test-coverage 豁免表同一个原因）。
 *   提成无依赖的独立模块，判据才能**跑真身**而不是跑副本。
 *
 * ⚠ 提出来的另一半原因是它此前**一行行为门都没有**：三个条件、两个常量都在，
 *   但「哪个条件单独就能触发」从来没被验证过 ——
 *   与 §52 在 `planRefine` 上修掉的是同一个形状（决策内联 ⇒ 无行为断言 ⇒ 改坏无声）。
 *
 * ⭐ 真实规则是**四选一的或**，不是「每 30 秒一次」：
 *
 *     force（换场/强制）|| 主题切换 || 新增 ≥3 段 || 距上次 ≥30s
 *
 *   这条事实有三处用户可见的后果，所以值得单独立判据：
 *   ① 密集讨论时实际节奏**快于** 30 秒（仓库里曾有两处把它写成单一的「每 30 秒」）；
 *   ② **每轮都跟着跑一次 `/recommend` 的 LLM 调用** ⇒ 调用次数由**分段到达速度**
 *      决定，不由那个常量决定；
 *   ③ 「主题切换时立刻刷新」是产品特性 —— 把它误改成纯节流会悄悄砍掉它。
 */

/** 距上次成功更新超过这么久就更新（毫秒）。⚠ 是**下限之一**，不是节流周期。 */
export const SUMMARY_INTERVAL_MS = 30_000
/** 新增这么多段就更新一次（不必等满上面那个下限）。 */
export const SUMMARY_SEGMENT_THRESHOLD = 3

/**
 * @param newCount   本次新增的分段数（`segments.length - lastSegmentCount`）
 * @param elapsedMs  距上次成功更新的毫秒数；首轮是 `now - 0` ⇒ 必然很大
 * @returns true = 这一轮该跑
 */
export function shouldRefreshSummary(input: {
  force?: boolean
  topicChanged?: boolean
  newCount: number
  elapsedMs: number
  segmentThreshold?: number
  intervalMs?: number
}): boolean {
  const {
    force = false,
    topicChanged = false,
    newCount,
    elapsedMs,
    segmentThreshold = SUMMARY_SEGMENT_THRESHOLD,
    intervalMs = SUMMARY_INTERVAL_MS,
  } = input
  return Boolean(force || topicChanged || newCount >= segmentThreshold || elapsedMs >= intervalMs)
}