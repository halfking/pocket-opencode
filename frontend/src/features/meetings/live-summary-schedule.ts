/**
 * live-summary-schedule.ts — 「下一次 refresh 该在多久之后被调用」（§424）。
 *
 * ⚠ 为什么不放在 `useLiveSummary.ts` 里：同 `live-summary-gate.ts` 的理由——
 *   那一条 import 无扩展名，Node 的 ESM 解析器要求显式扩展名 ⇒ 任何
 *   `node --test` 都 import 不了它。提成无依赖的独立模块，判据才能跑真身。
 *
 * ## 它替掉的两个真实缺陷（都不是「优化」，是「兑现声明的意图」）
 *
 * ### ① 无 ceiling 的 debounce 会把即时总结**饿死**
 *
 * 原实现只有两行：
 *
 *     if (throttleTimer) clearTimeout(throttleTimer)
 *     throttleTimer = setTimeout(() => refresh(), 2000)
 *     watch(segments, () => scheduleRefresh(), { deep: true })
 *
 * 这是**纯 debounce**（尾沿、无上限）：每来一段就把定时器推后 2000ms。
 * ⇒ 只要分段到达间隔 **< 2000ms**，定时器在整场会议里**一次都不会到期**。
 *
 * 实跑（虚拟时钟逐字复刻那两行，10 分钟会议、refresh 耗时按 0 算即最有利假设）：
 *
 *     分段间隔 1000ms ⇒ 全场刷新 1 次（第 602000ms 才触发）
 *     分段间隔 1500ms ⇒ 全场刷新 1 次
 *     分段间隔 2000ms ⇒ 301 次      ← 恰好卡在边界
 *     分段间隔 3000ms ⇒ 201 次（≈ 每 3 秒一次）
 *
 * 用户需求「在录音时需要进行即时总结」在这种节奏下**整场不更新**，
 * 且没有任何报错。而 `useLiveSummary.ts` 的文件头写的是「节流更新」——
 * **声明是节流，实现是 debounce**。
 *
 * ### ⭐ ceiling 必须相对「距上次**成功**更新」算，不能相对「距上次分段变化」
 *
 * 这是本模块第一版的真错误，记在这里免得重犯：
 * `scheduleRefresh()` 是在**每次分段变化时被调用**的，所以那一刻
 * 「距上次分段变化」恒等于 0 ⇒ ceiling 分支永远为假 ⇒ 加了等于没加。
 *
 * 真正会随时间增长、且**不被分段变化重置**的量是「距上次成功更新」
 * （`lastUpdateAt`）—— 也正是 `shouldRefreshSummary` 里
 * `elapsedMs >= intervalMs` 那个分支用的同一个钟。
 *
 * ⇒ 两个钟各管一件事：
 *   `sinceSegmentMs`  喂 debounce（合并一批连续到达的分段）
 *   `sinceRefreshMs`  喂 ceiling （不许「一直有分段」把刷新无限推后）
 *
 * ceiling 取 `SUMMARY_INTERVAL_MS`：**不引入新的产品常量**——
 * 它本来就写着「距上次成功更新 ≥30s 必须更新」，
 * 是外层这个无上限的 debounce 把该分支变成了不可达代码。
 *
 * ### ② `refresh()` 没有在飞守卫 ⇒ 慢模型下并发叠加 + 旧轮覆盖新轮
 *
 * `isUpdating` 只被写、从不在入口读 ⇒ 任何两次 `refresh()` 可以同时在飞。
 * 触发条件不需要任何设备计时假设：`refresh 耗时 > 分段间隔` 即可。
 * 而本仓自己记录的常态就是慢模型（§35.7：网关 auto → deepseek-v4-flash，
 * 首字节 24~142s，agent 预算 30s）⇒ 分段间隔 2~5s 时**必然**重叠。
 *
 * 后果不是「多花点钱」，是**正确性**：`liveSummary.value = {...}` 是后写覆盖先写，
 * ⇒ 一个跑了 40 秒的旧轮可以盖掉一个 5 秒就完成的新轮，界面回退到更旧的摘要。
 *
 * 修法：入口加在飞守卫。**跳过不行、重排才行** ——
 * 丢掉这一次刷新是不可接受的：会议最后一段到达后可能再没有新分段，
 * 那一次刷新就永远丢了（尾段永远进不了摘要）。
 */

/** 静默期：最后一段变化后至少等这么久，让一批连续到达的分段合并成一轮。 */
export const LIVE_SUMMARY_DEBOUNCE_MS = 2000

export type RefreshDelayReason = 'in-flight' | 'ceiling' | 'debounce'

export interface RefreshDelay {
  delayMs: number
  reason: RefreshDelayReason
}

/**
 * @param sinceSegmentMs  距上一次**分段变化**的毫秒数（喂 debounce）
 * @param sinceRefreshMs  距上一次**成功更新**的毫秒数（喂 ceiling；首次是 `now - 0` ⇒ 必然很大）
 * @param inFlight        上一轮 refresh 是否还在飞
 */
export function nextRefreshDelay(input: {
  sinceSegmentMs: number
  sinceRefreshMs: number
  inFlight: boolean
  debounceMs?: number
  ceilingMs?: number
}): RefreshDelay {
  const {
    sinceSegmentMs,
    sinceRefreshMs,
    inFlight,
    debounceMs = LIVE_SUMMARY_DEBOUNCE_MS,
    ceilingMs,
  } = input
  // ceiling 缺省时由调用方传入（生产接 SUMMARY_INTERVAL_MS）。
  // 这里不兜一个默认值：**默认值必须由产品常量给**，模块自己编一个数
  // 就等于把「多久刷一次」藏进了实现里。
  const ceiling = ceilingMs ?? Number.POSITIVE_INFINITY

  // 在飞优先于一切：不重排的话，上一轮跑完时就没有下一次刷新了。
  // 用 debounceMs 轮询而不是 0 —— 在飞时若返回 0 就是忙等空转。
  if (inFlight) return { delayMs: debounceMs, reason: 'in-flight' }
  // ⚠ 判据是 sinceRefreshMs，不是 sinceSegmentMs。见文件头 ⭐ 那段。
  if (sinceRefreshMs >= ceiling) return { delayMs: 0, reason: 'ceiling' }
  const rest = debounceMs - sinceSegmentMs
  return { delayMs: rest > 0 ? rest : 0, reason: 'debounce' }
}
