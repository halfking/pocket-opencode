/**
 * useKeyboardInset — 软键盘避让（visualViewport 方案，App 级单例）。
 *
 * 业务标准（微信 / Telegram / 系统表单的通用做法）：
 * 1. 键盘弹起时，底部输入区（composer 等 flex 停靠元素）贴住键盘上沿；
 * 2. 页面整体上滑，把正在编辑的栏目对齐到输入区上沿之上，而不是被键盘盖住。
 *
 * 为什么不走原生 adjustResize：Capacitor Android 壳是 edge-to-edge
 * （MainActivity setDecorFitsSystemWindows(false)），新系统上
 * SOFT_INPUT_ADJUST_RESIZE 不可靠——实测同一 APK：模拟器 API 36 WebView
 * 随键盘缩放（resize 路径），真机 Android 15 WebView 不缩、键盘直接盖在
 * 视口上（overlay 路径）。这里对两条路径统一建模：
 *
 * - 以「无键盘时见过的最大视口高」为基线 baseline；
 * - overlap = baseline - min(innerHeight, visualViewport.height)
 *   （resize 路径两值同缩、overlay 路径仅 vv 缩，overlap 都是键盘高度）；
 * - html.kb-open = 键盘在场（两路径通用，驱动 tabbar 滑走、内容槽位让位）；
 * - --kb-inset = overlay 路径的键盘高度（#app 高度收缩、flex 停靠输入区
 *   贴键盘）；resize 路径 WebView 已自己缩过，置 0 防止双重收缩。
 *
 * 键盘手势交互式收起时 resize 事件持续回流，变量 1:1 跟手还原。
 * 桌面浏览器 baseline === innerHeight === vv.height，整套机制自动 no-op。
 * iOS WKWebView 未装 @capacitor/keyboard 时行为差异较大，以 Android 真机为准。
 */

import { computed, ref } from 'vue'

/** 键盘净高（CSS px；0 = 键盘不在场；overlay 路径 = 键盘遮挡高度）。 */
const keyboardHeight = ref(0)
const keyboardVisible = computed(() => keyboardHeight.value > 0)

/** 高于该差值才认定键盘弹起（排除浏览器工具栏/地址栏这类小幅视口变化）。 */
const OPEN_THRESHOLD = 120
/** 已在场时低于该值即认定键盘收起（留迟滞区间避免临界抖动）。 */
const CLOSE_THRESHOLD = 60
/** 聚焦字段与输入区/键盘上沿之间的呼吸间距。 */
const REVEAL_GAP = 12
/** 键盘再次调整高度（候选栏展开等）超过该差值时做一次非动画校正。 */
const REVEAL_DELTA = 40

/** 无键盘时的 layout viewport 高度基线；旋转/分屏后经 max() 自动重学。 */
let baseline = 0

const TEXT_INPUT_SEL = 'input, textarea, select, [contenteditable="true"], [contenteditable=""]'

function isTextInput(el: Element | null): el is HTMLElement {
  return !!el && el.matches(TEXT_INPUT_SEL)
}

/** el 最近的可滚动祖先（overflow auto/scroll 且确实可滚）。 */
function nearestScrollableAncestor(el: HTMLElement): HTMLElement | null {
  let node: HTMLElement | null = el.parentElement
  while (node && node !== document.body) {
    const oy = getComputedStyle(node).overflowY
    if ((oy === 'auto' || oy === 'scroll') && node.scrollHeight > node.clientHeight) {
      return node
    }
    node = node.parentElement
  }
  return null
}

/**
 * 把聚焦字段滚到最近滚动容器的底部上沿之上（留 REVEAL_GAP）。
 * 容器底此刻就是键盘上沿——即"聚焦栏目对齐输入区上沿"。
 */
