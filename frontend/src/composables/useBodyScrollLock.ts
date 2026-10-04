/**
 * useBodyScrollLock — 模态层（Dialog / BottomSheet / ConfirmDialog）打开时
 * 锁住**背景滚动**，关闭时释放。引用计数实现：多个模态层同时打开时互不干扰，
 * 全部关闭后才真正释放。
 *
 * ⚠️ 2026-10-06：**原来只锁 `document.body`，在本仓是空操作。**
 * 本仓的滚动宿主不是 body：
 *   - `AppLayout` 的 `.content` 是 `flex:1 1 auto; min-height:0; overflow-y:auto`；
 *   - 带 `PullToRefresh` 的页面里，真正的滚动容器是它的 `.refresh-content`。
 * 实测（e2e，`/email` + BottomSheet 打开）：
 *   `body.style.overflow = 'hidden'` 之后，背景容器的 `scrollTop`
 *   照样能从 600 改到 100 —— 锁了个不滚的东西。
 * 这条规则在 `lib/shell/focusWorkspace.ts` 的规则 1 里早就写明了
 * （「锁 body **不等于**锁住背景」），本 composable 是它的劣化版。
 *
 * 修法：**同时锁住 `#app` 内所有当前可滚动的容器**。
 * 只扫 `#app` 子树，是因为弹层都用 `<Teleport to="body">` 渲染
 * （BottomSheet / Dialog 均已核对），落在 `#app` 之外——这样既不会漏掉
 * 背景容器，也不会把弹层**自身**的长列表一起锁死。
 *
 * 边界（诚实标注）：
 *  - 只锁**acquire 那一刻**已经在滚的容器。若某个容器在弹层打开后才开始
 *    溢出（数据异步到达把它撑高），它不会被锁。彻底解法是 CSS 层面的
 *    `overflow: clip` + 焦点陷阱，属 `focusWorkspace` 的职责。
 *  - 程序化 `scrollTop` 不受 overflow 影响；本 composable 防的是**用户手势**
 *    与滚轮。回归判据因此直接断言「手势/滚轮不再改变背景 scrollTop」。
 *
 * 用法：
 *   const lock = useBodyScrollLock()
 *   watch(visible, (v) => v ? lock.acquire() : lock.release())
 *   onUnmounted(() => lock.release())
 */

/** 容器被认为是「在滚」的最小溢出量，滤掉 1~2px 的舍入抖动。 */
const SCROLLABLE_SLACK_PX = 20

interface LockedHost {
  el: HTMLElement
  overflow: string
  overflowY: string
}

let lockCount = 0
let savedBodyOverflow = ''
let savedBodyOverflowY = ''
let lockedHosts: LockedHost[] = []

/** 找出 `#app` 内真正在滚的容器。 */
function findScrollableHosts(): HTMLElement[] {
  const root = document.getElementById('app')
  if (!root) return []
  const out: HTMLElement[] = []
  for (const el of root.querySelectorAll<HTMLElement>('*')) {
    if (el.scrollHeight <= el.clientHeight + SCROLLABLE_SLACK_PX) continue
    const oy = getComputedStyle(el).overflowY
    if (oy !== 'auto' && oy !== 'scroll') continue
    out.push(el)
  }
  return out
}

export function useBodyScrollLock() {
  function acquire() {
    if (typeof document === 'undefined') return
    lockCount += 1
    if (lockCount > 1) return // 已有层锁着，别重复快照（否则后开的层会覆盖先开的恢复值）

    savedBodyOverflow = document.body.style.overflow
    savedBodyOverflowY = document.body.style.overflowY
    document.body.style.overflow = 'hidden'
    document.body.style.overflowY = 'hidden'

    lockedHosts = findScrollableHosts().map((el) => {
      const snapshot: LockedHost = {
        el,
        overflow: el.style.overflow,
        overflowY: el.style.overflowY,
      }
      el.style.overflow = 'hidden'
      el.style.overflowY = 'hidden'
      return snapshot
    })
  }

  function release() {
    if (lockCount <= 0) return
    lockCount -= 1
    if (lockCount > 0) return

    for (const { el, overflow, overflowY } of lockedHosts) {
      // 逐项**精确**恢复原 inline 值。粗暴写 '' 会抹掉页面自己设的样式。
      el.style.overflow = overflow
      el.style.overflowY = overflowY
    }
    lockedHosts = []
    if (typeof document !== 'undefined') {
      document.body.style.overflow = savedBodyOverflow
      document.body.style.overflowY = savedBodyOverflowY
      savedBodyOverflow = ''
      savedBodyOverflowY = ''
    }
  }

  /** 供调试与回归判据读取：当前锁住了几个容器。 */
  function lockedCount(): number {
    return lockedHosts.length
  }

  return { acquire, release, lockedCount }
}
