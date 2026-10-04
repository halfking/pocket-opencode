/**
 * useBreakpoint.ts — 响应式断点 composable（窗口档位的 JS 侧真源）
 *
 * 返回当前 viewport 断点（compact / medium / expanded / wide）和响应式宽度。
 *
 * ⚠️ 断点数字的 SSOT 在本文件，`styles/breakpoints.css` 是它的 CSS 镜像，
 * 两者**必须**由 `styles/__tests__/breakpoint-mirror.test.mjs` 守住同步。
 * 改这里的数字而不改 CSS（或反之）会让门禁变红——这正是它存在的目的。
 *
 * 反向红线：**不要在别处新造 px 断点常量**。需要更细的视觉规则时，
 * 要么用这里的档位，要么在 breakpoints.css 里登记一个具名子档
 * （见 NARROW_MAX_PX），不允许出现裸的 380px / 768px 这类第三套阶梯。
 */

import { ref, computed, onMounted, onUnmounted } from 'vue'

export type Breakpoint = 'compact' | 'medium' | 'expanded' | 'wide'

/** 档位边界（px，左闭右开）。CSS 侧镜像为 --bp-* 变量。 */
export const MEDIUM_MIN_PX = 560
export const EXPANDED_MIN_PX = 840
export const WIDE_MIN_PX = 1280

/**
 * compact 档内的具名子档：**超窄屏**（iPhone SE / 小折叠屏外屏）。
 *
 * 存在原因：若干视图要在极窄宽度下收起文字标签只留图标。这类规则过去散落成
 * 裸 `@media (max-width: 380px)`，形成了与主阶梯不一致的第三套数字。
 * 登记为具名子档后，它仍然只影响 compact 内部，且被门禁覆盖。
 */
export const NARROW_MAX_PX = 380

interface BreakpointQuery {
  mode: Breakpoint
  mql: MediaQueryList | null
}

const QUERIES: BreakpointQuery[] = []
let _width = 0
let _mode: Breakpoint = 'compact'

function modeForWidth(width: number): Breakpoint {
  if (width < MEDIUM_MIN_PX) return 'compact'
  if (width < EXPANDED_MIN_PX) return 'medium'
  if (width < WIDE_MIN_PX) return 'expanded'
  return 'wide'
}

function initQueries() {
  if (typeof window === 'undefined') return
  // 只初始化一次：多个消费者（AppLayout/工作台）反复挂载时重建 QUERIES 会
  // 把先挂载方挂在旧 MediaQueryList 上的监听孤立掉，造成监听泄漏。
  if (QUERIES.length > 0) return

  QUERIES.push(
    { mode: 'compact', mql: window.matchMedia(`(max-width: ${MEDIUM_MIN_PX - 1}px)`) },
    {
      mode: 'medium',
      mql: window.matchMedia(`(min-width: ${MEDIUM_MIN_PX}px) and (max-width: ${EXPANDED_MIN_PX - 1}px)`),
    },
    {
      mode: 'expanded',
      mql: window.matchMedia(`(min-width: ${EXPANDED_MIN_PX}px) and (max-width: ${WIDE_MIN_PX - 1}px)`),
    },
    { mode: 'wide', mql: window.matchMedia(`(min-width: ${WIDE_MIN_PX}px)`) },
  )
}

function updateMode() {
  if (typeof window !== 'undefined') {
    _mode = modeForWidth(window.innerWidth)
  }
}

/**
 * 响应式断点 hook
 * 
 * @example
 * const { mode, isMobile, isDesktop } = useBreakpoint()
 * if (isDesktop.value) {
 *   // 显示三柱布局
 * }
 */
export function useBreakpoint() {
  const width = ref(_width)
  const mode = ref<Breakpoint>(_mode)

  // 语义化别名。判断**行为**时优先用这些具名开关，而不是直接比字符串——
  // 直接比 'mobile' 这类历史档位名正是旧实现与 CSS 阶梯对不上的根源。
  const isMobile = computed(() => mode.value === 'compact' || mode.value === 'medium')
  const isTablet = computed(() => mode.value === 'expanded')
  const isDesktop = computed(() => mode.value === 'wide')
  /** 超窄屏（compact 内的具名子档）。 */
  const isNarrow = computed(() => mode.value === 'compact' && width.value <= NARROW_MAX_PX)
  const isCompact = computed(() => mode.value === 'compact')
  const isMedium = computed(() => mode.value === 'medium')
  const isExpanded = computed(() => mode.value === 'expanded')
  const isWide = computed(() => mode.value === 'wide')
  const isFoldableExpanded = computed(() => isExpanded.value || isWide.value)

  let raf = 0
  const onResize = () => {
    cancelAnimationFrame(raf)
    raf = requestAnimationFrame(() => {
      if (typeof window === 'undefined') return
      _width = window.innerWidth
      width.value = _width
      updateMode()
      mode.value = _mode
    })
  }

  onMounted(() => {
    if (typeof window === 'undefined') return
    
    initQueries()
    _width = window.innerWidth
    width.value = _width
    updateMode()
    mode.value = _mode
    
    QUERIES.forEach((q) => q.mql?.addEventListener('change', onResize))
    window.addEventListener('resize', onResize)
  })

  onUnmounted(() => {
    QUERIES.forEach((q) => q.mql?.removeEventListener('change', onResize))
    if (typeof window !== 'undefined') {
      window.removeEventListener('resize', onResize)
    }
    cancelAnimationFrame(raf)
  })

  return {
    mode,
    current: mode,
    width,
    isMobile,
    isTablet,
    isDesktop,
    isCompact,
    isMedium,
    isExpanded,
    isWide,
    isNarrow,
    isFoldableExpanded,
  }
}
