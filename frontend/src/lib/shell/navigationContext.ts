/**
 * shell/navigationContext.ts — 导航上下文（v2 schema）。
 *
 * 职责：记录「谁打开了谁」以及每个条目的视图快照。**不拥有路由**——
 * Vue Router 才是路由真源，浏览器 History 是页面时间轴，本模块只保存
 * 打开意图与视图快照（UI规范 06 §3–§4）。
 *
 * 几条容易写错、因此在实现里显式成立的规则：
 *
 *  1. **不碰浏览器真实历史。** entries 超过上限时淘汰内存记录，
 *     但绝不调用 history.back/forward 去「配合」淘汰。
 *  2. **cursor 只在成功提交时移动。** 守卫阻止/用户取消/路由失败 → 记
 *     `cancelled`/`failed`，cursor 与 DOM 都不动。
 *  3. **持久化只留白名单 query。** 搜索原文、token、秘密不落 sessionStorage。
 *  4. **读回来的数据一律当作不可信输入校验。** sessionStorage 可被同源
 *     脚本改写、也可能损坏；schema 不过就直接丢弃，不「尽力修复」。
 *  5. **scope 隔离。** 换账号/换服务器整体作废，绝不跨账号恢复标题与筛选。
 *
 * 纯数据模块，无 Vue 依赖，便于 node --test 直接覆盖。
 */

import { shallowRef } from 'vue'
import {
  LEGACY_STORAGE_KEY,
  MAX_OPERATIONS,
  MAX_PAGE_ENTRIES,
  MAX_TITLE_LENGTH,
  PERSISTED_QUERY_WHITELIST,
  STORAGE_KEY,
  type NavigationContext,
  type NavigationEntry,
  type NavigationKind,
  type NavigationOperation,
  type NavigationScope,
  type NavigationView,
  type Presentation,
  type ScrollAnchor,
  type TitleSource,
} from './types.ts'

/** 可注入的 id / 时钟，让单测不依赖随机数与真实时间。 */
export interface Clock {
  now(): number
  nextId(): string
}

let idCounter = 0
const defaultClock: Clock = {
  now: () => Date.now(),
  nextId: () => {
    idCounter += 1
    return `e${idCounter.toString(36)}`
  },
}

function emptyView(): NavigationView {
  return { scroll: {} }
}

function makeContext(): NavigationContext {
  return { version: 2, entries: [], cursor: -1, overlayIds: [], operations: [] }
}

export class NavigationContextStore {
  private ctx: NavigationContext = makeContext()
  private clock: Clock
  private seq = 0
  private storage: Storage | null
  /**
   * 变更信号。
   *
   * ⚠️ 这个 store 的其余状态是**普通模块变量**，不是 Vue ref。于是
   * `computed(() => store.snapshot())` 一次求值后就被永久缓存——它没有
   * 任何响应式依赖，路由变了标题也不会变。所以需要一个可被 Vue 追踪的
   * 计数器：每次变更自增，让依赖它的 computed 重新求值。
   *
   * 用 `shallowRef` 而非普通 `{value}` 对象：后者 Vue 同样追踪不到。
   * 这里确实引入了 Vue 依赖（其余模块刻意保持无 Vue 依赖），因为
   * 「纯数据」与「能被 computed 追踪」在 Vue 3 里只能二选一，
   * 而这条信号的价值正是在接线层（本仓的顶栏标题就依赖它）。
   */
  private readonly _version = shallowRef(0)

  /** 只读变更计数。放进 computed 里即可让它随上下文变化重算。 */
  get version(): number {
    return this._version.value
  }

  /** 触碰变更信号。放在**所有**会改 ctx 的路径上。 */
  private touch(): void {
    this._version.value += 1
  }

  constructor(opts: { clock?: Clock; storage?: Storage | null } = {}) {
    this.clock = opts.clock ?? defaultClock
    // storage 不可用（隐私模式 / 禁用 cookie）时降级为纯内存，
    // 导航仍然工作，只是刷新后不恢复。
    this.storage =
      opts.storage === undefined ? safeSessionStorage() : opts.storage
  }

