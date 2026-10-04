/**
 * composables/useContinuousList.ts — ContinuousListController 的 Vue 适配层。
 *
 * 为什么需要这一层：`continuousList.ts` 刻意不依赖 Vue（便于 node --test 覆盖），
 * 但接进页面必须处理三件纯逻辑层管不了的事：
 *
 *  1. **响应式**。控制器是普通模块变量，模板读它不会更新。这里用 ref 承载
 *     rows/status/hasMore，由 `onRowsChange` / `onStatusChange` 灌入。
 *  2. **KeepAlive 生命周期**。页面被停用时必须 `pause()`（断开 observer、
 *     停止自动追加），重新激活时 `resume()` 并**重测 sentinel** ——
 *     初次渲染时 root 不溢出、之后变内滚，sentinel 位置会变。
 *  3. **滚动宿主**。`root` 必须是 sentinel 的祖先，错了 IntersectionObserver
 *     永远不触发或一直触发。默认取最近的 `overflow-y: auto/scroll` 祖先。
 *
 * 页面侧只需：`const { rows, status, sentinelRef, onFilterChange } = useContinuousList({...})`。
 */

import {
  computed,
  getCurrentInstance,
  onActivated,
  onBeforeUnmount,
  onDeactivated,
  onMounted,
  onUnmounted,
  ref,
  shallowRef,
  watch,
  type Ref,
} from 'vue'
import {
  createContinuousList,
  type ContinuousListController,
  type ListRow,
  type ListStatus,
  type RefreshStatus,
  type RowsMeta,
} from '../lib/shell/continuousList.ts'

export interface UseContinuousListOptions<Row extends ListRow> {
  /** 取一页。`page` 从 1 起；offset 型后端请自行换算 `(page-1)*pageSize`。 */
  fetchPage: (input: { page: number; pageSize: number; signal: AbortSignal }) => Promise<{
    rows: Row[]
    total: number
    hasMore: boolean
  }>
  pageSize?: number
  /** 初始立即加载第一页。默认 true。 */
  immediate?: boolean
  /** 显式指定滚动宿主。默认自动向上找可滚动祖先。 */
  scrollRoot?: Ref<HTMLElement | null>
  /** 停在第一页不 autoFill 时是否继续补屏。默认 false（保守）。 */
  autoFill?: boolean
  /**
   * 刷新语义。省略即 `'replace'`（与 continuousList 引入前逐字节一致）。
   * `'merge'` 时刷新按**行**并入顶部并保留已加载行——收件箱要的就是它。
   */
  refreshPolicy?: 'replace' | 'merge'
  /**
   * `merge` 下决定「刷新页 + 保留行」的最终顺序。默认新的在前。
   * 收件箱按 `date` 倒序，**排序是领域知识** ⇒ 必须由调用方给，
   * 不设等于强制「新页在前」，对需要全局重排的列表是错的。
   */
  mergeRows?: (fresh: Row[], kept: Row[]) => Row[]
}

export interface UseContinuousListReturn<Row extends ListRow> {
  rows: Ref<Row[]>
  total: Ref<number>
  status: Ref<ListStatus>
  refreshStatus: Ref<RefreshStatus>
  hasMore: Ref<boolean>
  needsManualLoad: Ref<boolean>
  loadedPages: Ref<number>
  /** 当前查询代次。调试与「旧响应不回写」的可观测性用。 */
  revision: Ref<number>
  /** 绑到列表底部的哨兵元素上。 */
  sentinelRef: Ref<HTMLElement | null>
  /** 加载失败/无更多时的显式重试入口。 */
  retry: () => void
  /** 筛选/月份/项目/排序变化时调它（内部提升代次并回到首页）。 */
  resetQuery: () => void
  /** 换账号/权限变化时调它。 */
  invalidateScope: () => void
  /** 手动加载下一页（按钮）。 */
  loadMore: () => Promise<void>
  /** 供外部（例如刷新手势）复用同一条刷新路径。 */
  refresh: () => Promise<void>
  /** 组件卸载时 disconnect。手动调用一般没必要。 */
  disconnect: () => void
}

