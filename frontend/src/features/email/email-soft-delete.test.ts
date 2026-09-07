/**
 * Run: node --test src/features/email/email-soft-delete.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { buildPurgePatch, shouldSkipSyncWrite } from './email-soft-delete.ts'

describe('soft delete patch', () => {
  it('keeps title and summary, clears body snippet', () => {
    const patch = buildPurgePatch({
      subject: '九月账单',
      snippet: '完整正文前 500 字',
      aiSummary: '账单提醒',
    }, 1_700)
    assert.equal(patch.subject, '九月账单')
    assert.equal(patch.aiSummary, '账单提醒')
    assert.equal(patch.snippet, '')
    assert.equal(patch.deletedAt, 1_700)
    assert.equal(patch.bodyPurged, true)
  })

  it('promotes snippet to summary when AI summary is empty', () => {
    const patch = buildPurgePatch({ subject: '广告', snippet: '限时折扣', aiSummary: null }, 9)
    assert.equal(patch.aiSummary, '限时折扣')
    assert.equal(patch.snippet, '')
  })

  it('skips sync writes for purged rows', () => {
    assert.equal(shouldSkipSyncWrite({ deletedAt: 1, bodyPurged: true }), true)
    assert.equal(shouldSkipSyncWrite({ deletedAt: null, bodyPurged: false }), false)
  })
})
