import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  captureDeviceLocation, formatCapturedTitle, formatCoords, parseTagInput, suggestMeetingTags,
} from './meeting-meta.ts'

describe('meeting-meta', () => {
  it('prefers topic then spoken title then time+location', () => {
    const startedAt = Date.parse('2026-09-08T09:30:00+08:00')
    assert.equal(formatCapturedTitle({ startedAt, topic: 'Q3 预算' }), 'Q3 预算')
    assert.equal(
      formatCapturedTitle({ startedAt, firstUtterance: '今天先过一遍发布清单然后看风险' }),
      '今天先过一遍发布清单然后看风险',
    )
    assert.match(formatCapturedTitle({ startedAt, location: '3F-A' }), /3F-A/)
    assert.match(formatCapturedTitle({ startedAt }), /会议/)
  })

  it('parses and suggests tags without duplicates', () => {
    assert.deepEqual(parseTagInput('周会, 预算，#周会'), ['周会', '预算'])
    assert.deepEqual(suggestMeetingTags('下午周会过 Q3 预算'), ['周会', '预算'])
    assert.deepEqual(suggestMeetingTags(''), [])
  })

  it('formats coords and captures location through the geo seam', async () => {
    assert.equal(formatCoords(31.2304, 121.4737), '31.2304, 121.4737')
    assert.equal(formatCoords(Number.NaN, 1), '')
    const loc = await captureDeviceLocation({
      getCurrentPosition(ok) { ok({ coords: { latitude: 1.2, longitude: 3.4 } }) },
    })
    assert.equal(loc, '1.2000, 3.4000')
    const denied = await captureDeviceLocation({
      getCurrentPosition(_ok, err) { err?.(new Error('denied')) },
    })
    assert.equal(denied, null)
  })
})
