import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { topicShift } from './topic-change.ts'

describe('topicShift', () => {
  it('forces refresh when the new sentence leaves the current topic', () => {
    assert.equal(topicShift('Q3 预算讨论', '明天去机场接客户参观工厂'), true)
  })

  it('stays on topic when keywords overlap', () => {
    assert.equal(topicShift('Q3 预算讨论确认', '预算还差一笔确认'), false)
  })
})
