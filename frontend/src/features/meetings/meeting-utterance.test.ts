import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { buildUtteranceSegment, normalizeUtterance } from './meeting-utterance.ts'

describe('meeting-utterance', () => {
  it('normalizes spaces and rejects empty lines', () => {
    assert.equal(normalizeUtterance('  今天  评审  '), '今天 评审')
    assert.equal(buildUtteranceSegment({ meetingId: 'm1', text: '   ', startMs: 0 }), null)
    assert.equal(buildUtteranceSegment({ meetingId: '', text: 'hi', startMs: 0 }), null)
  })

  it('builds a persistable segment from a typed or captioned line', () => {
    const seg = buildUtteranceSegment({
      meetingId: 'm1', text: '李四下周提交预算方案', startMs: 1200, speakerLabel: '张三',
    })
    assert.ok(seg)
    assert.equal(seg.meetingId, 'm1')
    assert.equal(seg.speakerLabel, '张三')
    assert.equal(seg.text, '李四下周提交预算方案')
    assert.equal(seg.startMs, 1200)
    assert.ok((seg.endMs ?? 0) > 1200)
  })
})
