/**
 * useKeyboardInset — 软键盘避让（Android 原生 insets + visualViewport 双信号）。
 *
 * 业务标准（微信 / Telegram / 系统表单的通用做法）：
 * 1. 键盘弹起时，底部输入区（composer 等 flex 停靠元素）贴住键盘上沿；
 * 2. 页面整体上滑，把正在编辑的栏目对齐到输入区上沿之上，而不是被键盘盖住。
 *
 * ## 三条路径，不是一条
 *
 * 原实现只用视口高度差建模（`baseline - min(innerHeight, vv.height)`），它能
 * 归一两条路径：
 *
 * - **resize 路径**：WebView 自己缩，innerHeight 与 vv.height 同缩；
 * - **overlay 路径（有信号）**：视口不缩但 visualViewport 缩。
 *
 * 但 2026-10-03 模拟器 API 35 实测发现**第三条路径，机制在上面完全失效**：
 *
 *   键盘弹起后 `innerHeight` 与 `visualViewport.height` **恒定不变**
 *   （实测 915 = 915，与收起时一模一样），键盘只是画在视口上面。
 *
 * 原因在原生侧：MainActivity 是 edge-to-edge（setDecorFitsSystemWindows(false)）
 * 且 manifest 没有声明 adjustResize，Android 15 的 IME 于是既不重布局 WebView
 * 也不动 visualViewport。此时 overlap 恒为 0 → `kb-open` 不置位 →
 * `--kb-inset` 不下发 → **聚焦输入框被键盘盖住**（实测证据见
 * test-evidence/2026-10-03-audit/02-keyboard-password.png）。
 *
 * 真机 Android 15 走的是同一条路径（见本文件历史版本的文件头），所以这不是
 * 模拟器特例，而是这一整类设备上的真实缺陷。
 *
 * ## 修法：原生 insets 作为权威信号
 *
 * MainActivity 的 window insets 监听补上 `WindowInsetsCompat.Type.ime()`，
 * 换算成 CSS px 注入 `--android-ime-inset`（沿用项目已有的
 * `--android-safe-top/bottom` 注入模式）。本模块读它：
 *
 * - 原生值 > 阈值 → 键盘在场，净高直接取原生值（**权威**）；
 * - 原生值缺失/为 0（iOS、桌面浏览器、极老内核）→ 回落原来的视口差模型。
 *
 * 取「有信号的一方为准」而不是相加：resize 路径下 IME insets 与视口差描述的是
 * 同一段键盘高度，相加会翻倍。
 *
 * 监听方式用 MutationObserver 盯 <html> 的 style 属性——原生用 setProperty
 * 写入 CSS 变量，CSS 变量变化本身不派发任何事件，只有属性变更能被观察到。
 * 变更是事件驱动的（IME 显隐各一次），不存在轮询开销。
 *
 * --kb-inset 的语义保持不变：#app 高度收缩、flex 停靠输入区贴键盘。resize
 * 路径下 WebView 已自己缩过，置 0 防止双重收缩。
 *
 * iOS WKWebView 未装 @capacitor/keyboard 时行为差异较大，以 Android 为准。
 */

import { computed, ref } from 'vue'

/** 键盘净高（CSS px；0 = 键盘不在场）。 */
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

/** 原生 --android-ime-inset 是否已经被原生层写过（区分「没这个键」与「键盘没开」）。 */
let nativeImeSeen = false

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

/**
 * 读原生注入的 IME 净高（CSS px）；没有这个变量时返回 null。
 *
 * 返回 null 而不是 0 是关键：「原生层不存在」（iOS / 桌面浏览器）与
 * 「原生层在报、键盘没开」必须能区分——前者该走视口差模型，后者就是 0。
 */
function readNativeImeInset(): number | null {
  if (typeof document === 'undefined') return null
  const raw = document.documentElement.style.getPropertyValue('--android-ime-inset')
  if (raw === '') return null
  const n = Number.parseFloat(raw)
  return Number.isFinite(n) ? n : null
}

function measure(): void {
  const vv = window.visualViewport
  const nativeIme = readNativeImeInset()
  if (nativeIme !== null) nativeImeSeen = true

  // ---- 信号一：原生 IME insets（权威）----
  // 迟滞与视口模型共用同一组阈值，保证两条路径的边界行为一致。
  if (nativeIme !== null) {
    let next: number
    if (keyboardHeight.value > 0) {
      next = nativeIme <= CLOSE_THRESHOLD ? 0 : nativeIme
    } else {
      next = nativeIme >= OPEN_THRESHOLD ? nativeIme : 0
    }
    applyKeyboardHeight(next, /* fromNative */ true)
    return
  }

  // ---- 信号二：视口高度差（iOS / 桌面 / 无原生层的回落路径）----
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
  // 原生层已经在场说明视口模型在这台设备上不可信，别让它把已知的键盘高度抹掉。
  if (nativeImeSeen && next === 0 && keyboardHeight.value > 0) return
  applyKeyboardHeight(next, /* fromNative */ false)
}

/** 统一下发键盘态：写 CSS 变量 / 类名，并在需要时把聚焦字段顶上来。 */
function applyKeyboardHeight(next: number, fromNative: boolean): void {
  if (next === keyboardHeight.value) return
  const wasOpen = keyboardHeight.value > 0
  const delta = Math.abs(next - keyboardHeight.value)
  keyboardHeight.value = next

  const root = document.documentElement
  if (next > 0) {
    // 原生 resize 已把 layout viewport 缩到位时不能再用 --kb-inset 收缩
    // #app（会双重下移），只保留 kb-open 类驱动 tabbar/槽位规则。
    // 原生 insets 路径下 WebView 不会自己缩，所以 --kb-inset 照常取键盘净高。
    const nativeResized = !fromNative && baseline - window.innerHeight > CLOSE_THRESHOLD
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

let installed = false

/** 键盘已开场中切换焦点（登录页用户名 → 密码）：无动画地对齐新字段。 */
function onFocusIn(e: FocusEvent): void {
  if (!isTextInput(e.target as Element)) return
  if (keyboardHeight.value > 0) {
    requestAnimationFrame(() => revealFocusedInput(false))
  }
}

/**
 * 盯 <html> 的 style 属性：原生用 setProperty 写 --android-ime-inset，
 * CSS 变量变化不派发任何事件，只有属性变更可观察。
 */
function watchNativeImeInset(): void {
  if (typeof MutationObserver === 'undefined') return
  const root = document.documentElement
  let last = root.style.getPropertyValue('--android-ime-inset')
  new MutationObserver(() => {
    const now = root.style.getPropertyValue('--android-ime-inset')
    if (now === last) return
    last = now
    measure()
  }).observe(root, { attributes: true, attributeFilter: ['style'] })
}

/** App.vue setup 里调用一次；重复调用为 no-op。 */
export function installKeyboardInset(): void {
  if (installed || typeof window === 'undefined') return
  installed = true
  const vv = window.visualViewport
  if (vv) {
    vv.addEventListener('resize', measure)
    // 兜底：旋转等引起 layout viewport 变化时同步重算
    window.addEventListener('resize', measure)
  }
  document.addEventListener('focusin', onFocusIn, true)
  watchNativeImeInset()
  // 页面在键盘已开状态下热重载/恢复时对齐初始值
  measure()
}

/** 供视图订阅键盘高度（如聊天页键盘弹起时贴底滚动）。 */
export function useKeyboardInset() {
  return { keyboardHeight, keyboardVisible }
}
