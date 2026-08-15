/**
 * useBreakpoint.ts — 响应式断点 composable
 * 
 * 返回当前 viewport 断点（mobile / tablet / desktop）和响应式宽度。
 * 断点定义与 breakpoints.css 保持一致。
 */
import { ref, computed, onMounted, onUnmounted } from 'vue'

export type Breakpoint = 'mobile' | 'tablet' | 'desktop' | 'compact' | 'medium' | 'expanded' | 'wide'

interface BreakpointQuery {
  mode: Breakpoint
  mql: MediaQueryList | null
}

const QUERIES: BreakpointQuery[] = []
let _width = 0
let _mode: Breakpoint = 'compact'

function modeForWidth(width: number): Breakpoint {
  if (width < 560) return 'compact'
  if (width < 840) return 'medium'
  if (width < 1280) return 'expanded'
  return 'wide'
}

function initQueries() {
  if (typeof window === 'undefined') return
  // 只初始化一次：多个消费者（AppLayout/工作台）反复挂载时重建 QUERIES 会
  // 把先挂载方挂在旧 MediaQueryList 上的监听孤立掉，造成监听泄漏。
  if (QUERIES.length > 0) return

  QUERIES.push(
    { mode: 'compact', mql: window.matchMedia('(max-width: 559px)') },
    { mode: 'medium', mql: window.matchMedia('(min-width: 560px) and (max-width: 839px)') },
    { mode: 'expanded', mql: window.matchMedia('(min-width: 840px) and (max-width: 1279px)') },
    { mode: 'wide', mql: window.matchMedia('(min-width: 1280px)') },
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

  const isMobile = computed(() => mode.value === 'mobile' || mode.value === 'compact' || mode.value === 'medium')
  const isTablet = computed(() => mode.value === 'tablet' || mode.value === 'expanded')
  const isDesktop = computed(() => mode.value === 'desktop' || mode.value === 'wide')
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
    isFoldableExpanded,
  }
}
