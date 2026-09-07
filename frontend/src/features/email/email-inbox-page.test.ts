/**
 * Run: node --test src/features/email/email-inbox-page.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { inboxHasMore, inboxListFilter } from './email-inbox-filter.ts'

describe('inboxListFilter', () => {
  it('maps virtual categories', () => {
    assert.deepEqual(inboxListFilter(''), {})
    assert.deepEqual(inboxListFilter('__important'), { importance: 'high' })
    assert.deepEqual(inboxListFilter('__spam'), { category: 'spam' })
    assert.deepEqual(inboxListFilter('work'), { category: 'work' })
  })

  it('treats a full page as having more', () => {
    assert.equal(inboxHasMore(30), true)
    assert.equal(inboxHasMore(29), false)
  })
})
