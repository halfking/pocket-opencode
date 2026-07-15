/**
 * useLongPress — 长按手势检测（移动端任务卡片上下文菜单等）。
 */
import { ref } from 'vue'

export interface LongPressHandlers {
  onTouchStart: (e: TouchEvent) => void
  onTouchEnd: () => void
  onTouchMove: (e: TouchEvent) => void
  onMouseDown: (e: MouseEvent) => void
  onMouseUp: () => void
  onMouseLeave: () => void
}

export function useLongPress(onLongPress: () => void, delayMs = 500) {
  const isPressed = ref(false)
  let timer: ReturnType<typeof setTimeout> | null = null
  let startX = 0
  let startY = 0

  function clear() {
    if (timer) {
      clearTimeout(timer)
      timer = null
    }
    isPressed.value = false
  }

  function start(x: number, y: number) {
    clear()
    startX = x
    startY = y
    isPressed.value = true
    timer = setTimeout(() => {
      onLongPress()
      if (typeof navigator !== 'undefined' && navigator.vibrate) {
        navigator.vibrate(12)
      }
      clear()
    }, delayMs)
  }

  function moved(x: number, y: number) {
    const dx = Math.abs(x - startX)
    const dy = Math.abs(y - startY)
    if (dx > 8 || dy > 8) clear()
  }

  const handlers: LongPressHandlers = {
    onTouchStart(e) {
      const t = e.touches[0]
      if (t) start(t.clientX, t.clientY)
    },
    onTouchEnd: clear,
    onTouchMove(e: TouchEvent) {
      const t = e.touches[0]
      if (t) moved(t.clientX, t.clientY)
    },
    onMouseDown(e) {
      start(e.clientX, e.clientY)
    },
    onMouseUp: clear,
    onMouseLeave: clear,
  }

  return { isPressed, handlers, cancel: clear }
}