  /** 当前上下文的只读快照。 */
  snapshot(): NavigationContext {
    return structuredClone(this.ctx)
  }

  /** cursor 指向的当前页面条目；没有则 undefined。 */
  current(): NavigationEntry | undefined {
    if (this.ctx.cursor < 0) return undefined
    return this.ctx.entries[this.ctx.cursor]
  }

  /** 按 id 取条目。 */
  get(id: string): NavigationEntry | undefined {
    return this.ctx.entries.find((e) => e.id === id)
  }

  /** 当前 cursor 之后是否还有可前进的页面。覆盖层存在时前进必须禁用。 */
  canForward(): boolean {
    return this.ctx.cursor < this.ctx.entries.length - 1
  }

  /** 前进目标。调用方仍需复核目的地权限。 */
  forwardTarget(): NavigationEntry | undefined {
    if (this.ctx.entries[this.ctx.cursor + 1]?.presentation !== 'page') return undefined
    return this.ctx.entries[this.ctx.cursor + 1]
  }

  /**
   * 打开一个条目。
   *
   * push     —— 新的前进分支，cursor 移到末尾，中间的 forward 分支被截断；
   * replace  —— 替换当前条目，保留条目 id（同一页换筛选/月份用）；
   * deepLink —— 冷启动或外部直入，开一个新栈。
   */
  open(input: {
    fullPath: string
    presentation: Presentation
    openedBy: NavigationKind
    title?: string
    titleSource?: TitleSource
    titleKey?: string
    routeName?: string
    scope: NavigationScope
    parentId?: string
  }): NavigationEntry {
    const parentId = input.parentId ?? (input.presentation === 'page' ? this.current()?.id : undefined)
    const entry: NavigationEntry = {
      id: this.clock.nextId(),
      parentId,
      fullPath: input.fullPath,
      routeName: input.routeName,
      presentation: input.presentation,
      openedBy: input.openedBy,
      title: clampTitle(input.title ?? ''),
      titleSource: input.titleSource ?? 'registered',
      titleKey: input.titleKey,
      scope: input.scope,
      view: emptyView(),
      createdAt: this.clock.now(),
    }

    if (input.openedBy === 'push') {
      // 返回后打开新详情：截断前进分支，旧的 forward 不可达。
      this.ctx.entries.length = this.ctx.cursor + 1
      this.ctx.entries.push(entry)
      this.ctx.cursor = this.ctx.entries.length - 1
      this.rememberCursorFromHistory()
    } else if (input.openedBy === 'replace') {
      // 保留条目 id：同页换月份/排序/内容 Tab 不该产生可回退的新条目。
      if (this.ctx.cursor >= 0) {
        const prev = this.ctx.entries[this.ctx.cursor]
        this.ctx.entries[this.ctx.cursor] = { ...entry, id: prev.id, createdAt: prev.createdAt }
      } else {
        this.ctx.entries.push(entry)
        this.ctx.cursor = this.ctx.entries.length - 1
        this.rememberCursorFromHistory()
      }
    } else {
      // deepLink / restore / present：冷启动或覆盖层，直接追加。
      this.ctx.entries.push(entry)
      if (this.presentationOfCursor() === 'page') {
        this.ctx.cursor = this.ctx.entries.length - 1
        this.rememberCursorFromHistory()
      }
    }

    this.evict()
    this.record(entry.id, input.openedBy, 'committed')
    return this.current() ?? entry
  }

  /** 打开一个覆盖层（modal / sheet / focus）。不推进 cursor。 */
  openOverlay(input: {
    fullPath: string
    presentation: Exclude<Presentation, 'page'>
    title?: string
    titleKey?: string
    scope: NavigationScope
    parentId?: string
  }): NavigationEntry {
    // 父条目默认是**最上层覆盖层**而不是 current()：覆盖层不推进 cursor，
    // 嵌套弹窗若挂到页面下，继承链会跳过上一层弹窗的标题
    // （UI规范 06 §2 明确要求「包括前一层弹窗标题」）。
    const parentId = input.parentId ?? this.topOverlay()?.id ?? this.current()?.id
    const entry: NavigationEntry = {
      id: this.clock.nextId(),
      parentId,
      fullPath: input.fullPath,
      presentation: input.presentation,
      openedBy: 'present',
      // 无标题覆盖层沿用它打开前的有效标题，**不是**「弹窗」也不是空。
      title: clampTitle(input.title ?? this.inheritedTitle(parentId)),
      titleSource: input.title ? 'registered' : 'inherited',
      titleKey: input.titleKey,
      inheritedFrom: input.title ? undefined : parentId,
      scope: input.scope,
      view: emptyView(),
      createdAt: this.clock.now(),
    }
    this.ctx.entries.push(entry)
    this.ctx.overlayIds.push(entry.id)
    this.evict()
    this.record(entry.id, 'present', 'committed')
    return entry
  }

