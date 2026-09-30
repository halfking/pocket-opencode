/**
 * 收件箱分页的**纯状态机**（2026-10-01 需求：分页展示 / 下拉加载最新 / 上滑加载下一页）。
 *
 * 单独成文件的原因：分页的真正难点不在「发请求」，而在**游标怎么走**。这部分
 * 是纯计算（给定已取行数与已合并结果，算出下一个 offset 与是否还有更多），
 * 抽出��就能在 node --test 下穷举各种边界，不必起本地数据库。
 *
 * 三个真实存在的坑（本文件就是为修它们而写）：
 *
 * 1. **游标不能用已合并列表长度。**
 *    分页结果要与已有列表按 id 去重后追加。若列表里混进了本次之前不存在的行
 *    （下拉同步来了新邮件、或客户端去重丢弃了重复行），`emails.length` 与
 *    「已向数据库索取过的行数」就不再相等。此时若用 `emails.length` 当 offset，
 *    下一批会**重复索取**已看过的区间，用户表现为「一直转圈、后面几页刷不出来」。
 *    所以游标只按「数据库实际返回过的原始行数」前进。
 *
 * 2. **有更多 ≠ 还能推进。**
 *    新邮件插入到列表顶部会让 offset 语义整体平移，可能连续取回「全都已在列表里」
 *    的页。此时必须仍然推进 offset（否则死循环），但连续多页无新增就要收敛停止，
 *    否则用户在底部无限等待。
 *
 * 3. **下拉刷新不能把已加载的分页丢掉。**
 *    刷新只应把「最新一页」合并进列表顶部；已翻开的页必须保留，否则用户下拉一下
 *    就被弹回第 1 页、滚动位置作废。
 *
 * 本模块**不含任何删除语义**：刷新与翻页都只做读取与合并，绝不触碰邮件数据。
 */

/** 收件箱每页条数。与 DEFAULT_LIST_PAGE_SIZE 保持一致。 */
export const INBOX_PAGE_SIZE = 30

/** 连续多少页「取回的全是已有行」后判定为无更多，防止无限空转。 */
const MAX_NO_PROGRESS_PAGES = 3

export interface InboxPageState {
  /**
   * 下一批要向数据库索取的 OFFSET。
   *
   * 只随「数据库已返回过的原始行数」递增，与合并去重后的列表长度**无关**。
   */
  nextOffset: number
  /** 是否还有下一页可取。 */
  hasMore: boolean
  /** 正在取下一页（防重入）。 */
  loadingMore: boolean
  /** 连续无新增的页数，用于收敛停止。 */
  noProgressStreak: number
}

export function createInboxPageState(): InboxPageState {
  return { nextOffset: 0, hasMore: true, loadingMore: false, noProgressStreak: 0 }
}

/**
 * 重置游标（切换分类、首次加载）。
 *
 * 注意：这里**不清空**已合并的列表——是否清空由调用方决定，因为下拉刷新场景
 * 需要「重置游标但保留已加载的分页」。
 */
export function resetInboxPage(state: InboxPageState): InboxPageState {
  return { nextOffset: 0, hasMore: true, loadingMore: false, noProgressStreak: 0 }
}

/**
 * 一批数据取回后的新状态。
 *
 * @param fetchedCount 本批从数据库返回的**原始行数**（去重前）。
 * @param addedCount   本批实际新增进列表的行数（去重后）。
 * @param pageSize     每页条数，用于判断「这页是满的」= 可能还有更多。
 */
export function advanceInboxPage(
  state: InboxPageState,
  fetchedCount: number,
  addedCount: number,
  pageSize: number = INBOX_PAGE_SIZE,
): InboxPageState {
  // 数据库没再吐出任何行：到底了。
  if (fetchedCount <= 0) {
    return { ...state, hasMore: false, loadingMore: false, noProgressStreak: 0 }
  }

  // 游标只按「已索取的原始行数」前进——绝不能用合并后的列表长度。
  const nextOffset = state.nextOffset + fetchedCount

  // 收据：本批是否带来了新行。取回的都是已有行说明 offset 语义已平移，
  // 仍继续推进（否则死循环），但连续多页无新增就判定到底。
  const noProgressStreak = addedCount > 0 ? 0 : state.noProgressStreak + 1

  const hasMore =
    fetchedCount >= pageSize && noProgressStreak < MAX_NO_PROGRESS_PAGES

  return { ...state, nextOffset, hasMore, loadingMore: false, noProgressStreak }
}

/** 合并一批新页：按 id 去重，按收到时间倒序，不重复也不丢。 */
export function mergeInboxPages<T extends { id: string; date: number }>(
  existing: T[],
  incoming: T[],
): T[] {
  const seen = new Set(existing.map((e) => e.id))
  const added = incoming.filter((e) => e.id && !seen.has(e.id))
  if (!added.length) return existing
  return [...existing, ...added].sort((a, b) => (b.date ?? 0) - (a.date ?? 0))
}

/**
 * 下拉刷新：只把「最新一页」并到列表顶部，**保留已加载的分页**。
 *
 * 返回合并后的完整列表与是否真的有新邮件（用于提示「已是最新」）。
 * 不修改传入的 state，也不做任何删除。
 */
export function applyRefreshPage<T extends { id: string; date: number }>(
  existing: T[],
  freshPage: T[],
): { list: T[]; addedCount: number } {
  const existingIds = new Set(existing.map((e) => e.id))
  const topNew = freshPage.filter((e) => e.id && !existingIds.has(e.id))
  if (!topNew.length) return { list: existing, addedCount: 0 }
  return { list: mergeInboxPages([], [...topNew, ...existing]), addedCount: topNew.length }
}

/**
 * 哨兵是否应该继续触发加载。
 *
 * 追加完一页后，哨兵可能**仍在视口内**且没有产生新的 IntersectionObserver
 * 交叉事件（元素没离开过视口就不会再回调）。此时不手动补一次，列表就会停在
 * 那一页——这是无限滚动最常见的「只加载一页就停」故障。
 */
export function shouldAutoLoadMore(params: {
  hasMore: boolean
  loadingMore: boolean
  sentinelTop: number
  viewportHeight: number
  rootMargin?: number
}): boolean {
  const { hasMore, loadingMore, sentinelTop, viewportHeight, rootMargin = 120 } = params
  if (loadingMore || !hasMore) return false
  return sentinelTop <= viewportHeight + rootMargin
}