export function useContinuousList<Row extends ListRow>(
  opts: UseContinuousListOptions<Row>,
): UseContinuousListReturn<Row> {
  const rows = ref([]) as Ref<Row[]>
  const total = ref(0)
  const status = ref<ListStatus>('idle')
  const refreshStatus = ref<RefreshStatus>('idle')
  const hasMore = ref(true)
  const needsManualLoad = ref(false)
  const loadedPages = ref(0)
  const sentinelRef = shallowRef<HTMLElement | null>(null)
  const scrollRootRef = shallowRef<HTMLElement | null>(null)
  const revision = ref(0)

  const controller: ContinuousListController<Row> = createContinuousList<Row>({
    fetchPage: opts.fetchPage,
    ...(opts.pageSize ? { pageSize: opts.pageSize } : {}),
    ...(opts.refreshPolicy ? { refreshPolicy: opts.refreshPolicy } : {}),
    ...(opts.mergeRows ? { mergeRows: opts.mergeRows } : {}),
  })

  controller.onRowsChange((next: readonly Row[], meta: RowsMeta) => {
    rows.value = next as Row[]
    total.value = meta.total
    hasMore.value = controller.hasMore
    loadedPages.value = meta.loadedPages
    revision.value = meta.revision
  })

  controller.onStatusChange((s, r) => {
    status.value = s
    refreshStatus.value = r
    needsManualLoad.value = controller.needsManualLoad
  })

  /** 找到 sentinel 最近的、真正可纵向滚动的祖先。 */
  function resolveScrollRoot(): HTMLElement | null {
    if (opts.scrollRoot) return opts.scrollRoot.value
    if (scrollRootRef.value) return scrollRootRef.value
    const el = sentinelRef.value
    let cur: HTMLElement | null = el?.parentElement ?? null
    while (cur) {
      const style = getComputedStyle(cur)
      const oy = style.overflowY
      if ((oy === 'auto' || oy === 'scroll') && cur.scrollHeight > cur.clientHeight) return cur
      cur = cur.parentElement
    }
    return el?.parentElement ?? null
  }

  function connect() {
    if (!sentinelRef.value) return
    const root = resolveScrollRoot()
    scrollRootRef.value = root
    controller.connectSentinel(sentinelRef.value, root)
  }

  /**
   * 哨兵出现/消失时重连。
   *
   * ⚠️ 2026-10-06：`connect()` 原本只在 `onMounted` / `onActivated` 调一次。
   *   而三个已迁页面的哨兵都写成 `v-if="list.length > 0"` —— **首屏一行都没有时
   *   哨兵根本不渲染**，`sentinelRef.value` 是 null，于是 observer **一次都没连上过**，
   *   滚动到底永远不加载下一页。单元测试测不到：composable 的测试直接注入元素，
   *   不经过「v-if 先 false、拿到数据后才 true」这个时序。
   *   是在迁移 `EmailInboxView` 时被 `email-continuous-list` e2e 撞出来的
   *   （`MeetingListView` / `NoteListView` 有同一个缺陷，只是当时没人测它们）。
   *
   *   `flush: 'post'` 是必须的：DOM 更新**之后**才能量 scrollHeight，
   *   否则量的是上一帧的旧布局。
   */
  watch(
    sentinelRef,
    (el) => {
      if (el) connect()
      else controller.connectSentinel(null, null)
    },
    { flush: 'post' },
  )

  // 自动补屏：只有显式开启且宿主的 scrollHeight 确实填不满视口时才补。
  // 默认关闭是因为「填不满」是布局事实，拿不到就宁可短屏也不要请求风暴。
  function maybeAutoFill(root: HTMLElement | null) {
    if (!opts.autoFill || !root) return
    const viewport = root.clientHeight
    if (root.scrollHeight >= viewport) return
    controller.shouldStopAutoFill = false
    void controller.autoFill().finally(() => {
      controller.shouldStopAutoFill = true
    })
  }

  onMounted(() => {
    connect()
    if (opts.immediate !== false) controller.resetQuery()
  })

  // KeepAlive：停用时暂停（保留缓存与 query），激活时重连并重测 sentinel。
  onActivated(() => {
    controller.resume()
    connect()
    maybeAutoFill(scrollRootRef.value)
  })

  onDeactivated(() => {
    controller.pause()
  })

  onUnmounted(() => {
    controller.disconnect()
  })

  onBeforeUnmount(() => {
    controller.disconnect()
  })

  // HMR / 非组件上下文使用时的兜底，避免 observer 泄漏。
  if (!getCurrentInstance()) controller.disconnect()

  return {
    rows,
    total,
    status,
    refreshStatus,
    hasMore,
    needsManualLoad,
    loadedPages,
    revision,
    sentinelRef,
    retry: () => controller.retry(),
    resetQuery: () => controller.resetQuery(),
    invalidateScope: () => controller.invalidateScope(),
    loadMore: () => controller.loadMore(),
    refresh: () => controller.refresh(),
    disconnect: () => controller.disconnect(),
  }
}