  /** 关闭一个覆盖层条目（回内存、移出 overlayIds）。页面 cursor 不动。 */
  closeOverlay(id: string): boolean {
    const idx = this.ctx.overlayIds.indexOf(id)
    if (idx < 0) return false
    this.ctx.overlayIds.splice(idx, 1)
    const entryIdx = this.ctx.entries.findIndex((e) => e.id === id)
    if (entryIdx >= 0) this.ctx.entries.splice(entryIdx, 1)
    this.record(id, 'dismiss', 'committed')
    return true
  }

  /** 最高层覆盖层；没有则 undefined。 */
  topOverlay(): NavigationEntry | undefined {
    for (let i = this.ctx.overlayIds.length - 1; i >= 0; i -= 1) {
      const e = this.get(this.ctx.overlayIds[i])
      if (e) return e
    }
    return undefined
  }

  /** 是否存在覆盖层。返回仲裁的第一层判据。 */
  hasOverlay(): boolean {
    return this.ctx.overlayIds.length > 0
  }

  /** 记录一次未提交的操作（守卫阻止/用户取消/路由失败）。cursor 不动。 */
  recordCancelled(entryId: string, type: string, reason: string): void {
    this.record(entryId, type, 'cancelled', reason)
  }

  /** 记录一次失败。 */
  recordFailed(entryId: string, type: string, reason: string): void {
    this.record(entryId, type, 'failed', reason)
  }

  /**
   * 页面返回：把 cursor 移到已知前驱。
   *
   * 只在「已提交」时移动 cursor；调用方负责在路由守卫失败时改用
   * recordFailed 并保持 cursor 不动。
   */
  pop(): NavigationEntry | undefined {
    if (this.ctx.cursor <= 0) return undefined
    // 返回会关闭它上面的所有覆盖层。
    this.ctx.overlayIds.length = 0
    this.ctx.cursor -= 1
    const entry = this.current()
    this.record(entry?.id ?? '-', 'pop', 'committed')
    return entry
  }

  /** 前进：仅在无覆盖层且确有页面目的地时。 */
  forward(): NavigationEntry | undefined {
    if (this.hasOverlay()) return undefined
    const target = this.forwardTarget()
    if (!target) return undefined
    this.ctx.cursor += 1
    this.record(target.id, 'forward', 'committed')
    return target
  }

  /** 更新某个条目的标题（异步实体名到达、KeepAlive 重新激活等）。 */
  setTitle(id: string, title: string, source: TitleSource = 'registered'): void {
    const e = this.get(id)
    if (!e) return
    e.title = clampTitle(title)
    e.titleSource = source
    this.touch()
  }

  /** 存储滚动快照。 */
  setScroll(id: string, scrollId: string, anchor: ScrollAnchor): void {
    const e = this.get(id)
    if (!e) return
    e.view.scroll[scrollId] = {
      x: Number(anchor.x) || 0,
      y: Number(anchor.y) || 0,
      ...(anchor.anchorId ? { anchorId: anchor.anchorId } : {}),
    }
    this.touch()
  }

  /** 取滚动快照。 */
  getScroll(id: string, scrollId: string): ScrollAnchor | undefined {
    return this.get(id)?.view.scroll[scrollId]
  }

  /** 记录当前 history 位置，供 refresh 恢复用。 */
  rememberCursorFromHistory(): void {
    if (typeof window === 'undefined') return
    const cur = this.current()
    if (cur) cur.historyPosition = window.history.state?.hyperHistoryPosition ?? cur.historyPosition
  }

