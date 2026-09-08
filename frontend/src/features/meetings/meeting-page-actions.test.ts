import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  canDispatchMeeting, meetingAccDispatchInput, studioMenuItems,
} from './meeting-page-actions.ts'
import { MEETING_LIST_FILTERS } from './meeting-list.ts'

describe('meeting-page-actions', () => {
  it('lists archive vs restore plus classify/dispatch/delete', () => {
    const active = studioMenuItems({ archivedAt: null })
    assert.deepEqual(active.map((i) => i.id), ['archive', 'classify', 'dispatch-acc', 'delete'])
    assert.equal(active.find((i) => i.id === 'delete')?.danger, true)
    const archived = studioMenuItems({ archivedAt: 9 })
    assert.equal(archived[0]?.id, 'restore')
    assert.equal(archived[0]?.label, '恢复')
  })

  it('allows ACC dispatch only when summary or todos exist', () => {
    assert.equal(canDispatchMeeting({ summary: null, liveSummary: null }), false)
    assert.equal(canDispatchMeeting({ summary: '  ', liveSummary: null }), false)
    assert.equal(canDispatchMeeting({ summary: '拍板下周发布', liveSummary: null }), true)
    assert.equal(canDispatchMeeting({
      summary: null,
      liveSummary: { summary: '', keyPoints: [], actionItems: [{ text: '补测试' }], decisions: [], openQuestions: [], updatedAt: 1 },
    }), true)
  })

  it('builds a meeting-level ACC one-shot from title, summary and todos', () => {
    const acc = meetingAccDispatchInput({
      title: '架构评审',
      topic: '同步方案',
      summary: '先落本地再推远端',
      liveSummary: {
        summary: '',
        keyPoints: [],
        actionItems: [{ text: '补冲突表', assignee: '黄', due: '周五' }],
        decisions: [],
        openQuestions: [],
        updatedAt: 1,
      },
    })
    assert.equal(acc.kind, 'redclaw_chat')
    assert.equal(acc.maxRuns, 1)
    assert.equal(acc.payload.source, 'meeting-dispatch')
    assert.match(acc.name, /架构评审/)
    assert.match(String(acc.payload.prompt), /同步方案/)
    assert.match(String(acc.payload.prompt), /补冲突表/)
    assert.match(String(acc.payload.prompt), /黄/)
  })

  it('exposes list filters for the header, not a content toolbar', () => {
    assert.deepEqual(MEETING_LIST_FILTERS.map((f) => f.label), ['进行中', '已归档'])
  })
})
