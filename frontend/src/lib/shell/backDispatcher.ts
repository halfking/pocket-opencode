/**
 * shell/backDispatcher.ts — 单一返回仲裁。
 *
 * 为什么要有「唯一」：返回可能被四个来源触发——顶栏返回钮、键盘 Esc、
 * Android 系统返回键、内容区左滑手势。如果每个来源各自判断「要不要先关弹窗」，
 * 就会出现「关了两层」「关了弹窗又跳了路由」这类穿透与重复。
 * 所以它们只**发意图**，由本模块按同一套优先级裁决一次。
 *
 * 优先级（UI规范 06 §5，从上到下）：
 *   1. 正在交互的子菜单/选择器声明消费返回 → 先关它（输入法由原生处理，不在 JS 强制提交表单）
 *   2. 关闭最高层覆盖层；专注层上的确认框先关确认框，再退出专注
 *   3. 覆盖层不可关闭或有未保存数据 → 调 beforeClose；拒绝时**消费返回并保持现状**，
 *      绝不跳到背景路由
 *   4. 无覆盖层 → 按当前分支的已知页面前驱返回；内部跳转走 Router 守卫
 *   5. 无已知前驱 → 路由登记的安全 fallback（用 replace，避免首页↔详情循环）
 *   6. 首页无层 → Web 保持首页；原生把意图交还系统
 *
 * 三条容易写错、因此在实现里显式成立的规则：
 *   - 返回在飞行中按 transition ID **单飞**，不靠固定 500ms 判定成功；
 *   - 未消费的返回必须被**显式标记**，因为「未消费」会穿透到路由层；
 *   - 返回钮的图标/名称随动作变化（页面「返回」、弹窗「关闭」、专注「退出专注」），
 *     由本模块导出 label/actionKind，UI 只渲染不判断。
 */

import type { BackOutcome, NavigationContext, NavigationEntry, Presentation } from './types.ts'

/** 覆盖层的可关闭契约。 */
export interface OverlayHandle {
  id: string
  presentation: Exclude<Presentation, 'page'>
  /** 有未保存数据时给出 beforeClose；返回 false 表示拒绝关闭。 */
  beforeClose?: () => boolean | Promise<boolean>
  close: () => void | Promise<void>
  /** 声明「正在交互、需要先消费返回」的子菜单/选择器。 */
  consumesBack?: boolean
}

/** 路由器适配器。本模块不直接依赖 vue-router，便于单测与降级。 */
export interface RouterAdapter {
  /** 返回是否真的发生（被守卫阻止时为 false）。 */
  pop(): Promise<boolean>
  /** 跳到安全落地页；必须用 replace。 */
  replace(fallbackPath: string): Promise<boolean>
}

/** 界面呈现：顶栏/按钮该显示什么。 */
export interface BackAffordance {
  actionKind: 'back' | 'close' | 'exit-focus'
  label: string
  /** 有覆盖层时 Esc 只关可关闭的上层，不默认退出整个 App。 */
  closeable: boolean
}

const LABEL: Record<BackAffordance['actionKind'], string> = {
  back: '返回',
  close: '关闭',
  'exit-focus': '退出专注',
}

export class BackDispatcher {
  private overlays: OverlayHandle[] = []
  private transitionId = 0
  private inFlight: Promise<BackOutcome> | null = null
  private router: RouterAdapter | null = null
  /** 路由级安全落地页登记。 */
  private fallbacks = new Map<string, string>()

  setRouter(router: RouterAdapter | null): void {
    this.router = router
  }

  /** 页面登记自己的安全落地页。深链进入时用它，而不是 history.length 随机退。 */
  registerFallback(routeName: string, path: string): void {
    this.fallbacks.set(routeName, path)
  }

  /** 注册覆盖层。返回一个注销函数。 */
  registerOverlay(handle: OverlayHandle): () => void {
    this.overlays.push(handle)
    return () => {
      const i = this.overlays.indexOf(handle)
      if (i >= 0) this.overlays.splice(i, 1)
    }
  }

