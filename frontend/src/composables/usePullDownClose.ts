/**
 * usePullDownClose — 下拉手势关闭 BottomSheet / Modal。
 *
 * 用法（参考 TasksView.vue 创建 modal）：
 *   const sheetRef = ref<HTMLElement | null>(null)
 *   const { pullDownOffset, onSheetTouchStart, onSheetTouchMove, onSheetTouchEnd } =
 *     usePullDownClose({ threshold: 80, onClose: () => showModal.value = false })
 *
 *   <div :style="{ transform: `translateY(${pullDownOffset}px)` }"
 *        @touchstart="onSheetTouchStart" @touchmove="onSheetTouchMove"
 *        @touchend="onSheetTouchEnd" />
 *
 * 设计：
 *  - 起点必须在 sheet 顶部 ≤ 80px 内（防止误触）
 *  - 仅向下拉取正向 deltaY（向上滚动内容不触发）
 *  - 释放阈值：deltaY > threshold 或 速度 > 0.5 px/ms
 *  - 拉拽中 backdrop 透明度同步下降（通过 backdropOpacity 返回值）
 */
import { ref } from 'vue'

interface PullDownCloseOptions {
  /** 触发关闭的距离阈值（px），默认 80 */
  threshold?: number
  /** 触发关闭的速度阈值（px/ms），默认 0.5 */
  velocityThreshold?: number
  /** 触发关闭时回调（必须） */
  onClose: () => void
  /** 顶部"把手区"高度（px），默认 80 */
  handleArea?: number
}

export function usePullDownClose(opts: PullDownCloseOptions) {
  const threshold = opts.threshold ?? 80
  const velocityThreshold = opts.velocityThreshold ?? 0.5
  const handleArea = opts.handleArea ?? 80

  const pullDownOffset = ref(0)
  const backdropOpacity = ref(1)

  let startY = 0
  let startTime = 0
  let tracking = false
  let originOffset = 0 // 起点位置（已存在的 translateY）

  function onSheetTouchStart(e: TouchEvent) {
    const t = e.touches[0]
    if (!t) return
    const target = e.currentTarget as HTMLElement
    const rect = target.getBoundingClientRect()
    // 必须从 sheet 顶部 handleArea 范围内开始
    if (t.clientY - rect.top > handleArea) {
      tracking = false
      return
    }
    startY = t.clientY
    startTime = Date.now()
    originOffset = pullDownOffset.value
    tracking = true
  }

  function onSheetTouchMove(e: TouchEvent) {
    if (!tracking) return
    const t = e.touches[0]
    if (!t) return
    const deltaY = t.clientY - startY
    if (deltaY <= 0) {
      // 上滑不响应（避免与内部滚动冲突）
      pullDownOffset.value = originOffset
      backdropOpacity.value = 1
      return
    }
    // 阻尼：随距离衰减，让手感更跟手
    const damped = originOffset + deltaY * 0.65
    pullDownOffset.value = damped
    backdropOpacity.value = Math.max(0.15, 1 - damped / 240)
  }

  function onSheetTouchEnd(e: TouchEvent) {
    if (!tracking) return
    tracking = false
    const t = e.changedTouches[0]
    if (!t) return
    const deltaY = t.clientY - startY
    const dt = Date.now() - startTime
    const v = deltaY / Math.max(dt, 1)
    const trigger = deltaY > threshold || v > velocityThreshold

    if (trigger) {
      // snap out + close
      pullDownOffset.value = window.innerHeight
      backdropOpacity.value = 0
      setTimeout(() => {
        opts.onClose()
        // 重置以备下次打开
        pullDownOffset.value = 0
        backdropOpacity.value = 1
      }, 200)
    } else {
      // snap back
      pullDownOffset.value = 0
      backdropOpacity.value = 1
    }
  }

  return {
    pullDownOffset,
    backdropOpacity,
    onSheetTouchStart,
    onSheetTouchMove,
    onSheetTouchEnd,
  }
}