/**
 * learning-streak-view.test.ts — streak 响应形状校验的回归测试。
 *
 * 运行：cd frontend && node --experimental-strip-types --test \
 *        src/features/study/__tests__/learning-streak-view.test.ts
 *
 * 这组用例的由来是一次真实白屏（2026-10-03 IA 重组冒烟时抓到）：
 * 模板写 `streak.streak.current` 是两级解引用，`v-if="streak"` 只挡外层；
 * 后端返回 200 + `{}` 时解引用抛异常，**整个「学习」页白屏**——
 * 连同一页的闪卡列表一起没了，而 streak 只是最次要的一个角标。
 *
 * 每条用例都对应一种"HTTP 200 但体不可信"的真实成因，改校验逻辑时
 * 它们会告诉你动的是哪一种降级。
 */
import { strict as assert } from 'node:assert'
import { describe, it } from 'node:test'

import { normalizeStreakView } from '../learning-streak-view.ts'

/** 合法的完整响应，作为「不该被误伤」的基准。 */
function full(patch: Record<string, unknown> = {}) {
  return {
    streak: { current: 7, longest: 12, lastStudyDay: 20000 },
    milestone: 30,
    next: 30,
    today: 20007,
    ...patch,
  }
}

describe('normalizeStreakView', () => {
  it('完整响应原样放行（不改变对象身份，避免无谓的响应式触发）', () => {
    const v = full()
    assert.equal(normalizeStreakView(v), v)
  })

  it('current 为 0 也放行 —— 「连续 0 天」是合法业务状态，不是异常', () => {
    // 关键：0 是 falsy。这里若写成 `if (!current) return null`，
    // 「今天还没学」的正常用户会看不到连续天数块，而真正的「查不到」
    // 反而显示 0 天 —— 恰好是这段代码注释里明说要避免的那种误导。
    const v = full({ streak: { current: 0, longest: 0, lastStudyDay: 0 } })
    assert.notEqual(normalizeStreakView(v), null)
  })

  // ---- 以下每一条都曾是白屏触发条件 ----

  it('空对象 {} → null（网关降级兜底体的典型形态）', () => {
    assert.equal(normalizeStreakView({}), null)
  })

  it('缺 streak 字段 → null（Learning Core 刚起、还没建表）', () => {
    assert.equal(normalizeStreakView({ milestone: 30, next: 30, today: 1 }), null)
  })

  it('streak 为 null → null（信封改版后端返回显式 null）', () => {
    assert.equal(normalizeStreakView({ streak: null, milestone: 0, next: 0 }), null)
  })

  it('current 不是数字（字符串/布尔）→ null', () => {
    assert.equal(normalizeStreakView({ streak: { current: '7' } }), null)
    assert.equal(normalizeStreakView({ streak: { current: true } }), null)
  })

  it('current 为 NaN / Infinity → null（不能让 NaN 上屏）', () => {
    assert.equal(normalizeStreakView({ streak: { current: NaN } }), null)
    assert.equal(normalizeStreakView({ streak: { current: Infinity } }), null)
  })

  it('整体为 null / undefined → null（不该抛异常）', () => {
    assert.equal(normalizeStreakView(null), null)
    assert.equal(normalizeStreakView(undefined), null)
  })

  it('非对象（数组/字符串/数字）→ null，不抛', () => {
    // 数组 typeof 是 'object'，容易漏；这里显式钉住。
    assert.equal(normalizeStreakView([]), null)
    assert.equal(normalizeStreakView('7'), null)
    assert.equal(normalizeStreakView(7), null)
  })

  it('缺 next / milestone 仍然放行（模板对这两项本来就有 v-if 兜底）', () => {
    const v = { streak: { current: 3 } }
    assert.notEqual(normalizeStreakView(v), null)
  })
})
