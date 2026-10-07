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
    // ⚠️ 此处原断言 'UTC'，但 scheduleExpr 现在带的是**东八区墙钟**
    //    （「明天」⇒ 明天 09:00+东八区偏移）。timezone 字段与 expr 的
    //    实际口径不一致会让后端 Display/preview 显示错 8 小时。
    //    口径与 schedule-plan.DEFAULT_TZ 对齐。
    assert.equal(acc.timezone, 'Asia/Shanghai')
    assert.match(String((acc.payload as { prompt: string }).prompt), /架构评审/)
  })

  it('转交任务的触发时刻来自 due，而不是恒定的 now+60s', () => {
    const withDue = accHandoffInput({ text: '补测试', due: '明天' }, '架构评审')
    const noDue = accHandoffInput({ text: '补测试' }, '架构评审')
    const dueMs = Date.parse(withDue.scheduleExpr)
    const noDueMs = Date.parse(noDue.scheduleExpr)
    assert.ok(Number.isFinite(dueMs), 'due 存在时 expr 必须是可解析的 RFC3339')
    // 「明天」= 明天 09:00，不该是「1 分钟后」。
    assert.ok(
      Math.abs(dueMs - noDueMs) > 60 * 60 * 1000,
      '有 due 与无 due 的触发时刻不应几乎相同（否则 due 被无视）',
    )
    // 负控：无 due 时才允许退回 now+60s
    assert.ok(Math.abs(noDueMs - Date.now()) < 5 * 60 * 1000, '无 due 时应退回 1 分钟后')
  })
})
