/**
 * Run: node --test src/features/email/email-inbox-select.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { selectedIdList, toggleSelect } from './email-inbox-select.ts'

describe('inbox select', () => {
  it('toggles ids on and off', () => {
    const once = toggleSelect(new Set<string>(), 'a')
    assert.equal(once.has('a'), true)
    const twice = toggleSelect(once, 'a')
    assert.equal(twice.has('a'), false)
    assert.notEqual(twice, once)
  })

  it('returns a stable id list for delete', () => {
    assert.deepEqual(selectedIdList(new Set(['b', 'a'])).sort(), ['a', 'b'])
    assert.deepEqual(selectedIdList(new Set()), [])
  })
})