  /** 换账号 / 登出 / 换服务器：整体作废。 */
  reset(): void {
    this.ctx = makeContext()
    this.storage?.removeItem(STORAGE_KEY)
    this.storage?.removeItem(LEGACY_STORAGE_KEY)
    this.touch()
  }

  /**
   * 持久化到 sessionStorage。
   *
   * 只写去敏后的恢复信息：fullPath 裁掉非白名单 query，view 里不存业务行。
   * 存储不可用时静默降级——导航不依赖持久化。
   */
  persist(): void {
    if (!this.storage) return
    try {
      const safe: NavigationContext = {
        version: 2,
        cursor: this.ctx.cursor,
        overlayIds: [],
        operations: [],
        entries: this.ctx.entries.map((e) => ({
          ...e,
          // 冷启动不恢复未提交覆盖层。
          fullPath: sanitizePath(e.fullPath),
          view: { ...e.view, scroll: { ...e.view.scroll } },
        })),
      }
      this.storage.setItem(STORAGE_KEY, JSON.stringify(safe))
    } catch {
      // 配额满 / 隐私模式：导航继续，只是刷新不恢复。
    }
  }

  /**
   * 从 sessionStorage 恢复。
   *
   * v2 优先；只有 v1 时迁移一次。**任何 schema 不符都直接丢弃**，
   * 不做「尽力修复」——半恢复的导航比不恢复更危险。
   */
  restore(): boolean {
    if (!this.storage) return false
    let raw: string | null = null
    try {
      raw = this.storage.getItem(STORAGE_KEY)
      if (!raw) raw = this.storage.getItem(LEGACY_STORAGE_KEY)
    } catch {
      return false
    }
    if (!raw) return false
    let parsed: unknown
    try {
      parsed = JSON.parse(raw)
    } catch {
      return false
    }
    const validated = validateContext(parsed)
    if (!validated) return false
    this.ctx = validated
    this.touch()
    return true
  }

  /** 淘汰：只裁内存，页面条目与操作环各自封顶。 */
  private evict(): void {
    while (this.ctx.entries.length > MAX_PAGE_ENTRIES) {
      // 从最旧的开始丢，但绝不丢当前条目和还没被覆盖的 overlay。
      const dropIdx = this.firstEvictableIndex()
      if (dropIdx < 0) break
      this.ctx.entries.splice(dropIdx, 1)
      this.ctx.overlayIds = this.ctx.overlayIds.filter((id) => this.get(id))
      if (this.ctx.cursor >= dropIdx) this.ctx.cursor = Math.max(0, this.ctx.cursor - 1)
    }
    while (this.ctx.operations.length > MAX_OPERATIONS) this.ctx.operations.shift()
  }

  private firstEvictableIndex(): number {
    const overlaySet = new Set(this.ctx.overlayIds)
    for (let i = 0; i < this.ctx.entries.length; i += 1) {
      const e = this.ctx.entries[i]
      if (overlaySet.has(e.id)) continue
      // 当前条目及其之后都不能丢（丢掉就断了前进分支与 cursor 语义）。
      if (i >= this.ctx.cursor) continue
      return i
    }
    return -1
  }

  private record(
    entryId: string,
    type: string,
    outcome: NavigationOperation['outcome'],
    reason?: string,
  ): void {
    this.seq += 1
    const op: NavigationOperation = {
      seq: this.seq,
      entryId,
      type,
      timestamp: this.clock.now(),
      outcome,
    }
    if (reason) this.lastReason = reason
    this.ctx.operations.push(op)
    while (this.ctx.operations.length > MAX_OPERATIONS) this.ctx.operations.shift()
    this.touch()
  }

  private lastReason: string | undefined

  private presentationOfCursor(): Presentation | undefined {
    return this.current()?.presentation
  }

  /** 无标题弹层沿用的标题：自身 → 父 → 递归到页面。 */
  private inheritedTitle(entryId: string | undefined): string {
    const seen = new Set<string>()
    let cur = entryId
    while (cur && !seen.has(cur)) {
      seen.add(cur)
      const e = this.get(cur)
      if (!e) return ''
      if (e.title) return e.title
      cur = e.parentId
    }
    return ''
  }
}

