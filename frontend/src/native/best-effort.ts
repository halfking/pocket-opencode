/**
 * best-effort 后台任务：失败只记日志，**绝不让它变成未处理拒绝**，也绝不阻断调用方。
 *
 * ── 为什么需要它（2026-10-17 登记）────────────────────────────────────
 * 录音收尾那段有一处 `void getMeeting(id).then((m) => …)` **没有 `.catch`**，
 * 而同一文件另两处同类写法都写了 `void x().catch(() => {})` —— 惯例存在，
 * 那一行漏了。`getMeeting` 会 `await localDB.queryOne(...)`，DB 异常时**能** reject。
 * 影响有界（上一行已写入 `status:'completed'`），但会在控制台留下未处理拒绝日志，
 * 且没有任何门能发现它退化。
 *
 * ── 为什么不直接 inline 写 `.catch(() => {})` ────────────────────────
 * inline 写法只能被「文件里有没有 catch」这种**静态**判据覆盖，而那类判据是
 * 恒真风险最高的（仓库专门有 `check:vacuous-guard` 盯着）。抽成可注入依赖的
 * 纯函数后，**行为**本身可测：work 抛错 ⇒ 返回的 promise 必须 resolve 而不是
 * reject。这样撤掉 `.catch` 就会让门真的转红（见 `__tests__/best-effort.test.ts`）。
 *
 * ⚠ 它**不是**「吞掉一切」的许可：work 里的同步 throw 也会被转成一次 warn 日志，
 *   调用方若需要感知失败，就不该用本助手。
 */

/** 执行一次 best-effort 后台任务；返回的 promise **永远 resolve**。 */
export function runBestEffort(label: string, work: () => unknown): Promise<void> {
  let p: unknown
  try {
    p = work()
  } catch (e) {
    // work 同步抛错：同样不得冒泡成未处理拒绝。
    console.warn(`[${label}] best-effort 失败（同步抛出），继续:`, e)
    return Promise.resolve()
  }
  // 用 `then(ok, err)` 而不是 `.then(ok).catch(err)`：后者在 `p: unknown` 时
  // 推成 `Promise<unknown>`，与本函数的 `Promise<void>` 契约不符（vue-tsc TS2322）。
  return Promise.resolve(p).then(
    () => undefined,
    (e: unknown) => { console.warn(`[${label}] best-effort 失败（异步拒绝），继续:`, e) },
  )
}
