/**
 * shell/focusWorkspace.ts — 专注全屏工作区（表格/Tab 一键展开）。
 *
 * 它是**可退出的应用内全屏层**，不是浏览器 Fullscreen API，也不靠双击/长按
 * 隐藏入口。核心不是「变全屏」，而是**事件隔离**：进入后背景产品交互一律
 * 不响应，退出后一切（滚动、焦点、锁、inert）精确归还。
 *
 * 状态机：normal → entering → focused → exiting → normal。
 * 进入前保存快照与 presentation='focus'，再锁背景；退出按相反顺序，
 * **任一步失败都要清理锁与注册**（半解锁的页面比不解锁更糟）。
 *
 * 几条容易做错、因此在实现里显式成立的规则：
 *
 *  1. 锁 `body` 不等于锁住背景。本仓主内容区是独立滚动容器 `<main>`，
 *     只锁 body 的话背景照样能滚——所以必须锁**真实滚动宿主**。
 *  2. 锁要**准确恢复原有 inline style**。粗暴写 `overflow=''` 会抹掉
 *     页面自己设的值。
 *  3. `inert` 在旧 WebView 不可用，需要经验证的 fallback（焦点门 +
 *     aria-hidden + 指针屏蔽 + 可恢复属性快照）。
 *  4. 专注层之上打开的确认框/菜单**属于专注**，不得被 inert 一起屏蔽。
 *  5. 暂停的是**背景**的 observer/快捷键/刷新/滑动返回/点击；系统返回、
 *     权限提示、安全事件仍然生效。
 *
 * 纯状态机 + DOM 效果分离，便于单测状态迁移（DOM 效果走注入的 adapter）。
 */

export type FocusStatus = 'normal' | 'entering' | 'focused' | 'exiting'

export interface FocusSnapshot {
  /** 进入前的滚动位置（按 scroll id）。退出时精确归还。 */
  scroll: Record<string, { x: number; y: number }>
  /** 触发按钮的焦点，退出后归还——否则键盘用户「焦点掉到 body」。 */
  triggerFocusId: string | null
  /** 背景滚动宿主的 inline style 原值。 */
  inlineStyles: Array<{ el: HTMLElement; overflow: string; position: string }>
  /** 背景根的 inert/aria-hidden 原值。 */
  inert: boolean | null
  ariaHidden: string | null
}

/** DOM 效果适配器。抽出来是为了状态机能脱离浏览器单测。 */
export interface FocusDomAdapter {
  setBackgroundInert(on: boolean): void
  setFocusableTrap(root: HTMLElement | null): void
  lockScrollHost(el: HTMLElement): void
  unlockScrollHost(el: HTMLElement, snapshot: { overflow: string; position: string }): void
  saveScroll(id: string): { x: number; y: number }
  restoreScroll(id: string, v: { x: number; y: number }): void
  lockBackgroundInteractions(on: boolean): void
}

export interface FocusWorkspaceOptions {
  dom: FocusDomAdapter
  /** 背景滚动宿主（真实可滚动容器，通常是 AppLayout 的 <main>）。 */
  scrollHosts: () => HTMLElement[]
  /** 进入时登记的导航条目 id，用于把 presentation 标成 focus。 */
  entryId?: string
  onStatusChange?: (s: FocusStatus) => void
}

export class FocusWorkspace {
  private status: FocusStatus = 'normal'
  private snapshot: FocusSnapshot | null = null
  private opts: FocusWorkspaceOptions
  /** 引用计数：子确认框退出不能提前解锁背景。 */
  private depth = 0

  constructor(opts: FocusWorkspaceOptions) {
    this.opts = opts
  }

  get current(): FocusStatus {
    return this.status
  }

  get isFocused(): boolean {
    return this.status === 'focused' || this.status === 'entering'
  }

  /**
   * 进入专注。
   *
   * 顺序不能变：先存快照 → 再锁背景。反过来的话锁住之后就量不到滚动位置了。
   */
  enter(trigger: { focusId?: string | null } = {}): FocusSnapshot {
    if (this.status !== 'normal') return this.snapshot as FocusSnapshot
    this.setStatus('entering')

    const inlineStyles = this.opts.scrollHosts().map((el) => ({
      el,
      overflow: el.style.overflow,
      position: el.style.position,
    }))
    const scroll: FocusSnapshot['scroll'] = {}
    for (const host of this.opts.scrollHosts()) {
      const id = host.dataset.focusScrollId
      if (id) scroll[id] = this.opts.dom.saveScroll(id)
    }

    this.snapshot = {
      scroll,
      triggerFocusId: trigger.focusId ?? null,
      inlineStyles,
      inert: null,
      ariaHidden: null,
    }

    for (const host of this.opts.scrollHosts()) this.opts.dom.lockScrollHost(host)
    this.opts.dom.setBackgroundInert(true)
    this.opts.dom.lockBackgroundInteractions(true)

    this.setStatus('focused')
    return this.snapshot
  }

  /**
   * 退出专注。
   *
   * 反向顺序解锁，且**任何一步抛错都仍然继续清理剩余的锁**——
   * 一次失败不应该让背景永久失去滚动能力。
   */
  exit(): void {
    if (this.status !== 'focused' && this.status !== 'entering') return
    this.setStatus('exiting')
    const snap = this.snapshot
    try {
      this.opts.dom.lockBackgroundInteractions(false)
      this.opts.dom.setBackgroundInert(false)
      this.opts.dom.setFocusableTrap(null)
      for (const { el, overflow, position } of snap?.inlineStyles ?? []) {
        // 准确恢复原 inline style，不粗暴置空。
        this.opts.dom.unlockScrollHost(el, { overflow, position })
      }
      for (const [id, v] of Object.entries(snap?.scroll ?? {})) {
        this.opts.dom.restoreScroll(id, v)
      }
      if (snap?.triggerFocusId) this.restoreFocus(snap.triggerFocusId)
    } finally {
      this.snapshot = null
      this.depth = 0
      this.setStatus('normal')
    }
  }

  /** 专注层之上打开子层（确认框/菜单）：引用计数 +1。 */
  pushChild(): void {
    this.depth += 1
  }

  /** 子层关闭：引用计数 -1。只有归零才允许真正退出。 */
  popChild(): boolean {
    this.depth = Math.max(0, this.depth - 1)
    return this.depth === 0
  }

  get childDepth(): number {
    return this.depth
  }

  /** 路由卸载/401/异常退出：走同一条清理路径。 */
  forceExit(): void {
    if (this.status === 'normal') return
    this.exit()
  }

  private restoreFocus(id: string): void {
    if (typeof document === 'undefined') return
    const el = document.getElementById(id)
    if (el && 'focus' in el) (el as HTMLElement).focus()
  }

  private setStatus(s: FocusStatus): void {
    this.status = s
    this.opts.onStatusChange?.(s)
  }
}

/**
 * 背景事件是否应被拦截。
 *
 * ⚠️ 只 stopPropagation 撤销不了**已经执行**的外部监听器——所以运行时的
 * 全局 capture handler 必须检查事件 owner。本函数是那个 owner 判据。
 */
export function shouldBlockBackgroundEvent(status: FocusStatus, target: EventTarget | null, focusRoot: EventTarget | null): boolean {
  if (status !== 'focused' && status !== 'entering') return false
  if (!target) return false
  // 专注层自身（含其子层确认框/菜单）一律放行。
  if (focusRoot && (focusRoot === target || (focusRoot as Node).contains?.(target as Node))) return false
  return true
}
