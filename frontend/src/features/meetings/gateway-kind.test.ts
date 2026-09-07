import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { gatewayWorkType } from './gateway-kind.ts'

describe('gatewayWorkType', () => {
  it('maps live translate and summary to gateway work types', () => {
    assert.equal(gatewayWorkType('live_translate'), 'doc_translate')
    assert.equal(gatewayWorkType('meeting_summary'), 'meeting_summary')
    assert.equal(gatewayWorkType('meeting_refine'), 'doc_translate')
    assert.equal(gatewayWorkType('chat'), '')
  })
})
