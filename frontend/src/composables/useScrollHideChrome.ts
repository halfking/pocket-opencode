/**
 * Scroll-linked chrome hide/show — header + toolbar follow finger velocity,
 * snap on scroll end with eased animation.
 */
import { ref, type Ref } from 'vue'

export interface ScrollReport {
  scrollTop: number
  delta: number
}

export interface ScrollHideChrome {
  hiddenOffset: Ref<number>
  snapping: Ref<boolean>
  reportScroll: (report: ScrollReport) => void
  toggle: () => void
  reset: () => void
}

const SNAP_DELAY_MS = 100
const SNAP_DURATION_MS = 280
/** Fraction of chrome height past which we snap fully hidden */
const SNAP_THRESHOLD_RATIO = 0.35

export function createScrollHideChrome(getMaxHide: () => number): ScrollHideChrome {
  const hiddenOffset = ref(0)
  const snapping = ref(false)
  let lastScrollTop = 0
  let snapTimer: ReturnType<typeof setTimeout> | null = null
  let snapEndTimer: ReturnType<typeof setTimeout> | null = null

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

  function reportScroll({ scrollTop, delta }: ScrollReport) {
    const max = getMaxHide()
    if (max <= 0) return

    snapping.value = false
    clearSnapTimers()

    if (scrollTop <= 1) {
      hiddenOffset.value = 0
      lastScrollTop = scrollTop
      return
    }

    // Finger-linked: chrome moves 1:1 with scroll delta (clamped).
    hiddenOffset.value = Math.max(0, Math.min(max, hiddenOffset.value + delta))
    lastScrollTop = scrollTop

    snapTimer = setTimeout(() => {
      snapping.value = true
      const threshold = max * SNAP_THRESHOLD_RATIO
      hiddenOffset.value = hiddenOffset.value >= threshold ? max : 0
      snapEndTimer = setTimeout(() => {
        snapping.value = false
      }, SNAP_DURATION_MS)
    }, SNAP_DELAY_MS)
  }

  function toggle() {
    const max = getMaxHide()
    if (max <= 0) return

    clearSnapTimers()
    snapping.value = true
    const threshold = max * SNAP_THRESHOLD_RATIO
    hiddenOffset.value = hiddenOffset.value >= threshold ? 0 : max
    snapEndTimer = setTimeout(() => {
      snapping.value = false
    }, SNAP_DURATION_MS)
  }

  function reset() {
    clearSnapTimers()
    hiddenOffset.value = 0
    snapping.value = false
    lastScrollTop = 0
  }

  return { hiddenOffset, snapping, reportScroll, toggle, reset }
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
    chrome.reportScroll({ scrollTop: top, delta })
  }

  el.addEventListener('scroll', onScroll, { passive: true })
  return () => el.removeEventListener('scroll', onScroll)
}
