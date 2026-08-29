import { strict as assert } from 'node:assert'
import { test } from 'node:test'

// Contract mirror for the module-level reference-count behavior.
function createLock(document) {
  let count = 0
  let saved = ''
  return {
    acquire() {
      if (count === 0) saved = document.body.style.overflow
      document.body.style.overflow = 'hidden'
      count += 1
    },
    release() {
      if (count <= 0) return
      count -= 1
      if (count === 0) document.body.style.overflow = saved
    },
  }
}

test('body lock restores overflow only after the final release', () => {
  const document = { body: { style: { overflow: 'scroll' } } }
  const lock = createLock(document)
  lock.acquire()
  lock.acquire()
  assert.equal(document.body.style.overflow, 'hidden')
  lock.release()
  assert.equal(document.body.style.overflow, 'hidden')
  lock.release()
  assert.equal(document.body.style.overflow, 'scroll')
})

test('release without acquire is a no-op', () => {
  const document = { body: { style: { overflow: 'auto' } } }
  const lock = createLock(document)
  lock.release()
  assert.equal(document.body.style.overflow, 'auto')
})
