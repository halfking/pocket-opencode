/**
 * 列表录音状态机：点击开始/停止，累计时长与转写拼接。
 * Run: node --test --experimental-strip-types src/features/notes/note-recording.test.ts
 */
import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  appendTranscript,
  formatRecordingClock,
  nextRecordingState,
} from './note-recording.ts'

describe('formatRecordingClock', () => {
  it('formats elapsed milliseconds as mm:ss', () => {
    assert.equal(formatRecordingClock(0), '00:00')
    assert.equal(formatRecordingClock(12_000), '00:12')
    assert.equal(formatRecordingClock(75_400), '01:15')
  })
})

describe('nextRecordingState', () => {
  it('toggles idle to recording and recording to stopping', () => {
    assert.equal(nextRecordingState('idle', 'toggle'), 'recording')
    assert.equal(nextRecordingState('recording', 'toggle'), 'stopping')
    assert.equal(nextRecordingState('stopping', 'drafted'), 'idle')
  })
})

describe('appendTranscript', () => {
  it('replaces the live partial while keeping committed text', () => {
    const first = appendTranscript('', '', '今天天气')
    assert.equal(first.committed, '')
    assert.equal(first.display, '今天天气')
    const finalized = appendTranscript(first.committed, first.display, '', '今天天气不错。')
    assert.equal(finalized.committed, '今天天气不错。')
    assert.equal(finalized.display, '今天天气不错。')
  })
})
