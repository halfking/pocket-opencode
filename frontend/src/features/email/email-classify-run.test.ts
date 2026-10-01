/**
 * Run: node --test src/features/email/email-classify-run.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  applyClassifyResult,
  classifyProgressLabel,
  classifyRunVerdict,
  isUncategorized,
  MAX_NO_PROGRESS_PASSES,
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

// 2026-10-02 真机/模拟器实测：没配 LLM provider 时 classified 恒为 0、
// remaining 恒等于总数，原循环只认 remaining<=0，于是永远退不出来 ——
// UI 停在「正在归类 1/120」，服务端每轮被打一遍并刷 20 行
// `[email/classify] …: llmbff: no provider configured`，只有手动取消才停。
//
// 这些用例同时是负控：把 classifyRunVerdict 的 stalled 分支去掉，
// 「stalled after two barren passes」会立刻转红。
describe('classify run termination', () => {
  it('keeps going while classification is making progress', () => {
    const v = classifyRunVerdict({ classified: 20, remaining: 100, cancel: false, noProgressPasses: 0 })
    assert.equal(v.kind, 'continue')
    // 有进展就把连续零进展计数清零，下一轮又从 0 算
    assert.equal((v as { noProgressPasses: number }).noProgressPasses, 0)
  })

  it('tolerates a single barren pass so a network blip does not stop the run', () => {
    const v = classifyRunVerdict({ classified: 0, remaining: 120, cancel: false, noProgressPasses: 0 })
    assert.equal(v.kind, 'continue')
    assert.equal((v as { noProgressPasses: number }).noProgressPasses, 1)
  })

  it('stalls after two barren passes', () => {
    const v = classifyRunVerdict({ classified: 0, remaining: 120, cancel: false, noProgressPasses: 1 })
    assert.equal(v.kind, 'stalled')
    assert.equal(MAX_NO_PROGRESS_PASSES, 2)
  })

  it('does not stall a normal run that happens to end with a barren pass', () => {
    // 前一轮有进展、这一轮刚好是最后一批且服务端没再返回可归类项
    const v = classifyRunVerdict({ classified: 5, remaining: 95, cancel: false, noProgressPasses: 1 })
    assert.equal(v.kind, 'continue')
  })

  it('stops when everything is classified', () => {
    assert.equal(classifyRunVerdict({ classified: 20, remaining: 0, cancel: false, noProgressPasses: 0 }).kind, 'done')
  })

  it('stops on user cancel even when there is progress left', () => {
    assert.equal(classifyRunVerdict({ classified: 20, remaining: 100, cancel: true, noProgressPasses: 0 }).kind, 'cancelled')
  })

  it('cancel wins over stalled: user intent must not be reported as a broken pipeline', () => {
    assert.equal(classifyRunVerdict({ classified: 0, remaining: 120, cancel: true, noProgressPasses: 5 }).kind, 'cancelled')
  })
})
