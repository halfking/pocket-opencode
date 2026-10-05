/**
 * shell/continuousList.ts — 连续加载控制器 + 下拉刷新状态机。
 *
 * 为什么不用现有的 `useListSentinel` 就够了：那个只做「露出就拉下一页」，
 * 没有代次、没有去重、没有失败态、没有耗尽态、没有短页补屏上限。
 * 后果是筛选变了旧响应仍回写、同页重复请求叠加、短列表反复触发观察、
 * 失败后无限重试。UI规范 07 §3 把这些列成了明确契约。
 *
 * 三个必须成立的正确性约束：
 *
 *  1. **刷新与追加互斥。** 刷新提升 queryRevision、取消旧请求、清游标、
 *     从首页重载；此后**任何**旧 revision 的响应一律丢弃。漏了这一条，
 *     「下拉刷新后又被上一页的慢响应覆盖」就会复现。
 *  2. **失败保留已加载行。** 请求失败停自动重试、显示显式重试，
 *     绝不能把已有数据清空再报错。
 *  3. **筛选/账号变化清缓存。** 不在新账号展示旧行。
 *
 * 布局模式（cards/table）与加载方式（continuous/paged）是**两个独立维度**，
 * 本模块只管后者。
 *
 * 纯逻辑 + 注入式依赖（fetch / observer / viewport），无 Vue 依赖，可单测。
 */

import { createHyperPages, type HyperPages } from './hyperPages.ts'

/** 控制器状态。`exhausted` 与 `failed` 都停止自动请求。 */
export type ListStatus = 'idle' | 'loadingNext' | 'refreshing' | 'failed' | 'exhausted' | 'paused'

/** 下拉刷新状态机。 */
export type RefreshStatus = 'idle' | 'pulling' | 'armed' | 'refreshing' | 'settling' | 'error'

/**
 * 刷新语义（2026-10-06 新增）。
 *
 * - `'replace'`（**默认**）：刷新 = 回到快照。`pages.reset()` 后只留第 1 页。
 *   会议/笔记列表要的就是这个——服务端是权威快照，旧行没出现在新快照里
 *   就说明它已被删除。
 * - `'merge'`：刷新 = 把第 1 页的新值**并到顶部并保留已翻开的页**。
 *   收件箱要的是这个——新邮件插到列表顶部时，用户如果正停在第 5 页，
 *   `replace` 会把他弹回顶部，等于下拉一次就丢掉阅读进度。
 *
 * ⚠️ `merge` **不删行**。服务端删掉的行会继续留在已加载页里，直到用户换筛选
 * （`resetQuery`）或换账号（`invalidateScope`）才清。这不是缺陷而是取舍：
 * 合并语义的前提就是「只增不减」，`features/email/email-inbox-pagination.ts`
 * 的 `applyRefreshPage` 同样明确「本模块不含任何删除语义」。
 * 需要删除语义的列表应当用 `'replace'`。
 *
 * ⚠️ 改成别的默认值会让已接线的 `MeetingListView` / `NoteListView` 行为改变。
 * 门禁 `continuousList.test.mjs` 的「默认策略是 replace」一条专门钉住它。
 */
export type RefreshPolicy = 'replace' | 'merge'

/**
 * 一行的最小契约。
 *
 * ⚠️ 这里**故意**只要求 `id`，不要求 `[key: string]: unknown` 索引签名。
 * 早期版本加了索引签名，结果 `LocalMeeting` 这类正常 interface 都无法
 * 作为 Row 传入——去重与滚动锚点只需要一个稳定 id，索引签名纯属多余约束。
 */
export interface ListRow {
  /** 稳定行 id。去重与滚动锚点都靠它，不能用数组下标。 */
  id: string
}

