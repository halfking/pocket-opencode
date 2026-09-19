/**
 * routeTransition — 路由转场的跨模块状态（原生顺滑度审计 P0 #1/#2 + P1 #9）。
 *
 * 三个消费方：
 *  - router-mobile.ts 的全局守卫：判定方向（push/pop/tab）+ 保存滚动位置
 *  - useSwipeBack：提交手势时写入 swipeFromPx（pop 转场从拖拽处接力）
 *  - App.vue 的 <Transition> 钩子：消费方向/起始位移，快照离场滚动
 *
 * 用普通对象而非 ref：守卫在渲染 flush 前同步写入，App.vue 渲染时读取，
 * 路由变化本身已触发重渲染，无需额外响应式开销。
 */

export type NavDirection = 'push' | 'pop' | 'tab' | 'none'

/** 路径深度：/email → 1，/email/123 → 2。方向判定用（审计建议的 meta.depth 等价实现，免改 50+ 条路由）。 */
export function depthOf(path: string): number {
  return path.replace(/\/+$/, '').split('/').filter(Boolean).length
}

export const transitionState = {
  /** 本次导航的方向（router 守卫写入，Transition 渲染时读取） */
  direction: 'none' as NavDirection,
  /** 右滑返回提交时的拖拽位移（px）。pop 转场从这里接力滑出，避免"回弹再滑"跳变 */
  swipeFromPx: null as number | null,
  /** 滚动位置记忆：path → main.scrollTop（P1 #9，返回/切 tab 恢复） */
  scrollMemory: new Map<string, number>(),
  /** 待恢复的滚动位置（守卫写入，Transition enter 钩子消费） */
  pendingScrollTop: 0,
}

/** useSwipeBack 提交手势时调用：记录拖拽位移供 pop 转场接力 */
export function prepareSwipePop(px: number) {
  transitionState.swipeFromPx = Math.max(0, Math.round(px))
}

/** Transition leave 钩子消费一次，防重放 */
export function consumeSwipeFromPx(): number | null {
  const px = transitionState.swipeFromPx
  transitionState.swipeFromPx = null
  return px
}

/**
 * 守卫入口：每次导航前调用。
 *  - 判定方向（深度增加=push，减少=pop，持平=tab）
 *  - 记忆离场页滚动位置
 *  - push 落地页从 0 开始；pop/tab 恢复记忆位置
 */
export function beforeRouteTransition(toPath: string, fromPath: string, fromMatched: number) {
  if (fromMatched === 0) {
    transitionState.direction = 'none'
    return
  }
  const dt = depthOf(toPath) - depthOf(fromPath)
  transitionState.direction = dt > 0 ? 'push' : dt < 0 ? 'pop' : 'tab'

  if (typeof document !== 'undefined') {
    const main = document.querySelector<HTMLElement>('#main')
    if (main) transitionState.scrollMemory.set(fromPath, main.scrollTop)
  }
  transitionState.pendingScrollTop =
    transitionState.direction === 'push' ? 0 : transitionState.scrollMemory.get(toPath) ?? 0
}
