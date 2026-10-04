/**
 * shell/titleResolver.ts — 标题真源解析。
 *
 * 顶栏标题与页面标题必须**共用一个解析结果**。以前常见的做法是分别读：
 * 顶栏从路由 meta 翻，页面从 DOM 找 h1，于是异步实体名到达时两者会短暂
 * 不一致，旧页的晚到响应还会覆盖新页标题。
 *
 * 解析次序（UI规范 06 §2，高优先级在前）：
 *   1. 最上层覆盖层显式登记的标题，或其 aria-labelledby 指向的标题节点
 *   2. 覆盖层自身的兼容标记（data-shell-title / 组件标题节点 / h1|h2）
 *      —— **只查该覆盖层**，不查背景页、也不查其它覆盖层
 *   3. 覆盖层没有标题 → 沿用它打开前的有效标题（不是「弹窗」，不是空）
 *   4. 无覆盖层 → 当前激活页面登记的标题，与页面实际标题绑同一个值
 *   5. 旧页兼容 → 页面根内的 data-shell-title / 主 h1
 *   6. 页面尚未渲染 → 路由 meta.titleKey 的翻译；最后才 document.title
 *
 * 两个必须成立的正确性约束：
 *   - **只扫当前页面与最上层覆盖层**；MutationObserver 合批到一帧，
 *     不在每次滚动时全树找所有 h1。
 *   - **异步结果带 entryId + renderEpoch**。旧页晚到的实体名不允许覆盖新页。
 */

import { MAX_TITLE_LENGTH, type NavigationContext, type NavigationEntry, type TitleSource } from './types.ts'

/** 一次解析的结果。 */
export interface ResolvedTitle {
  title: string
  source: TitleSource
  /** 这个标题属于哪个条目。异步回写时用它做守门。 */
  entryId?: string
}

/** 覆盖层/页面的标题 DOM 探针。抽成接口是为了让解析逻辑可单测。 */
export interface TitleDomProbe {
  /** 最上层覆盖层根元素；没有则 null。 */
  overlayRoot(): Element | null
  /** 当前激活页面的根元素；没有则 null。 */
  pageRoot(): Element | null
}

export interface TitleResolverOptions {
  /** 路由 meta 标题的翻译函数（可用 i18n 的 te/t）。 */
  translate?: (key: string) => string | undefined
  /** document.title 兜底。 */
  appName?: string
  dom?: TitleDomProbe
}

export class TitleResolver {
  /** entryId → 已登记标题。权威来源，高于一切 DOM 猜测。 */
  private registered = new Map<string, string>()
  /** entryId → renderEpoch。页面每次激活 +1。 */
  private epoch = new Map<string, number>()
  private opts: TitleResolverOptions

  constructor(opts: TitleResolverOptions = {}) {
    this.opts = opts
  }

  /**
   * 页面激活时推进 epoch。
   *
   * 这之后，任何携带旧 epoch 的异步结果都会被拒绝——这是「旧页晚到的项目名
   * 不能覆盖新页面」的唯一守门点。
   */
  beginRender(entryId: string): number {
    const next = (this.epoch.get(entryId) ?? 0) + 1
    this.epoch.set(entryId, next)
    return next
  }

  /** 页面显式登记标题（业务标题绑定 PageHeader 的同一个 computed）。 */
  register(entryId: string, title: string): void {
    const clean = sanitize(title)
    if (clean) this.registered.set(entryId, clean)
  }

  /**
   * 接受一个异步到达的标题。
   *
   * @returns true 表示已接受；false 表示被 epoch 守门拒绝。
   */
  acceptAsync(entryId: string, renderEpoch: number, title: string): boolean {
    if (this.epoch.get(entryId) !== renderEpoch) return false
    this.register(entryId, title)
    return true
  }

  /** 覆盖层关闭后清除登记，避免下次打开时读到上一次的标题。 */
  forget(entryId: string): void {
    this.registered.delete(entryId)
    this.epoch.delete(entryId)
  }

  /** 换账号/登出：标题里可能有姓名，必须整体清空。 */
  clearAll(): void {
    this.registered.clear()
    this.epoch.clear()
  }

