import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import { judgeCodeDelivery, NO_MAIL_DELIVERY_ERROR } from '../code-delivery.ts'

describe('judgeCodeDelivery', () => {
  it('blocks the stepper when the server has no mail channel', () => {
    // 修复前的行为：这里会返回 200，前端直接推进到第 2 步，用户静默卡死。
    const v = judgeCodeDelivery({ delivery: 'none' })
    assert.equal(v.advance, false)
    assert.equal(v.error, NO_MAIL_DELIVERY_ERROR)
  })

  it('advances when the server reports a mail channel', () => {
    assert.deepEqual(judgeCodeDelivery({ delivery: 'smtp' }), { advance: true, error: '' })
  })

  it('still advances in dev mode, where the code is echoed back', () => {
    // DEBUG_ECHO 下 delivery 也是 'none'，但验证码就在响应里，拦了会打断本地联调。
    const v = judgeCodeDelivery({ delivery: 'none', debug_code: '123456' })
    assert.equal(v.advance, true)
    assert.equal(v.error, '')
  })

  it('treats a missing delivery field as deliverable (older backend)', () => {
    // 不返回该字段的老后端不能被这个守卫误伤。
    assert.equal(judgeCodeDelivery({}).advance, true)
    assert.equal(judgeCodeDelivery({ debug_code: '123456' }).advance, true)
  })
})