function revealFocusedInput(smooth: boolean): void {
  const el = document.activeElement
  if (!isTextInput(el)) return
  const scroller = nearestScrollableAncestor(el)
  if (!scroller) return
  const er = el.getBoundingClientRect()
  const sr = scroller.getBoundingClientRect()
  const reduced = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
  const behavior = smooth && !reduced ? ('smooth' as const) : ('auto' as const)
  if (er.bottom + REVEAL_GAP > sr.bottom) {
    scroller.scrollBy({ top: er.bottom + REVEAL_GAP - sr.bottom, behavior })
  } else if (er.top - REVEAL_GAP < sr.top) {
    scroller.scrollBy({ top: er.top - REVEAL_GAP - sr.top, behavior })
  }
  if (er.left < sr.left) {
    scroller.scrollBy({ left: er.left - sr.left, behavior: 'auto' })
  } else if (er.right > sr.right) {
    scroller.scrollBy({ left: er.right - sr.right, behavior: 'auto' })
  }
}

function measure(): void {
  const vv = window.visualViewport
  if (!vv) return
  const vvH = Math.round(vv.height)
  const innerH = window.innerHeight
  if (innerH > baseline) baseline = innerH
  if (baseline === 0) return
  // resize 路径 innerH 与 vvH 同缩；overlay 路径仅 vvH 缩。取小者对基线求差
  // 即键盘高度，两路径统一。
  const overlap = baseline - Math.min(innerH, vvH)
  let next: number
  if (keyboardHeight.value > 0) {
    next = overlap <= CLOSE_THRESHOLD ? 0 : overlap
  } else {
    next = overlap >= OPEN_THRESHOLD ? overlap : 0
  }
  if (next === keyboardHeight.value) return
  const wasOpen = keyboardHeight.value > 0
  const delta = Math.abs(next - keyboardHeight.value)
  keyboardHeight.value = next

  const root = document.documentElement
  if (next > 0) {
    // 原生 resize 已把 layout viewport 缩到位时不能再用 --kb-inset 收缩
    // #app（会双重下移），只保留 kb-open 类驱动 tabbar/槽位规则。
    const nativeResized = baseline - innerH > CLOSE_THRESHOLD
    root.style.setProperty('--kb-inset', nativeResized ? '0px' : `${next}px`)
    root.classList.toggle('kb-resized', nativeResized)
    root.classList.add('kb-open')
    if (!wasOpen) {
      // 键盘升起的同一帧布局收缩已下发；等两帧样式生效后把聚焦字段
      // 对齐到输入区上沿。320ms 后再校正一次，兜住只回调一次 resize 的引擎。
      requestAnimationFrame(() => requestAnimationFrame(() => revealFocusedInput(true)))
      window.setTimeout(() => revealFocusedInput(false), 320)
    } else if (delta >= REVEAL_DELTA) {
      revealFocusedInput(false)
    }
  } else {
    root.style.setProperty('--kb-inset', '0px')
    root.classList.remove('kb-open', 'kb-resized')
  }
}

/** 键盘已开场中切换焦点（登录页用户名 → 密码）：无动画地对齐新字段。 */
function onFocusIn(e: FocusEvent): void {
  if (!isTextInput(e.target as Element)) return
  if (keyboardHeight.value > 0) {
    requestAnimationFrame(() => revealFocusedInput(false))
  }
}

let installed = false

/** App.vue setup 里调用一次；重复调用为 no-op。 */
export function installKeyboardInset(): void {
  if (installed || typeof window === 'undefined') return
  const vv = window.visualViewport
  if (!vv) return
  installed = true
  vv.addEventListener('resize', measure)
  // 兜底：旋转等引起 layout viewport 变化时同步重算
  window.addEventListener('resize', measure)
  document.addEventListener('focusin', onFocusIn, true)
  // 页面在键盘已开状态下热重载/恢复时对齐初始值
  measure()
}

/** 供视图订阅键盘高度（如聊天页键盘弹起时贴底滚动）。 */
export function useKeyboardInset() {
  return { keyboardHeight, keyboardVisible }
}