  /**
   * 解析当前生效标题。
   *
   * @param ctx  当前导航上下文
   * @param store 用于无标题覆盖层的标题继承
   */
  resolve(ctx: NavigationContext, store?: { get(id: string): NavigationEntry | undefined }): ResolvedTitle {
    const top = topOverlayOf(ctx, store)
    const current = entryAtCursor(ctx, store)

    // 1) 最上层覆盖层：显式登记优先
    if (top) {
      const reg = this.registered.get(top.id)
      if (reg) return { title: reg, source: 'registered', entryId: top.id }

      // 1b) aria-labelledby
      const aria = this.scanAria(top)
      if (aria) return { title: aria, source: 'aria', entryId: top.id }

      // 2) 覆盖层自身的兼容标记——只在它自己的根里找
      const domTitle = this.scanRootFor(top)
      if (domTitle) return { title: domTitle, source: 'dom', entryId: top.id }

      // 3) 无标题弹层：沿用它打开前的有效标题
      const inherited = this.resolveInherited(top, ctx, store)
      if (inherited) return { ...inherited, entryId: top.id }
    }

    // 4) 当前激活页面登记的标题
    if (current) {
      const reg = this.registered.get(current.id)
      if (reg) return { title: reg, source: 'registered', entryId: current.id }
      // 恢复路径：从 sessionStorage 复原时注册表是空的，但条目上带着上次
      // 登记的标题快照。用它兜住，否则「刷新后标题掉回应用名」。
      if (current.title) {
        return { title: current.title, source: 'registered', entryId: current.id }
      }
    }

    // 5) 旧页兼容：页面根内的 data-shell-title / 主 h1
    if (current) {
      const domTitle = this.scanRootFor(current)
      if (domTitle) return { title: domTitle, source: 'dom', entryId: current.id }
    }

    // 6) 路由 meta → document.title → 应用名
    const routeTitle = current?.titleKey ? this.opts.translate?.(current.titleKey) : undefined
    if (routeTitle) return { title: sanitize(routeTitle), source: 'route', entryId: current?.id }

    const docTitle = typeof document !== 'undefined' ? document.title : ''
    if (docTitle) return { title: sanitize(docTitle), source: 'document', entryId: current?.id }

    const fallback = sanitize(this.opts.appName ?? '')
    return { title: fallback, source: 'document', entryId: current?.id }
  }

  /** 无标题覆盖层沿用父链上的有效标题，并记录继承来源。 */
  private resolveInherited(
    entry: NavigationEntry,
    ctx: NavigationContext,
    store?: { get(id: string): NavigationEntry | undefined },
  ): ResolvedTitle | null {
    const seen = new Set<string>()
    let cur = entry.inheritedFrom ?? entry.parentId
    while (cur && !seen.has(cur)) {
      seen.add(cur)
      const parent = store?.get(cur) ?? ctx.entries.find((e) => e.id === cur)
      if (!parent) break
      const reg = this.registered.get(parent.id)
      if (reg) return { title: reg, source: 'inherited' }
      if (parent.title) return { title: parent.title, source: 'inherited' }
      cur = parent.inheritedFrom ?? parent.parentId
    }
    return null
  }

  /**
   * aria-labelledby：只在该条目自己的根里解析。
   * 不查背景页，避免把背景页标题认成弹窗标题。
   */
  private scanAria(entry: NavigationEntry): string | null {
    const root = this.rootFor(entry)
    if (!root) return null
    const labelledBy = root.getAttribute('aria-labelledby')
    if (labelledBy) {
      const node = root.ownerDocument.getElementById(labelledBy)
      const t = node ? textOf(node) : ''
      if (t) return t
    }
    if (root.getAttribute('role') === 'dialog' || root.tagName === 'DIALOG') {
      const t = root.getAttribute('aria-label')
      if (t) return sanitize(t)
    }
    return null
  }

  /**
   * 兼容标记扫描：data-shell-title → 组件标题节点 → 主 h1/h2。
   *
   * 作用域写进选择器本身（`root.querySelector`），不靠调用方自觉传参——
   * 否则很容易退化成「在整份 document 里找第一个 h1」，
   * 那会把隐藏的 KeepAlive 页或非当前 Tab 的标题认成页面标题。
   */
  private scanRootFor(entry: NavigationEntry): string | null {
    const root = this.rootFor(entry)
    if (!root) return null
    const explicit = root.querySelector('[data-shell-title]')
    if (explicit) {
      const t = textOf(explicit)
      if (t) return t
    }
    const heading = root.querySelector('h1, h2')
    if (heading) {
      const t = textOf(heading)
      if (t) return t
    }
    return null
  }

  private rootFor(entry: NavigationEntry): Element | null {
    const probe = this.opts.dom
    if (!probe) return null
    const top = probe.overlayRoot()
    const page = probe.pageRoot()
    // 覆盖层条目用覆盖层根，页面条目用页面根；交叉取用会让标题串台。
    return entry.presentation === 'page' ? page : top
  }
}

function topOverlayOf(
  ctx: NavigationContext,
  store?: { get(id: string): NavigationEntry | undefined },
): NavigationEntry | undefined {
  for (let i = ctx.overlayIds.length - 1; i >= 0; i -= 1) {
    const id = ctx.overlayIds[i]
    const e = store?.get(id) ?? ctx.entries.find((x) => x.id === id)
    if (e) return e
  }
  return undefined
}

function entryAtCursor(
  ctx: NavigationContext,
  store?: { get(id: string): NavigationEntry | undefined },
): NavigationEntry | undefined {
  if (ctx.cursor < 0) return undefined
  const raw = ctx.entries[ctx.cursor]
  if (!raw) return undefined
  return store?.get(raw.id) ?? raw
}

function textOf(el: Element): string {
  return sanitize(el.textContent ?? '')
}

function sanitize(raw: string): string {
  return String(raw ?? '')
    .replace(/<[^>]*>/g, '')
    .replace(/\s+/g, ' ')
    .trim()
    .slice(0, MAX_TITLE_LENGTH)
}