function clampTitle(raw: string): string {
  // 只传纯文本、去首尾空格、限长；禁止标题 HTML 注入。
  return String(raw ?? '')
    .replace(/<[^>]*>/g, '')
    .trim()
    .slice(0, MAX_TITLE_LENGTH)
}

/** 裁掉白名单以外的 query。搜索原文与任何 token 都不得落盘。 */
export function sanitizePath(fullPath: string): string {
  const qIndex = fullPath.indexOf('?')
  if (qIndex < 0) return fullPath
  const path = fullPath.slice(0, qIndex)
  const params = new URLSearchParams(fullPath.slice(qIndex + 1))
  // 按**原始顺序**遍历而不是按白名单顺序遍历：重排 query 会让
  // 任何按 fullPath 字符串比较的地方（缓存键、恢复比对）行为漂移。
  const kept = new URLSearchParams()
  for (const [key, value] of params) {
    if (PERSISTED_QUERY_WHITELIST.includes(key)) kept.set(key, value)
  }
  const qs = kept.toString()
  return qs ? `${path}?${qs}` : path
}

/**
 * schema 校验。sessionStorage 内容同源可改写，也可能是半截 JSON。
 * 校验不过就是「没有可恢复的上下文」，不是「部分恢复」。
 */
export function validateContext(input: unknown): NavigationContext | null {
  if (typeof input !== 'object' || input === null) return null
  const o = input as Record<string, unknown>
  if (o.version !== 2) return null
  if (!Array.isArray(o.entries) || !Array.isArray(o.operations)) return null
  const entries: NavigationEntry[] = []
  for (const raw of o.entries) {
    if (typeof raw !== 'object' || raw === null) return null
    const e = raw as Record<string, unknown>
    if (typeof e.id !== 'string' || e.id.length === 0) return null
    if (typeof e.fullPath !== 'string') return null
    if (!isPresentation(e.presentation)) return null
    if (typeof e.title !== 'string') return null
    if (typeof e.createdAt !== 'number') return null
    const scope = e.scope
    if (typeof scope !== 'object' || scope === null) return null
    const s = scope as Record<string, unknown>
    if (typeof s.serverId !== 'string' || typeof s.accountId !== 'string') return null
    entries.push({
      id: e.id,
      parentId: typeof e.parentId === 'string' ? e.parentId : undefined,
      fullPath: e.fullPath,
      routeName: typeof e.routeName === 'string' ? e.routeName : undefined,
      presentation: e.presentation,
      openedBy: (typeof e.openedBy === 'string' ? e.openedBy : 'push') as NavigationKind,
      title: e.title,
      titleSource: (typeof e.titleSource === 'string' ? e.titleSource : 'route') as TitleSource,
      titleKey: typeof e.titleKey === 'string' ? e.titleKey : undefined,
      inheritedFrom: typeof e.inheritedFrom === 'string' ? e.inheritedFrom : undefined,
      scope: {
        serverId: s.serverId,
        accountId: s.accountId,
        projectId: typeof s.projectId === 'string' ? s.projectId : undefined,
      },
      view: { scroll: {} },
      historyPosition: typeof e.historyPosition === 'number' ? e.historyPosition : undefined,
      createdAt: e.createdAt,
    })
  }
  const cursor = typeof o.cursor === 'number' ? o.cursor : -1
  return {
    version: 2,
    entries,
    cursor: Math.min(cursor, entries.length - 1),
    overlayIds: [], // 冷启动不恢复未提交覆盖层
    operations: [],
  }
}

function isPresentation(v: unknown): v is Presentation {
  return v === 'page' || v === 'modal' || v === 'sheet' || v === 'focus'
}

function safeSessionStorage(): Storage | null {
  if (typeof window === 'undefined') return null
  try {
    const s = window.sessionStorage
    const probe = '__hyper_probe__'
    s.setItem(probe, '1')
    s.removeItem(probe)
    return s
  } catch {
    return null
  }
}
