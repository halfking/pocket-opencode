/**
 * meeting-related-refresh.ts — 相关推荐刷新的**并发闸**（§926）。
 *
 * ── 缺陷本体 ──
 * `MeetingDetailView` 的相关推荐刷新是**每段触发**的：
 * `watch(() => displaySegments.value.length, …)` ⇒ 段数一变就重置 2.5 s 定时器
 * ⇒ 定时器到期就 `void refreshRelated()`。
 *
 * 而 `refreshRelated()` 里 `await searchRelatedContext(...)` 是 `Promise.all` 三路
 * （笔记检索 / 本地会议 / **维基百科**）⇒ 一次刷新要等最慢的那一路。
 * ⚠⚠ **「定时器到期就发」不检查上一次是否还在途** ⇒
 *   段到达得比一次刷新更快时（密集发言段间隔可以到几秒），
 *   **每一次段到达都会再叠一个在途刷新** ⇒
 *   在途的第三方请求数**没有上界**，而每次刷新至少 1 次外网请求（实测 1.89 次/触发）。
 *
 * ⭐ 改造后的语义（**内容不变，只变并发**）：
 *   · 至多**一个**在途刷新；
 *   · 在途期间到达的段**合并成至多一次补跑**（补跑时读的是**最新**的分段，
 *     所以内容与「每次都跑」一致，只是中间轮次被省掉）；
 *   · `await` 的调用方等到「面板已追上」才返回（⇒ 追加/改字/收尾那几处
 *     `await refreshRelated()` 的时序语义不变）。
 *
 * ⚠⚠ **刻意不改的语义**：`refreshRelated()` 仍然是**整体覆盖** `noteRecs`
 * （不是合并）—— 那是 §411 那道门钉着的既有契约
 * （`meeting-agent-recommendations-restore.test.mjs`：
 * 「改成合并会让上一轮的检索结果永久累积」）。
 * ⇒ 这里合并的是**请求的并发**，不是**结果的合并**。
 *
 * ⚠ 为什么提成独立模块：`.vue` 里的逻辑在 Node 里测不到
 * （要拖进 vue/编译链）⇒ 与 `live-summary-gate.ts` 同一手法：
 * **决策搬进零依赖模块，判据跑真身**。
 */
export class RelatedRefreshGate {
  private inFlight = false
  /** 在途期间又来了新段 ⇒ 需要补跑一次。 */
  private pending = false
  /**
   * **最新**一次请求带来的任务。
   *
   * ⚠⚠ 补跑必须跑**这个**，不是「在途那次的」——
   *   合并掉的中间轮次之所以能合并，正是因为**后来的那次更新**：
   *   它带来的才是最新数据。跑旧的那次 ⇒ 面板停在旧内容上。
   */
  private latestTask: (() => Promise<void>) | null = null
  /** 在途期间到达的调用方：等「面板追上」之后才放行。 */
  private waiters: Array<() => void> = []

  /**
   * 排一次刷新。
   *
   * @param task 真正干活的函数（**每次调用都重新读最新数据** ⇒ 补跑拿到的就是最新内容）
   * @returns 在途期间到达 ⇒ 等到「面板追上最新」之后才 resolve
   */
  async run(task: () => Promise<void>): Promise<void> {
    this.latestTask = task
    if (this.inFlight) {
      // P2：在途 ⇒ 不再叠一个请求，只记下「还要再来一次」。
      this.pending = true
      await new Promise<void>((resolve) => this.waiters.push(resolve))
      return
    }
    this.inFlight = true
    try {
      // P1：至多一个在途；P2：期间来的合并成至多一次补跑。
      do {
        this.pending = false
        const latest = this.latestTask
        if (latest) await latest()
      } while (this.pending)
    } finally {
      // P4：一次失败不得把闸门卡死（否则后面所有刷新都会被当成「在途」而永久合并掉）。
      this.inFlight = false
      const waiters = this.waiters
      this.waiters = []
      for (const resolve of waiters) resolve()
    }
  }

  /** 当前是否有刷新在途（判据/调试用）。 */
  get busy(): boolean {
    return this.inFlight
  }
}
