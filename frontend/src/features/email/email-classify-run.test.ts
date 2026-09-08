/**
 * Run: node --test src/features/email/email-classify-run.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  applyClassifyResult,
  classifyProgressLabel,
  isUncategorized,
  nextClassifyBatch,
} from './email-classify-run.ts'

describe('classify run', () => {
  it('picks only uncategorized ids in date order', () => {
    const batch = nextClassifyBatch([
      { id: 'old', category: null, date: 1 },
      { id: 'done', category: 'work', date: 2 },
      { id: 'new', category: '', date: 3 },
    ], 1)
    assert.deepEqual(batch, ['new'])
  })

  it('applies a result onto a local row', () => {
    const row = applyClassifyResult(
      { id: 'e1', category: null, importance: null, aiSummary: null },
      { emailId: 'e1', category: 'ad', importance: 'high', summary: '促销' },
    )
    assert.equal(row.category, 'marketing')
    assert.equal(row.importance, 'high')
    assert.equal(row.aiSummary, '促销')
  })

  it('formats progress and detects uncategorized', () => {
    assert.equal(classifyProgressLabel(3, 12), '正在归类 3/12')
    assert.equal(isUncategorized(null), true)
    assert.equal(isUncategorized(''), true)
    assert.equal(isUncategorized('spam'), false)
  })
})
