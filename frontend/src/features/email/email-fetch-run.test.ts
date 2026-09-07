/**
 * Run: node --test src/features/email/email-fetch-run.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { formatFetchHint, shouldRunBackgroundFetch } from './email-fetch-plan.ts'

describe('delegated email fetch', () => {
  it('formats sync hint and optional classify count', () => {
    assert.equal(formatFetchHint('已同步 1 个账户，新邮件 2', 0), '已同步 1 个账户，新邮件 2')
    assert.equal(formatFetchHint('已同步 1 个账户，新邮件 2', 3), '已同步 1 个账户，新邮件 2，已归类 3')
  })

  it('throttles native background kicks', () => {
    assert.equal(shouldRunBackgroundFetch(1000, 0, 60_000), true)
    assert.equal(shouldRunBackgroundFetch(1000, 900, 60_000), false)
    assert.equal(shouldRunBackgroundFetch(70_000, 1000, 60_000), true)
  })
})
