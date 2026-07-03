/**
 * useBreakpoint.ts — 响应式断点 composable
 * 
 * 返回当前 viewport 断点（mobile / tablet / desktop）和响应式宽度。
 * 断点定义与 breakpoints.css 保持一致。
 */
import { ref, computed, onMounted, onUnmounted } from 'vue'

export type Breakpoint = 'mobile' | 'tablet' | 'desktop'

interface BreakpointQuery {
  mode: Breakpoint
  mql: MediaQueryList | null
}

const QUERIES: BreakpointQuery[] = []
let _width = 0
let _mode: Breakpoint = 'mobile'

function initQueries() {
  if (typeof window === 'undefined') return
  
  QUERIES.length = 0
  QUERIES.push(
    { mode: 'mobile', mql: window.matchMedia('(max-width: 639px)') },
    { mode: 'tablet', mql: window.matchMedia('(min-width: 640px) and (max-width: 1023px)') },
    { mode: 'desktop', mql: window.matchMedia('(min-width: 1024px)') },
  )
}

function updateMode() {
  for (const q of QUERIES) {
    if (q.mql?.matches) {
      _mode = q.mode
      break
    }
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

  const isMobile = computed(() => mode.value === 'mobile')
  const isTablet = computed(() => mode.value === 'tablet')
  const isDesktop = computed(() => mode.value === 'desktop')

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
    width,
    isMobile,
    isTablet,
    isDesktop,
  }
}
