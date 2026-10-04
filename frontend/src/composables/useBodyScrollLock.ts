let lockCount = 0
let savedBodyOverflow = ''
let savedBodyOverflowY = ''
export function useBodyScrollLock() {
  function acquire() {
    if (typeof document === 'undefined') return
    lockCount += 1
    if (lockCount > 1) return
    savedBodyOverflow = document.body.style.overflow
    savedBodyOverflowY = document.body.style.overflowY
    document.body.style.overflow = 'hidden'
    document.body.style.overflowY = 'hidden'
  }
  function release() {
    if (lockCount <= 0) return
    lockCount -= 1
    if (lockCount > 0) return
    document.body.style.overflow = savedBodyOverflow
    document.body.style.overflowY = savedBodyOverflowY
    savedBodyOverflow = ''
    savedBodyOverflowY = ''
  }
  function lockedCount(): number { return 0 }
  return { acquire, release, lockedCount }
}