export interface ContinuousListDeps<Row extends ListRow> {
  /**
   * 取一页。必须接受 revision；实现方在 revision 落后时**自己也要能拒绝**
   * （服务端乱序返回时 controller 的检查已经太晚）。
   */
  fetchPage: (input: {
    page: number
    pageSize: number
    revision: number
    signal: AbortSignal
  }) => Promise<{ rows: Row[]; total: number; hasMore: boolean }>
  pageSize?: number
  /** 注入时钟，便于测代次。 */
  now?: () => number
  /** 内存预算阈值（初始设计值）。 */
  virtualizationThreshold?: number
  windowCacheThreshold?: number
  /** 单轮自动填屏最多补几页（设计值）。 */
  maxAutoFillPages?: number
  /**
   * 刷新语义。**省略即 `'replace'`**，与本选项引入前的行为逐字节一致。
   * 见 RefreshPolicy 的说明。
   */
  refreshPolicy?: RefreshPolicy
  /**
   * `merge` 策略下，决定「刷新页 + 保留下来的旧行」的最终顺序。
   *
   * 默认 `[...fresh, ...kept]`（新的在前）。**核心不知道业务序**——
   * 例如收件箱要按 `date` 倒序，而邮件列表的排序规则是领域知识，
   * 由调用方给（`mergeRows: (fresh, kept) => sortByDate([...fresh, ...kept])`）。
   * 不设就等于强制「新页在前」，对需要全局重排的列表是错的。
   */
  mergeRows?: (fresh: Row[], kept: Row[]) => Row[]
}

/** 观察器工厂：没有 IntersectionObserver 时返回 null，控制器降级为按钮。 */
export interface SentinelObserver {
  observe(el: Element): void
  unobserve?(el: Element): void
  disconnect(): void
}

export interface ContinuousListController<Row extends ListRow> {
  readonly status: ListStatus
  readonly refreshStatus: RefreshStatus
  readonly rows: readonly Row[]
  readonly total: number
  readonly revision: number
  /** 已加载页数。 */
  readonly loadedPages: number
  /** 是否还有下一页（耗尽后为 false，且不再观察）。 */
  readonly hasMore: boolean
  /** 无 observer 时 UI 应显示「继续加载」按钮。 */
  readonly needsManualLoad: boolean
  /** 已加载行数达到虚拟化评估阈值。 */
  readonly shouldVirtualize: boolean

  onStatusChange(cb: (s: ListStatus, r: RefreshStatus) => void): void
  onRowsChange(cb: (rows: readonly Row[], meta: RowsMeta) => void): void

  /** 筛选/月/项目/排序变化：提升代次、清游标、回到首页。 */
  resetQuery(): void
  /** 换账号/权限变化：清缓存并重载。 */
  invalidateScope(): void

  /** 连接 sentinel。返回 false 表示没有可用 observer。 */
  connectSentinel(el: Element | null, root: Element | null): boolean
  /** KeepAlive 停用 / 被覆盖层盖住时暂停，保留 query 与缓存。 */
  pause(): void
  /** 回到页面重新连接。 */
  resume(): void
  disconnect(): void

  /** 显式加载下一页（按钮 / 滚动兜底）。 */
  loadMore(): Promise<void>
  /** 刷新第一页。与追加互斥。 */
  refresh(): Promise<void>
  /** 请求失败后的显式重试。 */
  retry(): void
  /**
   * 首屏不足一屏时补屏，最多 maxAutoFillPages 轮。
   *
   * 「视口是否已填满」只能由真实布局判断（列表高度 vs 容器高度），
   * 所以调用方在挂载后测量并覆写 shouldStopAutoFill。默认 true = 立即停止，
   * 即**宁可不补屏也不无限补**：连续 append 触发的请求风暴比短屏更难排查。
   */
  autoFill(): Promise<void>
  /** 真实视口已填满 → autoFill 停止。默认 true。 */
  shouldStopAutoFill: boolean

  /** 供刷新手势调用。 */
  beginPull(): void
  updatePull(offset: number, threshold: number): void
  endPull(): void
  cancelPull(): void
}

export interface RowsMeta {
  total: number
  loadedPages: number
  revision: number
  /** 总计可能漂移（数据在变），把取值时刻带上让 UI 能标更新时间。 */
  measuredAt: number
}

