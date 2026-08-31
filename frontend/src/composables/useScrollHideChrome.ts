/**
 * Scroll-linked chrome hide/show — 底部输入区 + tabbar 跟手 1:1 下移/回弹，
 * 停手后按位移比例或快甩速度吸附到位（iOS 18 Safari 底部工具栏模式）。
 *
 * 交互规格（2026-08-31 定稿）：
 * - 跟手：滚动增量直接映射 chrome 位移（clamp [0, maxHide]），无过渡；
 * - 吸附：滚动静止 SNAP_DELAY_MS 后，位移 ≥ 35% 全隐 / < 35% 全显；
 * - 快甩优先：FLICK_WINDOW_MS 内累计位移达阈值则无视比例直接吸附
 *   （唤出阈值低于隐藏阈值——iOS 的不对称性：回看时更容易唤出）；
 * - 顶部（含下拉刷新区）与底部橡皮筋过度滚动强制展示；
 * - pin：聚焦输入框期间钉住展示（浏览器为露出光标产生的程序化滚动
 *   不应触发隐藏）；
 * - suppress：业务侧程序化滚动（如 scrollToBottom）前后抑制上报。
 */
import { ref, type Ref } from 'vue'

export interface ScrollReport {
  scrollTop: number
  delta: number
  /** 底部橡皮筋过度滚动（iOS bounce，scrollTop 超出滚动范围）：强制露出 chrome */
  overscrollBottom?: boolean
}

export interface ScrollHideChrome {
  hiddenOffset: Ref<number>
  /** 吸附动画进行中（消费方据此开启 CSS transition） */
  snapping: Ref<boolean>
  /** 吸附落定后的隐藏态（跟手过程保持上一次吸附值，不随手翻动——
   消费方据此做离散布局让位：padding/margin 让位只在吸附瞬间切换） */
  hidden: Ref<boolean>
  reportScroll: (report: ScrollReport) => void
  /** 内容区点击/聚焦等场景：从底部唤出（已在展示态则无操作） */
  reveal: () => void
  toggle: () => void
  /** 聚焦输入框时钉住展示；失焦解除 */
  setPinned: (pinned: boolean) => void
  /** 程序化滚动前后抑制上报，避免误触发隐藏 */
  suppress: (ms?: number) => void
  reset: () => void
}

const SNAP_DELAY_MS = 100
/** 吸附动画时长：与 tokens.css --duration-chrome 保持一致 */
export const CHROME_SNAP_DURATION_MS = 280
/** 位移比例吸附阈值（占 maxHide） */
const SNAP_THRESHOLD_RATIO = 0.35
/** 快甩判定窗口与阈值：窗口内累计位移达阈值则无视比例直接吸附 */
const FLICK_WINDOW_MS = 120
const FLICK_REVEAL_PX = 32
const FLICK_HIDE_PX = 48
const SUPPRESS_DEFAULT_MS = 400

export function createScrollHideChrome(getMaxHide: () => number): ScrollHideChrome {
  const hiddenOffset = ref(0)
  const snapping = ref(false)
  const hidden = ref(false)
  const pinned = ref(false)
  let snapTimer: ReturnType<typeof setTimeout> | null = null
  let snapEndTimer: ReturnType<typeof setTimeout> | null = null
  let suppressUntil = 0
  let flickSamples: Array<{ t: number; d: number }> = []

  function clearSnapTimers() {
    if (snapTimer) {
      clearTimeout(snapTimer)
      snapTimer = null
    }
    if (snapEndTimer) {
      clearTimeout(snapEndTimer)
      snapEndTimer = null
    }
  }

  function animateTo(target: number) {
    clearSnapTimers()
    snapping.value = true
    hiddenOffset.value = target
    hidden.value = target > 0
    snapEndTimer = setTimeout(() => {
      snapping.value = false
    }, CHROME_SNAP_DURATION_MS)
  }

  /** 清理窗口外样本并返回窗口内累计位移（正=向下滚/隐藏方向） */
  function flickSum(now: number): number {
    flickSamples = flickSamples.filter((s) => now - s.t <= FLICK_WINDOW_MS)
    return flickSamples.reduce((sum, s) => sum + s.d, 0)
  }

  function reportScroll({ scrollTop, delta, overscrollBottom }: ScrollReport) {
    const max = getMaxHide()
    if (max <= 0) return
    // 抑制窗口（程序化滚动）与 pin（输入聚焦）内不参与跟手，
    // 也不打断进行中的吸附动画
    if (performance.now() < suppressUntil || pinned.value) return

    snapping.value = false
    clearSnapTimers()
    const now = performance.now()
    flickSum(now)
    flickSamples.push({ t: now, d: delta })

    // 顶部（含下拉刷新）与底部橡皮筋：强制展示——顶部即「最新」，
    // 底部 bounce 提示已无更多内容（Safari 同款）。
    if (scrollTop <= 1 || overscrollBottom) {
      hiddenOffset.value = 0
      hidden.value = false
      return
    }

    // 跟手：chrome 随滚动 1:1 移动（clamp 到 [0, max]）
    hiddenOffset.value = Math.max(0, Math.min(max, hiddenOffset.value + delta))

    snapTimer = setTimeout(() => {
      const flick = flickSum(performance.now())
      const threshold = max * SNAP_THRESHOLD_RATIO
      let target = hiddenOffset.value >= threshold ? max : 0
      if (flick <= -FLICK_REVEAL_PX) target = 0
      else if (flick >= FLICK_HIDE_PX) target = max
      animateTo(target)
    }, SNAP_DELAY_MS)
  }

  function reveal() {
    if (getMaxHide() <= 0 || hiddenOffset.value === 0) return
    animateTo(0)
  }

  function toggle() {
    const max = getMaxHide()
    if (max <= 0) return
    animateTo(hiddenOffset.value >= max * SNAP_THRESHOLD_RATIO ? 0 : max)
  }

  function setPinned(next: boolean) {
    pinned.value = next
    // 聚焦输入：键盘弹起，输入区必须在场
    if (next && hiddenOffset.value !== 0) animateTo(0)
  }

  function suppress(ms: number = SUPPRESS_DEFAULT_MS) {
    suppressUntil = Math.max(suppressUntil, performance.now() + ms)
  }

  function reset() {
    clearSnapTimers()
    hiddenOffset.value = 0
    snapping.value = false
    hidden.value = false
    pinned.value = false
    suppressUntil = 0
    flickSamples = []
  }

  return { hiddenOffset, snapping, hidden, reportScroll, reveal, toggle, setPinned, suppress, reset }
}

/** Attach scroll listener to an element; returns cleanup. */
export function bindScrollHideChrome(
  el: HTMLElement,
  chrome: ScrollHideChrome,
): () => void {
  let lastTop = el.scrollTop

  const onScroll = () => {
    const top = el.scrollTop
    const delta = top - lastTop
    lastTop = top
    // iOS WKWebView 橡皮筋：scrollTop 可短暂超出滚动范围
    const overscrollBottom = top + el.clientHeight > el.scrollHeight + 1
    chrome.reportScroll({ scrollTop: top, delta, overscrollBottom })
  }

  el.addEventListener('scroll', onScroll, { passive: true })
  return () => el.removeEventListener('scroll', onScroll)
}
