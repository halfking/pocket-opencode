import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  filterMeetings, formatDuration, formatLocationLine, formatParticipants, isArchived, statusText,
} from './meeting-list.ts'
import type { LocalMeeting } from './meetings-store.ts'

function meeting(partial: Partial<LocalMeeting>): LocalMeeting {
  return {
    id: 'm1', title: '周会', location: null, participants: [], audioPath: null,
    durationMs: 0, transcript: null, summary: null, liveSummary: null,
    refinedTranscript: null, recommendations: [], noteId: null, sessionId: null,
    status: 'completed', startedAt: 1, createdAt: 1, deletedAt: null, archivedAt: null,
    tags: [], topic: null, summarySkill: 'meeting-minutes',
    ...partial,
  }
}

describe('meeting-list', () => {
  it('treats missing archivedAt as active', () => {
    assert.equal(isArchived(meeting({})), false)
    assert.equal(isArchived(meeting({ archivedAt: 0 })), false)
    assert.equal(isArchived(meeting({ archivedAt: 9 })), true)
  })

  it('filters archived vs active lists', () => {
    const rows = [meeting({ id: 'a' }), meeting({ id: 'b', archivedAt: 2 })]
    assert.deepEqual(filterMeetings(rows, 'active').map((m) => m.id), ['a'])
    assert.deepEqual(filterMeetings(rows, 'archived').map((m) => m.id), ['b'])
  })

  it('formats participants and location for the card', () => {
    assert.equal(formatParticipants([]), '')
    assert.equal(formatParticipants(['张三', '李四']), '张三、李四')
    assert.equal(formatParticipants(['a', 'b', 'c', 'd']), 'a、b、c 等4人')
    assert.equal(formatLocationLine('  3F '), '📍 3F')
    assert.equal(formatLocationLine(''), '')
  })

  it('maps status and duration', () => {
    assert.equal(statusText('recording'), '录音中')
    assert.equal(formatDuration(90_000), '1分30秒')
    assert.equal(formatDuration(0), '')
  })
})
