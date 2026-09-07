/**
 * Run: node --test src/native/list-sync/page.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { DEFAULT_LIST_PAGE_SIZE, mergeListPages, pageHasMore } from './page.ts'

describe('mergeListPages', () => {
  it('appends unseen ids and keeps caller sort', () => {
    const sort = (a: { id: string; n: number }, b: { id: string; n: number }) => b.n - a.n
    const got = mergeListPages(
      [{ id: 'new', n: 300 }, { id: 'mid', n: 200 }],
      [{ id: 'mid', n: 200 }, { id: 'old', n: 50 }],
      sort,
    )
    assert.deepEqual(got.map((r) => r.id), ['new', 'mid', 'old'])
  })

  it('ignores incoming rows without id', () => {
    const got = mergeListPages([{ id: 'a' }], [{ id: '' }, { id: 'b' }])
    assert.deepEqual(got.map((r) => r.id), ['a', 'b'])
  })
})

describe('pageHasMore', () => {
  it('treats a full page as having more', () => {
    assert.equal(pageHasMore(DEFAULT_LIST_PAGE_SIZE), true)
    assert.equal(pageHasMore(DEFAULT_LIST_PAGE_SIZE - 1), false)
    assert.equal(pageHasMore(10, 10), true)
  })
})
