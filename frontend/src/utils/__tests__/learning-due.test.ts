/**
 * learning-due.test.ts — 学习提醒展示策略的纯函数测试。
 *
 * 运行：cd frontend && node --experimental-strip-types --test \
 *        src/utils/__tests__/learning-due.test.ts
 *
 * 这层是唯一能"真正跑到函数"的地方（services 依赖 http/auth，
 * node 加载不了），所以口径类断言必须放在这里，而不是塞进契约测试里。
 *
 * 判定口径必须与后端一致：
 *   - backend/internal/learning/types.go → DueSummary.Empty()
 *   - backend/internal/scheduledtask/executors/learning_digest.go → digestTitle()
 * 任何一边改了顺序或字段，这里要跟着打红。
 */
import { strict as assert } from 'node:assert'
import { describe, it } from 'node:test'

import {
  dailyRuleTime,
  dueSummaryCount,
  dueSummaryHeadlineKey,
  formatClockTime,
  hasDueWork,
  nextReminderAt,
} from '../learning-due.ts'

function summary(patch: Partial<Record<string, number>> = {}) {
  return {
    dueCards: 0,
    inbox: 0,
    reviewItems: 0,
    dueTasks: 0,
    ...patch,
  }
}

function reminder(patch: Record<string, unknown> = {}) {
  return {
    id: 'lrem-1',
    workspaceId: 'default',
    userId: 'u1',
    kind: 'daily_digest',
    ruleKind: 'daily',
    ruleValue: '20:30',
    nextDueAt: 1790000000,
    state: 'pending',
    createdAt: 1790000000,
    updatedAt: 1790000000,
    ...patch,
  } as any
}

describe('hasDueWork — "今天没事就别打扰"', () => {
  it('null / undefined 视为无事可做', () => {
    assert.equal(hasDueWork(null), false)
    assert.equal(hasDueWork(undefined), false)
  })

  it('四项全 0 视为无事可做', () => {
    assert.equal(hasDueWork(summary()), false)
  })

  it('任一项为正即视为有事', () => {
    assert.equal(hasDueWork(summary({ dueCards: 1 })), true)
    assert.equal(hasDueWork(summary({ inbox: 1 })), true)
    assert.equal(hasDueWork(summary({ reviewItems: 1 })), true)
    assert.equal(hasDueWork(summary({ dueTasks: 1 })), true)
  })
})

describe('dueSummaryHeadlineKey — 优先级与后端 digestTitle 一致', () => {
  it('无数据时给"今天没有待办"', () => {
    assert.equal(dueSummaryHeadlineKey(null), 'study.due.allClear')
    assert.equal(dueSummaryHeadlineKey(summary()), 'study.due.allClear')
  })

  it('到期卡片最优先', () => {
    assert.equal(
      dueSummaryHeadlineKey(summary({ dueCards: 5, inbox: 9, reviewItems: 3, dueTasks: 2 })),
      'study.due.cardsDue',
    )
  })

  it('其次 inbox 待处理，再到复习中，最后工作项到期', () => {
    assert.equal(
      dueSummaryHeadlineKey(summary({ inbox: 9, reviewItems: 3, dueTasks: 2 })),
      'study.due.inboxWaiting',
    )
    assert.equal(
      dueSummaryHeadlineKey(summary({ reviewItems: 3, dueTasks: 2 })),
      'study.due.reviewing',
    )
    assert.equal(dueSummaryHeadlineKey(summary({ dueTasks: 2 })), 'study.due.tasksDue')
  })
})

describe('dueSummaryCount — 主数字取最可执行的一项', () => {
  it('无数据为 0', () => {
    assert.equal(dueSummaryCount(null), 0)
    assert.equal(dueSummaryCount(summary()), 0)
  })

  it('按 dueCards → inbox → reviewItems → dueTasks 顺序取第一个非零', () => {
    assert.equal(dueSummaryCount(summary({ dueCards: 4, inbox: 7 })), 4)
    assert.equal(dueSummaryCount(summary({ inbox: 7, reviewItems: 3 })), 7)
    assert.equal(dueSummaryCount(summary({ reviewItems: 3, dueTasks: 2 })), 3)
    assert.equal(dueSummaryCount(summary({ dueTasks: 2 })), 2)
  })
})

describe('nextReminderAt — 下一次触发时间', () => {
  it('无提醒返回 0，界面据此隐藏该行', () => {
    assert.equal(nextReminderAt(null), 0)
    assert.equal(nextReminderAt(undefined), 0)
    assert.equal(nextReminderAt([]), 0)
  })

  it('取最早的未确认提醒', () => {
    const at = nextReminderAt([
      reminder({ id: 'a', nextDueAt: 1790000500 }),
      reminder({ id: 'b', nextDueAt: 1790000000 }),
    ])
    assert.equal(at, 1790000000)
  })

  it('已确认 / 已完成的提醒不再参与计算', () => {
    const at = nextReminderAt([
      reminder({ id: 'acked', nextDueAt: 1790000000, state: 'acked' }),
      reminder({ id: 'done', nextDueAt: 1790000100, state: 'done' }),
      reminder({ id: 'live', nextDueAt: 1790000900, state: 'pending' }),
    ])
    assert.equal(at, 1790000900, 'acking a reminder must stop it from showing a next time')
  })

  it('nextDueAt <= 0 的脏数据被跳过', () => {
    assert.equal(nextReminderAt([reminder({ nextDueAt: 0 })]), 0)
  })
})

describe('dailyRuleTime', () => {
  it('每日规则取出 HH:MM', () => {
    assert.equal(dailyRuleTime(reminder({ ruleKind: 'daily', ruleValue: '20:30' })), '20:30')
    assert.equal(dailyRuleTime(reminder({ ruleKind: 'daily', ruleValue: '07:05' })), '07:05')
  })

  it('非每日规则返回空串', () => {
    assert.equal(dailyRuleTime(reminder({ ruleKind: 'interval', ruleValue: '20:30' })), '')
    assert.equal(dailyRuleTime(reminder({ ruleKind: 'once' })), '')
  })

  it('畸形值返回空串，而不是把垃圾串渲染到界面上', () => {
    assert.equal(dailyRuleTime(reminder({ ruleValue: '8pm' })), '')
    assert.equal(dailyRuleTime(reminder({ ruleValue: '20:75' })), '')
    assert.equal(dailyRuleTime(reminder({ ruleValue: '' })), '')
    assert.equal(dailyRuleTime(null), '')
    assert.equal(dailyRuleTime(undefined), '')
  })
})

describe('formatClockTime', () => {
  it('非法或非正输入返回空串', () => {
    assert.equal(formatClockTime(0), '')
    assert.equal(formatClockTime(-1), '')
    assert.equal(formatClockTime(Number.NaN), '')
    assert.equal(formatClockTime(Number.POSITIVE_INFINITY), '')
  })

  it('输出两位 HH:MM（本地时区）', () => {
    const d = new Date(2026, 2, 4, 9, 5, 0)
    assert.equal(formatClockTime(Math.floor(d.getTime() / 1000)), '09:05')
    const d2 = new Date(2026, 2, 4, 20, 30, 0)
    assert.equal(formatClockTime(Math.floor(d2.getTime() / 1000)), '20:30')
  })
})
