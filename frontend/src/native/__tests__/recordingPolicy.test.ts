/**
 * recordingPolicy 单测(2026-09-20 P0 录音后台化)。
 *
 * 覆盖会议/笔记录音的启动决策表:幂等重入、跨会议顶替(强制新会话态)、
 * resume 仅同场有效、麦克风互斥、stop 收尾期拒绝。
 */
import assert from 'node:assert/strict'
import { test } from 'node:test'
import { decideMeetingStart, decideNoteStart } from '../recordingPolicy.ts'

// ---- 会议录音 ----

test('会议:已在录同一场会议 → 幂等重入(页面重进不重启采集)', () => {
  const d = decideMeetingStart({
    meetingId: 'm1', meetingRecording: true, activeMeetingId: 'm1',
    stopping: false, noteRecording: false,
  })
  assert.deepEqual({ action: d.action, ok: d.ok }, { action: 'noop-attached', ok: true })
})

test('会议:别的会议在录 → 顶替(freshSession 强制 true,防残留分段)', () => {
  const d = decideMeetingStart({
    meetingId: 'm2', meetingRecording: true, activeMeetingId: 'm1',
    stopping: false, noteRecording: false, resume: true,
  })
  assert.equal(d.action, 'replace-other')
  assert.equal(d.ok, true)
  assert.equal(d.freshSession, true)
})

test('会议:同场 resume(切设备重开) → 续录不重置会话态', () => {
  const d = decideMeetingStart({
    meetingId: 'm1', meetingRecording: false, activeMeetingId: '',
    stopping: false, noteRecording: false, resume: true,
  })
  assert.equal(d.action, 'resume-self')
  assert.equal(d.freshSession, false)
})

test('会议:全新开始 → freshSession', () => {
  const d = decideMeetingStart({
    meetingId: 'm1', meetingRecording: false, activeMeetingId: '',
    stopping: false, noteRecording: false,
  })
  assert.equal(d.action, 'start-fresh')
  assert.equal(d.freshSession, true)
})

test('会议:笔记录音占用 → 拒绝', () => {
  const d = decideMeetingStart({
    meetingId: 'm1', meetingRecording: false, activeMeetingId: '',
    stopping: false, noteRecording: true,
  })
  assert.equal(d.action, 'reject-note-busy')
  assert.equal(d.ok, false)
})

test('会议:stop 收尾中 → 拒绝(防顶替与收尾竞争)', () => {
  const d = decideMeetingStart({
    meetingId: 'm1', meetingRecording: true, activeMeetingId: 'm2',
    stopping: true, noteRecording: false,
  })
  assert.equal(d.action, 'reject-stopping')
})

test('会议:空 meetingId → 拒绝', () => {
  const d = decideMeetingStart({
    meetingId: '', meetingRecording: false, activeMeetingId: '',
    stopping: false, noteRecording: false,
  })
  assert.equal(d.ok, false)
})

// ---- 笔记录音 ----

test('笔记:会议录音占用 → 拒绝并给出原因', () => {
  const d = decideNoteStart({ phase: 'idle', meetingRecording: true })
  assert.equal(d.action, 'reject-meeting-busy')
  assert.equal(d.ok, false)
})

test('笔记:非 idle(已在录/收尾中) → no-op', () => {
  assert.equal(decideNoteStart({ phase: 'recording', meetingRecording: false }).action, 'noop-busy-phase')
  assert.equal(decideNoteStart({ phase: 'stopping', meetingRecording: false }).action, 'noop-busy-phase')
})

test('笔记:空闲且无会议录音 → 允许', () => {
  const d = decideNoteStart({ phase: 'idle', meetingRecording: false })
  assert.equal(d.action, 'start')
  assert.equal(d.ok, true)
})
