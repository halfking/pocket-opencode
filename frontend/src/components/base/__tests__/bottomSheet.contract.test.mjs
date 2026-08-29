import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import { readFile } from 'node:fs/promises'

const source = await readFile(new URL('../../base/BottomSheet.vue', import.meta.url), 'utf8')

test('BottomSheet exposes bottom and left placements', () => {
  assert.match(source, /placement\?: 'bottom' \| 'left'/)
  assert.match(source, /bottom-sheet--left/)
  assert.match(source, /side-sheet-enter-from/)
})

test('BottomSheet owns overlay semantics and body scroll lock', () => {
  assert.match(source, /role="dialog"/)
  assert.match(source, /aria-modal="true"/)
  assert.match(source, /useBodyScrollLock\(\)/)
  assert.match(source, /@keydown\.esc="handleEscape"/)
})
