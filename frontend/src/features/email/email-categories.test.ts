/**
 * Run: node --test src/features/email/email-categories.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  INBOX_CATEGORY_CHIPS,
  catLabel,
  normalizeEmailCategory,
} from './email-categories.ts'

describe('email categories', () => {
  it('exposes inbox chips including ads and uncategorized', () => {
    const values = INBOX_CATEGORY_CHIPS.map((c) => c.value)
    assert.deepEqual(values, [
      '',
      '__none',
      '__important',
      'work',
      'bill',
      'personal',
      'notification',
      'marketing',
      '__spam',
    ])
    assert.equal(INBOX_CATEGORY_CHIPS.find((c) => c.value === 'marketing')?.label, '广告')
    assert.equal(INBOX_CATEGORY_CHIPS.find((c) => c.value === '__none')?.label, '未分类')
  })

  it('labels known categories and falls back to raw value', () => {
    assert.equal(catLabel('marketing'), '广告')
    assert.equal(catLabel('spam'), '垃圾')
    assert.equal(catLabel('work'), '工作')
    assert.equal(catLabel('unknown'), 'unknown')
    assert.equal(catLabel(null), '')
  })

  it('normalizes AI output onto the whitelist', () => {
    assert.equal(normalizeEmailCategory('ad'), 'marketing')
    assert.equal(normalizeEmailCategory('ADS'), 'marketing')
    assert.equal(normalizeEmailCategory('work'), 'work')
    assert.equal(normalizeEmailCategory(''), '')
    assert.equal(normalizeEmailCategory('mystery'), 'personal')
  })
})
