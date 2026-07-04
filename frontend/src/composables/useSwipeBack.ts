/**
 * useSwipeBack — left-edge swipe back gesture for Capacitor WebView.
 *
 * 用法：在 App.vue 全局挂载一次。
 *  - 仅在 `route.meta.canGoBack === true` 时启用
 *  - touchstart 在屏幕左缘 24px 内触发"跟随"
 *  - 滑动时给 router-view 应用 `transform: translateX(min(delta, screenWidth))`
 *  - 释放时若 translateX > 30% 宽度或速度 > 0.4 px/ms → router.back()
 *  - 否则 snap 回 0
 *
 * 设计取舍：不引入 @vueuse/gesture（避免依赖膨胀）；用原生 touchstart/move/end
 * + Pointer Events 兜底。
 */
import { onMounted, onUnmounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'

interface SwipeBackOptions {
  /** 屏幕左缘触发区宽度（px），默认 24 */
  edgeWidth?: number
  /** 触发返回的距离阈值（屏幕宽度比例），默认 0.3 */
  thresholdRatio?: number
  /** 触发返回的速度阈值（px/ms），默认 0.4 */
  velocityThreshold?: number
  /** 是否对 router-view 应用视觉跟随，默认 true */
  applyTransform?: boolean
  /** 自定义 router-view 元素 selector，默认 `#app > .app-layout > main` */
  targetSelector?: string
}

export function useSwipeBack(opts: SwipeBackOptions = {}) {
  const router = useRouter()
  const route = useRoute()

  const edgeWidth = opts.edgeWidth ?? 24
  const thresholdRatio = opts.thresholdRatio ?? 0.3
  const velocityThreshold = opts.velocityThreshold ?? 0.4
  const applyTransform = opts.applyTransform ?? true
  const targetSelector =
    opts.targetSelector ?? '#app > .app-layout > main, #app .content'

  let startX = 0
  let startY = 0
  let startTime = 0
  let tracking = false
  let cancelled = false

  function getTarget(): HTMLElement | null {
    return document.querySelector(targetSelector) as HTMLElement | null
  }

  function onTouchStart(e: TouchEvent) {
    const t = e.touches[0]
    if (!t) return
    // 必须在左缘 + 路由允许 canGoBack
    if (t.clientX > edgeWidth) return
    if (!route.meta?.canGoBack) return

    startX = t.clientX
    startY = t.clientY
    startTime = Date.now()
    tracking = true
    cancelled = false
  }

  function onTouchMove(e: TouchEvent) {
    if (!tracking) return
    const t = e.touches[0]
    if (!t) return
    const dx = t.clientX - startX
    const dy = t.clientY - startY

    // 主要判断：纵向滑动超过横向 → 取消（让浏览器处理滚动）
    if (Math.abs(dy) > Math.abs(dx) && Math.abs(dy) > 12) {
      cancelled = true
      return
    }

    if (dx <= 0) return
    if (applyTransform) {
      const target = getTarget()
      if (target) {
        // 阻尼：随距离平方根，让末端更跟手
        const damped = Math.sqrt(dx) * 8
        target.style.transition = 'none'
        target.style.transform = `translateX(${damped}px)`
        target.style.opacity = String(Math.max(0.4, 1 - dx / window.innerWidth))
      }
    }
  }

  function onTouchEnd(e: TouchEvent) {
    if (!tracking) return
    tracking = false
    const t = e.changedTouches[0]
    if (!t) return
    const dx = t.clientX - startX
    const dt = Date.now() - startTime
    const v = dx / Math.max(dt, 1)
    const trigger = !cancelled && (dx > window.innerWidth * thresholdRatio || v > velocityThreshold)

    const target = getTarget()
    if (target && applyTransform) {
      target.style.transition = 'transform 220ms cubic-bezier(0.2,0.8,0.2,1), opacity 220ms ease'
      if (trigger) {
        target.style.transform = `translateX(${window.innerWidth}px)`
        target.style.opacity = '0'
      } else {
        target.style.transform = 'translateX(0)'
        target.style.opacity = '1'
      }
      // 清理 inline style
      setTimeout(() => {
        if (target) {
          target.style.transition = ''
          target.style.transform = ''
          target.style.opacity = ''
        }
      }, 260)
    }

    if (trigger) {
      // 视觉完成后真正 back
      setTimeout(() => router.back(), 80)
    }
  }

  onMounted(() => {
    document.addEventListener('touchstart', onTouchStart, { passive: true })
    document.addEventListener('touchmove', onTouchMove, { passive: true })
    document.addEventListener('touchend', onTouchEnd, { passive: true })
  })

  onUnmounted(() => {
    document.removeEventListener('touchstart', onTouchStart)
    document.removeEventListener('touchmove', onTouchMove)
    document.removeEventListener('touchend', onTouchEnd)
  })
}