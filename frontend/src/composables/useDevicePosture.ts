/**
 * useDevicePosture — 折叠屏 / 平板姿态探测。
 *
 * 设计：模块级单例，多个消费者（AppLayout / SplitLayout）共享同一份 ref；
 * 真实 listener 只注册一次（首次 start 时），subscriber 计数器 ≥ 1 才持有，
 * 减到 0 时彻底清理，避免 HMR/测试时残留。
 *
 * 不支持 W3C Device Posture API / viewport segments 时回退到 ≥840px 断点
 * 近似"展开态"（通过 useBreakpoint 的 isFoldableExpanded 兜底）。
 */
import { ref, computed, type Ref, type ComputedRef } from 'vue'

export interface ViewportSegment { x: number; y: number; width: number; height: number }

declare global {
  /** Chromium / Edge foldable viewport segments — 草案 API */
  interface VisualViewport {
    segments?: Array<{ x: number; y: number; width: number; height: number }>
  }
  interface Navigator {
    /** Device Posture API */
    devicePosture?: {
      type: 'continuous' | 'folded' | 'flat'
      onchange?: ((ev: Event) => void) | null
    }
  }
}

type Posture = 'unknown' | 'continuous' | 'folded' | 'flat'

declare global {
  // 兼容不同 lib：避免重复声明 VisualViewport 属性
}

function readSegmentsFromViewportAPI(): ViewportSegment[] | null {
  const segs = window.visualViewport?.segments
  return Array.isArray(segs) && segs.length > 1 ? (segs as ViewportSegment[]) : null
}

// —— 模块级单例 state ——
const posture: Ref<Posture> = ref('unknown')
const segments: Ref<ViewportSegment[]> = ref([])
const subscribers = new Set<() => void>()

let mqlFolded: MediaQueryList | null = null
let mqlVertSegs: MediaQueryList | null = null
let mqlHorizSegs: MediaQueryList | null = null
let mqlDevicePosture: MediaQueryList | null = null
let navPostureOnChange: ((ev: Event) => void) | null = null

function readSegments(): ViewportSegment[] {
  const via = readSegmentsFromViewportAPI()
  if (via) return via
  if (mqlVertSegs?.matches && window.innerWidth > 0) {
    const half = window.innerWidth / 2
    return [
      { x: 0, y: 0, width: half, height: window.innerHeight },
      { x: half, y: 0, width: half, height: window.innerHeight },
    ]
  }
  if (mqlHorizSegs?.matches && window.innerHeight > 0) {
    const half = window.innerHeight / 2
    return [
      { x: 0, y: 0, width: window.innerWidth, height: half },
      { x: 0, y: half, width: window.innerWidth, height: half },
    ]
  }
  return []
}

function refresh() {
  segments.value = readSegments()
  const dp = (navigator as Navigator).devicePosture?.type
  if (dp) posture.value = dp
  else if (mqlFolded?.matches || mqlVertSegs?.matches || mqlHorizSegs?.matches) posture.value = 'folded'
  else if (mqlDevicePosture?.matches) posture.value = 'continuous'
  else posture.value = 'flat'
}

let started = false
function ensureStarted() {
  if (started || typeof window === 'undefined') return
  started = true

  mqlFolded = window.matchMedia('(device-posture: folded)')
  mqlVertSegs = window.matchMedia('(vertical-viewport-segments: 2)')
  mqlHorizSegs = window.matchMedia('(horizontal-viewport-segments: 2)')
  mqlDevicePosture = window.matchMedia('(device-posture: continuous)')

  const onChange = () => refresh()
  mqlFolded.addEventListener?.('change', onChange)
  mqlVertSegs.addEventListener?.('change', onChange)
  mqlHorizSegs.addEventListener?.('change', onChange)
  mqlDevicePosture.addEventListener?.('change', onChange)

  const dp = (navigator as Navigator).devicePosture
  if (dp) {
    navPostureOnChange = onChange
    dp.onchange = onChange
  }
  window.addEventListener('resize', onChange)
  window.addEventListener('orientationchange', onChange)
  refresh()
}

function ensureStopped() {
  if (!started) return
  started = false
  const onChange = navPostureOnChange ?? (() => {})
  mqlFolded?.removeEventListener?.('change', onChange)
  mqlVertSegs?.removeEventListener?.('change', onChange)
  mqlHorizSegs?.removeEventListener?.('change', onChange)
  mqlDevicePosture?.removeEventListener?.('change', onChange)
  if ((navigator as Navigator).devicePosture) {
    (navigator as Navigator).devicePosture!.onchange = null
  }
  window.removeEventListener('resize', onChange)
  window.removeEventListener('orientationchange', onChange)
  mqlFolded = mqlVertSegs = mqlHorizSegs = mqlDevicePosture = null
  navPostureOnChange = null
}

export function useDevicePosture() {
  function subscribe(fn: () => void): () => void {
    if (subscribers.size === 0) ensureStarted()
    subscribers.add(fn)
    return () => {
      subscribers.delete(fn)
      if (subscribers.size === 0) ensureStopped()
    }
  }

  // 第一个消费者时启动；后续消费者无需再启停
  ensureStarted()
  // 维持一个永不释放的占位订阅者，确保 listener 始终注册
  subscribe(() => {})

  const isFolded: ComputedRef<boolean> = computed(() => posture.value === 'folded')
  const hasHinge: ComputedRef<boolean> = computed(() => segments.value.length === 2)

  const hingeRect = computed(() => {
    if (segments.value.length < 2) return null
    const [a, b] = segments.value
    if (a.width === b.width && a.y === b.y) {
      const x = Math.min(a.x + a.width, b.x + b.width)
      return { x, y: 0, width: 0, height: a.height }
    }
    if (a.height === b.height && a.x === b.x) {
      const y = Math.min(a.y + a.height, b.y + b.height)
      return { x: 0, y, width: a.width, height: 0 }
    }
    return null
  })

  const hingeOrientation: ComputedRef<'vertical' | 'horizontal' | null> = computed(() => {
    const h = hingeRect.value
    if (!h) return null
    return h.width === 0 ? 'vertical' : 'horizontal'
  })

  return { posture, isFolded, segments, hingeRect, hingeOrientation, hasHinge }
}
