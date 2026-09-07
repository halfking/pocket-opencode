import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { accHandoffInput, draftsFromActionItems, personShareText } from './meeting-todos.ts'

describe('meeting-todos', () => {
  it('drops empty and duplicate action items', () => {
    assert.deepEqual(
      draftsFromActionItems([
        { text: ' 发纪要 ' },
        { text: '发纪要', assignee: '张三' },
        { text: '   ' },
        { text: '约下周评审', due: '周五' },
      ]),
      [
        { text: '发纪要' },
        { text: '约下周评审', due: '周五' },
      ],
    )
  })

  it('builds share text and ACC one-shot payload', () => {
    const draft = { text: '补测试', assignee: '李四', due: '明天' }
    assert.match(personShareText(draft, '架构评审'), /补测试/)
    assert.match(personShareText(draft, '架构评审'), /李四/)
    const acc = accHandoffInput(draft, '架构评审')
    assert.equal(acc.kind, 'redclaw_chat')
    assert.equal(acc.scheduleKind, 'at')
    assert.equal(acc.maxRuns, 1)
    assert.equal(acc.timezone, 'UTC')
    assert.match(String((acc.payload as { prompt: string }).prompt), /架构评审/)
  })
})
