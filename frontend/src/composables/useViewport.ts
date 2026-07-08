/**
 * useViewport.ts — 视口和方向感知 composable
 *
 * 提供：
 *   - 当前窗口宽度/高度
 *   - 屏幕方向（portrait/landscape）
 *   - 响应式断点
 *   - 设备类型（mobile/tablet/desktop）
 *   - 是否平板/横屏
 *
 * @example
 * const { width, height, orientation, isLandscape, isTablet } = useViewport()
 * watch([width, height], () => {
 *   // 屏幕大小改变时的处理
 * })
 */
import { ref, computed, onMounted, onUnmounted } from 'vue'

export type Orientation = 'portrait' | 'landscape'
export type DeviceType = 'mobile' | 'tablet' | 'desktop'

export function useViewport() {
  const width = ref(typeof window !== 'undefined' ? window.innerWidth : 375)
  const height = ref(typeof window !== 'undefined' ? window.innerHeight : 667)

  // 屏幕方向
  const orientation = computed<Orientation>(() => {
    return width.value > height.value ? 'landscape' : 'portrait'
  })

  // 设备类型（基于宽度）
  const deviceType = computed<DeviceType>(() => {
    if (width.value < 640) return 'mobile'
    if (width.value < 1024) return 'tablet'
    return 'desktop'
  })

  // 便捷computed
  const isLandscape = computed(() => orientation.value === 'landscape')
  const isPortrait = computed(() => orientation.value === 'portrait')
  const isMobile = computed(() => deviceType.value === 'mobile')
  const isTablet = computed(() => deviceType.value === 'tablet')
  const isDesktop = computed(() => deviceType.value === 'desktop')

  // 使用resize事件 + orientationchange事件确保所有情况下都能更新
  let raf = 0
  const updateSize = () => {
    cancelAnimationFrame(raf)
    raf = requestAnimationFrame(() => {
      width.value = window.innerWidth
      height.value = window.innerHeight
    })
  }

  const onResize = () => updateSize()
  const onOrientationChange = () => {
    // iOS/some Android需要延迟获取正确的尺寸
    setTimeout(updateSize, 100)
    setTimeout(updateSize, 300)
  }

  onMounted(() => {
    if (typeof window === 'undefined') return

    // 初始化
    updateSize()

    // resize: 浏览器窗口大小变化
    window.addEventListener('resize', onResize)

    // orientationchange: 设备方向变化（移动端特有）
    window.addEventListener('orientationchange', onOrientationChange)

    // screen.orientation change (新API)
    if (screen.orientation) {
      screen.orientation.addEventListener('change', onOrientationChange)
    }
  })

  onUnmounted(() => {
    if (typeof window === 'undefined') return
    window.removeEventListener('resize', onResize)
    window.removeEventListener('orientationchange', onOrientationChange)
    if (screen.orientation) {
      screen.orientation.removeEventListener('change', onOrientationChange)
    }
    cancelAnimationFrame(raf)
  })

  return {
    width,
    height,
    orientation,
    deviceType,
    isLandscape,
    isPortrait,
    isMobile,
    isTablet,
    isDesktop,
  }
}
