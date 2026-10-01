// email-classify-loop.test.mjs
//
// 锁住「归类循环什么时候该停」与「结束时提示说什么」。
//
// 2026-10-02 实测的缺陷：runClassify 原来是 `do { ... } while (!classifyCancel)`，
// 只靠用户中止与 remaining<=0 退出。分类器逐封调 LLM，失败是常态（网关没配 key、
// 凭据错、模型名不对），而 /api/emails/classify 在逐封失败时仍返回 200、
// remaining 保持不变 ⇒ 无限重试 + 界面上没有任何错误提示。
// 现场表现就是用户报的「邮件管理没有自动归纳整理的能力」：转圈，什么也没发生。
//
// 负控（见文件末尾注释）：把 shouldContinueClassify 里的
// `errorCount === rowCount` 改成 `false`，本文件必须转红。

import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  classifyDoneHint,
  DEFAULT_CLASSIFY_MAX_ROUNDS,
  shouldContinueClassify,
} from '../email-classify-run.ts'

/** 便捷构造：默认「一切正常、还有剩余、继续」。 */
const st = (o = {}) => ({
  round: 1,
  maxRounds: DEFAULT_CLASSIFY_MAX_ROUNDS,
  remaining: 20,
  cancelled: false,
  rowCount: 20,
  errorCount: 0,
  ...o,
})

describe('classify loop termination', () => {
  it('continues while the queue still has uncategorized mail', () => {
    assert.equal(shouldContinueClassify(st()), true)
  })

  it('stops when the queue is drained', () => {
    assert.equal(shouldContinueClassify(st({ remaining: 0 })), false)
  })

  it('stops immediately when the user cancels', () => {
    assert.equal(shouldContinueClassify(st({ cancelled: true })), false)
  })

  it('STOPS when every row in the round errored (the infinite-loop regression)', () => {
    // 网关没配 key 时的真实形状：20 封全带 error、classified=0、remaining 不变。
    assert.equal(shouldContinueClassify(st({ rowCount: 20, errorCount: 20 })), false)
  })

  it('keeps going on a partial failure (个别邮件失败不该终止整批)', () => {
    assert.equal(shouldContinueClassify(st({ rowCount: 20, errorCount: 1 })), true)
  })

  it('does not treat an empty result set as "all failed"', () => {
    // 空批次时 errorCount===rowCount===0，若不判 rowCount>0 会被误当成整批失败。
    assert.equal(shouldContinueClassify(st({ rowCount: 0, errorCount: 0 })), true)
  })

  it('honours the round cap', () => {
    assert.equal(shouldContinueClassify(st({ round: DEFAULT_CLASSIFY_MAX_ROUNDS })), false)
    assert.equal(shouldContinueClassify(st({ round: DEFAULT_CLASSIFY_MAX_ROUNDS - 1 })), true)
  })

  it('caps at 400 emails per invocation (20 rounds x 20)', () => {
    assert.equal(DEFAULT_CLASSIFY_MAX_ROUNDS * 20, 400)
  })
})

describe('classify done hint', () => {
  const base = { leftover: 0, firstError: '', allFailed: false, hitMaxRounds: false, maxRounds: 20, perRound: 20 }

  it('reports completion when nothing is left', () => {
    assert.equal(classifyDoneHint(base), '归类完成')
  })

  it('SURFACES the per-email error that used to be swallowed', () => {
    const h = classifyDoneHint({ ...base, leftover: 138, firstError: 'gateway 401', allFailed: true })
    assert.match(h, /归类失败：gateway 401/)
    assert.match(h, /138 封未归类/)
  })

  it('distinguishes partial failure from total failure', () => {
    const h = classifyDoneHint({ ...base, leftover: 3, firstError: 'rate limited', allFailed: false })
    assert.match(h, /部分归类失败：rate limited/)
  })

  it('says the cap was hit instead of silently stopping', () => {
    const h = classifyDoneHint({ ...base, leftover: 120, hitMaxRounds: true })
    assert.match(h, /上限 400 封/)
  })

  it('falls back to the plain paused message when there is no error to show', () => {
    assert.equal(classifyDoneHint({ ...base, leftover: 5 }), '已暂停，仍有 5 封未归类')
  })
})

// 负控：把 shouldContinueClassify 里的整批全失败判据去掉后，本文件里
// 「STOPS when every row in the round errored」必须转红。