  /** 当前呈现给用户的返回动作。UI 只渲染它，不自己判断。 */
  affordance(ctx: NavigationContext): BackAffordance {
    const top = this.topOverlay()
    if (!top) return { actionKind: 'back', label: LABEL.back, closeable: true }
    const kind = top.presentation === 'focus' ? 'exit-focus' : 'close'
    return { actionKind: kind, label: LABEL[kind], closeable: top.consumesBack !== false }
  }

  private topOverlay(): OverlayHandle | undefined {
    for (let i = this.overlays.length - 1; i >= 0; i -= 1) return this.overlays[i]
    return undefined
  }

  /**
   * 提交一次返回意图。
   *
   * 单飞：飞行中再来一次直接复用同一个 Promise，不排队也不叠加——
   * 重复提交曾导致「关了两层」和「关闭后又跳路由」。
   */
  back(ctx: NavigationContext): Promise<BackOutcome> {
    if (this.inFlight) return this.inFlight
    this.transitionId += 1
    const run = this.dispatch(ctx, this.transitionId).finally(() => {
      this.inFlight = null
    })
    this.inFlight = run
    return run
  }

  private async dispatch(ctx: NavigationContext, transitionId: number): Promise<BackOutcome> {
    // 1) 子菜单/选择器声明消费返回
    const consumer = this.topOverlay()
    if (consumer?.consumesBack === true) {
      await consumer.close()
      return { kind: 'overlay-closed', id: consumer.id }
    }

    // 2)+3) 覆盖层：先看能不能关，关不掉就消费掉这一次返回
    const overlay = this.topOverlay()
    if (overlay) {
      if (overlay.beforeClose) {
        const allowed = await overlay.beforeClose()
        if (!allowed) {
          // 拒绝：**消费**返回并保持现状，绝不跳到背景路由。
          return { kind: 'overlay-rejected', id: overlay.id, reason: 'unsaved-changes' }
        }
      }
      await overlay.close()
      return { kind: 'overlay-closed', id: overlay.id }
    }

    // 4) 无覆盖层 → 页面前驱
    if (ctx.cursor > 0) {
      const moved = (await this.router?.pop()) ?? false
      if (!moved) {
        // 守卫阻止/路由失败：cursor 不动，记一条失败。
        return { kind: 'blocked', reason: 'router-guard' }
      }
      const entry = ctx.entries[ctx.cursor - 1]
      return { kind: 'page-popped', entryId: entry?.id ?? '' }
    }

    // 5) 无已知前驱 → 安全落地页（replace）
    const current = ctx.entries[ctx.cursor]
    const fallback = current?.routeName ? this.fallbacks.get(current.routeName) : undefined
    if (fallback) {
      const ok = (await this.router?.replace(fallback)) ?? false
      if (!ok) return { kind: 'blocked', reason: 'fallback-router' }
      return { kind: 'page-popped', entryId: current?.id ?? '' }
    }

    // 6) 首页无层：交给宿主决定（Web 保持首页；原生交还系统）
    if (ctx.entries.length === 0) return { kind: 'handed-to-system' }
    return { kind: 'noop', reason: 'no-predecessor' }
  }

  /** 前进：仅在无覆盖层且确有页面目的地。 */
  forward(ctx: NavigationContext, go: (entry: NavigationEntry) => Promise<boolean>): Promise<BackOutcome> {
    if (this.overlays.length > 0) return Promise.resolve({ kind: 'blocked', reason: 'overlay-open' })
    const target = ctx.entries[ctx.cursor + 1]
    if (!target || target.presentation !== 'page') {
      return Promise.resolve({ kind: 'blocked', reason: 'no-forward-target' })
    }
    return go(target).then((ok) =>
      ok ? { kind: 'page-popped', entryId: target.id } : { kind: 'blocked', reason: 'forward-failed' },
    )
  }

  /** 换账号/登出：覆盖层注册表整体作废，否则会残留上一个账号的 beforeClose。 */
  reset(): void {
    this.overlays = []
    this.inFlight = null
    this.transitionId = 0
  }
}
