/**
 * email-inbox-adapter.ts — 收件箱分页接到 Hyper 连续加载内核的**适配层**。
 *
 * 为什么要单独成文件：页面里真正属于「迁移」的逻辑只有三处，且全都不是
 * Vue 相关——
 *   1. **页码 → offset**。内核是页码游标（`page` 从 1 起），本地库是 offset。
 *      换算必须是 `(page - 1) * pageSize`，**不能**用「已合并列表长度」——
 *      去重丢行会让它偏，表现是「一直转圈、后面几页刷不出来」
 *      （这个坑原先由 email-inbox-pagination.ts 手写了一遍，见其文件头第 1 条）。
 *   2. **hasMore 的来源**。本地查询没有 total，所以「还有没有更多」只能由
 *      「这页取满没有」推导。内核把它当**适配层的返回值**，正因如此不需要
 *      改核心就能让收件箱用上（07 §2.5 第 2 条）。
 *   3. **合并后的业务序**。内核不知道收件箱按 `date` 倒序——排序是领域知识，
 *      必须由这里给 `mergeRows`，否则等于强制「新页在前」，是错的。
 *
 * 抽成纯函数是为了能在 `node --test` 下穷举边界，不必起浏览器。
 */

/** 本地邮件的最小结构（本模块只用到 id/date，故不 import 业务 DTO）。 */
export interface InboxRow {
  id: string
  date: number
}

export interface InboxFetchDeps<Row extends InboxRow = InboxRow> {
  /** 读一页本地邮件。签名与 `readInboxPage(category, offset, folder)` 一致。 */
  readPage: (category: string, offset: number, folder: string) => Promise<Row[]>
  /** 当前筛选条件。在每次取页时**现读**，而不是建适配器时快照——筛选会变。 */
  getCategory: () => string
  getFolder: () => string
  /** 本地总量，仅用于 `total` 元信息（内核不拿它算 hasMore）。 */
  countAll?: () => Promise<number>
}

export interface InboxPageResult<Row extends InboxRow = InboxRow> {
  rows: Row[]
  total: number
  hasMore: boolean
}

/**
 * 适配器**实际用到的**入参。
 *
 * ⚠️ 刻意只声明 `page` / `pageSize` 两个必填项，而内核实际会多传
 * `revision` 与 `signal`。这不是偷懒：函数类型是**逆变**的——入参要求得越多，
 * 越难被赋给内核的 `fetchPage` 槽位（内核的类型里没有 `revision`）。
 * 声明得刚好够用，`(input) => …` 才能直接放进去。
 * `signal` 目前不用于取消：本地 SQLite 读很快，且内核在代次失配时会整段丢弃结果。
 */
export type ContinuousFetchInput = {
  page: number
  pageSize: number
}

/**
 * 生成内核的 `fetchPage`。
 *
 * ⚠️ offset 换算是这条适配器**唯一**容易写错的地方，改动时对照上面第 1 条。
 */
export function makeInboxFetchPage<Row extends InboxRow>(
  deps: InboxFetchDeps<Row>,
  pageSize: number,
): (input: ContinuousFetchInput) => Promise<InboxPageResult<Row>> {
  return async ({ page, pageSize: size }) => {
    const useSize = size || pageSize
    const rows = await deps.readPage(deps.getCategory(), (page - 1) * useSize, deps.getFolder())
    return {
      rows,
      total: deps.countAll ? await deps.countAll() : rows.length,
      // 「取满一页」= 可能还有更多。取不满就是最后一页。
      hasMore: rows.length >= useSize,
    }
  }
}

/**
 * 收件箱的合并序：整表按 `date` 倒序。
 *
 * ⚠️ 不能只是 `[...fresh, ...kept]`。新邮件插到顶部后，`kept` 里可能夹着
 * 比 `fresh` 更新的行（时区/延迟到达），不重排就会出现「新邮件排在旧邮件下面」。
 * 内核**刻意不替调用方决定顺序**（见 continuousList.ts 的 mergeRows 注释），
 * 所以这一步必须在这里做。
 *
 * 稳定排序：同 `date` 时保持传入次序（`Array.prototype.sort` 在现代引擎上稳定）。
 */
export function inboxMergeRows<Row extends InboxRow>(fresh: Row[], kept: Row[]): Row[] {
  return [...fresh, ...kept].sort((a, b) => (b.date ?? 0) - (a.date ?? 0))
}

/** 供页面复用的「已加载多少页」→ 「下一页的 offset」，与内核页码换算互为逆运算。 */
export function pageToOffset(page: number, pageSize: number): number {
  return Math.max(0, page - 1) * pageSize
}