const DEFAULT_PAGE_SIZE = 20
const DEFAULT_AUTOFILL_ROUNDS = 3
const DEFAULT_VIRTUALIZE = 1000
const DEFAULT_WINDOW_CACHE = 3000

export function createContinuousList<Row extends ListRow>(
  deps: ContinuousListDeps<Row>,
): ContinuousListController<Row> {
  const pageSize = deps.pageSize ?? DEFAULT_PAGE_SIZE
  const now = deps.now ?? (() => Date.now())
  const maxAutoFillPages = deps.maxAutoFillPages ?? DEFAULT_AUTOFILL_ROUNDS
  const virtualizationThreshold = deps.virtualizationThreshold ?? DEFAULT_VIRTUALIZE
  const windowCacheThreshold = deps.windowCacheThreshold ?? DEFAULT_WINDOW_CACHE
  const refreshPolicy: RefreshPolicy = deps.refreshPolicy ?? 'replace'
  const mergeRows: NonNullable<ContinuousListDeps<Row>['mergeRows']> =
    deps.mergeRows ?? ((fresh, kept) => [...fresh, ...kept])

  const pages: HyperPages<Row> = createHyperPages<Row>()

  let status: ListStatus = 'idle'
  let refreshStatus: RefreshStatus = 'idle'
  let revision = 0
  let nextPage = 1
  let total = 0
  let hasMore = true
  let needsManualLoad = false
  let observer: SentinelObserver | null = null
  let sentinelEl: Element | null = null
  let rootEl: Element | null = null
  let active = false
  /** 单飞：同一页的重复请求/重试只发一次。 */
  let inFlight: Map<number, Promise<void>> = new Map()
  let abort = new AbortController()
  /** 最近一次失败的操作种类，供 retry() 决定重放刷新还是继续追加。 */
  let failedKind: 'refresh' | 'append' | null = null

  const statusCbs: Array<(s: ListStatus, r: RefreshStatus) => void> = []
  const rowsCbs: Array<(rows: readonly Row[], meta: RowsMeta) => void> = []

  function setStatus(next: ListStatus) {
    if (status === next) return
    status = next
    for (const cb of statusCbs) cb(status, refreshStatus)
  }

  function setRefreshStatus(next: RefreshStatus) {
    if (refreshStatus === next) return
    refreshStatus = next
    for (const cb of statusCbs) cb(status, refreshStatus)
  }

  function emitRows() {
    const rows = pages.all()
    const meta: RowsMeta = {
      total,
      loadedPages: nextPage - 1,
      revision,
      measuredAt: now(),
    }
    for (const cb of rowsCbs) cb(rows, meta)
  }

  /**
   * 唯一的写回入口。任何 rows 变更都必须经过它。
   *
   * `reqRevision` 与当前 revision 不一致时**整体丢弃**——这就是
   * 「旧响应不能回写」的执行点。
   */
  function commitPage(
    page: number,
    reqRevision: number,
    res: { rows: Row[]; total: number; hasMore: boolean },
    preserveCursor = false,
    rowsOverride?: Row[],
  ) {
    if (reqRevision !== revision) return false
    pages.replace(page, rowsOverride ?? res.rows)
    total = res.total
    hasMore = res.hasMore
    if (!preserveCursor) nextPage = page + 1
    if (!hasMore) {
      setStatus('exhausted')
      disconnectObserver()
    }
    emitRows()
    return true
  }

  function loadPage(page: number, kind: 'append' | 'refresh'): Promise<void> {
    const reqRevision = revision
    const existing = inFlight.get(page)
    if (existing) return existing
    if (kind === 'append' && (status === 'refreshing' || status === 'paused')) {
      return Promise.resolve()
    }
    if (kind === 'append' && (!hasMore || status === 'exhausted' || status === 'failed')) {
      return Promise.resolve()
    }
    if (kind === 'refresh') {
      setStatus('refreshing')
    } else {
      setStatus('loadingNext')
    }
    const p = deps
      .fetchPage({ page, pageSize, revision: reqRevision, signal: abort.signal })
      .then((res) => {
        // ⚠️ 代次检查必须在**任何**写操作之前。放在 pages.reset() 之后就晚了：
        // 一条过期响应会把当前数据清空却不写入任何内容——比"晚到覆盖"更糟，
        // 它让用户看到列表突然空了。
        if (reqRevision !== revision) return
        let preserveCursor = false
        let rowsOverride: Row[] | undefined
        if (kind === 'refresh') {
          if (refreshPolicy === 'replace') {
            // 首页**成功**才替换数据并重置已加载页。
            pages.reset()
            nextPage = 1
          } else if (pages.count() > 0) {
            // merge：按**行**合并，不是按页替换。
            //
            // ⚠️ 曾先写成 pages.replace(1, fresh) —— 页级做法会把「从第 1 页
            // 滚下去、这次没出现在新第 1 页里」的行**整段丢掉**（收件箱一次
            // 到 2 封新邮件就会发生）。`applyRefreshPage` 的契约是
            // `fresh + 旧行中不在 fresh 里的`，所以这里必须显式算。
            //
            // ⚠️ `pages.count() > 0` 这个条件不能省：首屏（还没有任何已加载页）
            // 走 merge 时若也保留游标，nextPage 会停在 1，之后 loadMore 永远
            // 重复请求第 1 页，表现为「下拉刷新后翻不了页」。
            const fresh = res.rows
            const freshIds = new Set(fresh.map((r) => r.id))
            const kept = pages.all().filter((r) => !freshIds.has(r.id))
            rowsOverride = mergeRows(fresh, kept)
            pages.reset()
            pages.replace(1, rowsOverride)
            preserveCursor = true
          }
        }
        failedKind = null
        commitPage(page, reqRevision, res, preserveCursor, rowsOverride)
        if (status === 'refreshing' || status === 'loadingNext') setStatus('idle')
      })
      .catch(() => {
        // 失败保留已加载行；停自动重试，交给显式 retry()。
        failedKind = kind
        setStatus('failed')
      })
      .finally(() => {
        inFlight.delete(page)
      })
    inFlight.set(page, p)
    return p
  }

  function disconnectObserver() {
    observer?.disconnect()
    observer = null
  }

  function buildObserver(): SentinelObserver | null {
    if (typeof IntersectionObserver === 'undefined') {
      needsManualLoad = true
      return null
    }
    needsManualLoad = false
    return new IntersectionObserver(
      (entries) => {
        if (!active) return
        // sentinel 从顶部滚出视口不能误认成接近底部。
        if (!entries.some((e) => e.isIntersecting)) return
        if (!hasMore || status === 'loadingNext' || status === 'refreshing' || status === 'exhausted') return
        void loadPage(nextPage, 'append')
      },
      {
        // root 必须是 sentinel 的祖先；rootMargin 240px 是初始设计值。
        root: rootEl,
        rootMargin: '0px 0px 240px 0px',
      },
    )
  }

  const controller: ContinuousListController<Row> = {
    get status() {
      return status
    },
    get refreshStatus() {
      return refreshStatus
    },
    get rows() {
      return pages.all()
    },
    get total() {
      return total
    },
    get revision() {
      return revision
    },
    get loadedPages() {
      return nextPage - 1
    },
    get hasMore() {
      return hasMore
    },
    get needsManualLoad() {
      return needsManualLoad
    },
    get shouldVirtualize() {
      return pages.count() >= virtualizationThreshold
    },

    onStatusChange(cb) {
      statusCbs.push(cb)
    },
    onRowsChange(cb) {
      rowsCbs.push(cb)
    },

    // 宁可不补屏，也不要在没量到真实视口时无限 append。
    shouldStopAutoFill: true,

    resetQuery() {
      // 提升代次 → 旧响应全部失效；取消在飞行中的请求；清游标回到首页。
      revision += 1
      abort.abort()
      abort = new AbortController()
      inFlight = new Map()
      pages.reset()
      nextPage = 1
      hasMore = true
      total = 0
      setStatus('idle')
      void loadPage(1, 'refresh')
    },

    invalidateScope() {
      // 换账号/权限：缓存与代次一起作废，不在新账号展示旧行。
      pages.reset()
      revision += 1
      abort.abort()
      abort = new AbortController()
      inFlight = new Map()
      nextPage = 1
      hasMore = true
      total = 0
      setStatus('idle')
      void loadPage(1, 'refresh')
    },

    connectSentinel(el, root) {
      sentinelEl = el
      rootEl = root
      disconnectObserver()
      observer = buildObserver()
      if (observer && el) observer.observe(el)
      if (observer && pages.count() > 0) {
        // 重连后重测 sentinel：初次 root 不溢出、变内滚后位置会变。
        if (el) observer.observe(el)
      }
      return observer !== null
    },

    pause() {
      active = false
      setStatus('paused')
      disconnectObserver()
    },

    resume() {
      active = true
      if (status === 'paused') setStatus('idle')
      // 回到页面重新连 root，保留 query 与缓存。
      if (sentinelEl) {
        disconnectObserver()
        observer = buildObserver()
        if (observer) observer.observe(sentinelEl)
      }
    },

    disconnect() {
      active = false
      disconnectObserver()
      inFlight = new Map()
    },

    loadMore() {
      if (!hasMore) return Promise.resolve()
      return loadPage(nextPage, 'append')
    },

    refresh() {
      // 刷新与追加互斥：提升代次、取消在飞行中的请求、丢弃它们的单飞记录。
      //
      // ⚠️ 这里**不能**先 pages.reset()：刷新失败时必须保留旧内容与筛选
      // （UI规范 07 §2「失败保留旧内容及筛选，提示重试与更新时间，不把旧行
      // 瞬间清空后报错」）。真正的清空发生在第 1 页成功那一刻（见 loadPage）。
      revision += 1
      abort.abort()
      abort = new AbortController()
      inFlight = new Map()
      setRefreshStatus('refreshing')
      // 记住「刚才是一次刷新」，失败后 retry() 才知道该重试第 1 页
      // 而不是继续往下追加（否则会在旧数据上再叠一层）。
      failedKind = null
      return loadPage(1, 'refresh').finally(() => {
        setRefreshStatus('idle')
      })
    },

    retry() {
      // 优先重放失败的那次刷新；否则按当前游标追加。
      if (failedKind === 'refresh') {
        failedKind = null
        void loadPage(1, 'refresh')
        return
      }
      const page = pages.count() === 0 ? 1 : nextPage
      void loadPage(page, pages.count() === 0 ? 'refresh' : 'append')
    },

    async autoFill() {
      // 首屏不足一屏 → 顺序补页；单轮最多 maxAutoFillPages 轮。
      for (let round = 0; round < maxAutoFillPages; round += 1) {
        if (!hasMore || status === 'failed' || status === 'exhausted' || !active) return
        await loadPage(nextPage, 'append')
        if (!hasMore) return
        if (controller.shouldVirtualize || pages.count() >= windowCacheThreshold) return
        // 没有更多可补：真实视口已填满（由调用方通过 canScrollMore 覆盖判断）。
        if (controller.shouldStopAutoFill) return
      }
    },

    // ---- 下拉刷新手势 ----
    beginPull() {
      if (refreshStatus !== 'idle') return
      setRefreshStatus('pulling')
    },
    updatePull(offset, threshold) {
      if (refreshStatus === 'pulling' && offset >= threshold) setRefreshStatus('armed')
      if (refreshStatus === 'armed' && offset < threshold) setRefreshStatus('pulling')
    },
    endPull() {
      if (refreshStatus === 'armed') {
        setRefreshStatus('refreshing')
        void controller.refresh().finally(() => setRefreshStatus('idle'))
      } else {
        // 未达阈值：只回弹，不请求。
        setRefreshStatus('settling')
        setRefreshStatus('idle')
      }
    },
    cancelPull() {
      if (refreshStatus === 'pulling' || refreshStatus === 'armed') setRefreshStatus('idle')
    },
  } as ContinuousListController<Row>

  return controller
}
