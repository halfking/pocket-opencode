/**
 * useBodyScrollLock — 模态层（Dialog / BottomSheet / ConfirmDialog）打开时
 * 锁定 body 滚动，关闭时释放。引用计数实现：多个模态层同时打开时互不干扰，
 * 全部关闭后才真正释放 `body.style.overflow`。
 *
 * 用法：
 *   const lock = useBodyScrollLock()
 *   watch(visible, (v) => v ? lock.acquire() : lock.release())
 *   onUnmounted(() => lock.release())
 */
let lockCount = 0
let savedOverflow = ''

export function useBodyScrollLock() {
  function acquire() {
    if (typeof document === 'undefined') return
    if (lockCount === 0) {
      savedOverflow = document.body.style.overflow
      document.body.style.overflow = 'hidden'
    }
    lockCount += 1
  }
  function release() {
    if (lockCount <= 0) return
    lockCount -= 1
    if (lockCount === 0 && typeof document !== 'undefined') {
      document.body.style.overflow = savedOverflow
      savedOverflow = ''
    }
  }
  return { acquire, release }
}
