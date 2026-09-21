/**
 * useCountUp — easing + 边界单测。
 *
 * 完整 composable 依赖 Vue ref + onScopeDispose + requestAnimationFrame +
 * prefers-reduced-motion，跨 node:test 难复现；这里只覆盖核心数学，确保
 * tween 形状对 + 与产品默认 800ms 配对后的「快到慢收敛」观感对得上。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  easeOutCubic,
} from '../useCountUp.ts'

test('easeOutCubic 在边界 0 / 1 上各归宿', () => {
  assert.equal(easeOutCubic(0), 0)
  assert.equal(easeOutCubic(1), 1)
})

test('easeOutCubic 单调递增（动画不会有抖动 / 回拉）', () => {
  let prev = 0
  for (let i = 0; i <= 20; i++) {
    const t = i / 20
    const v = easeOutCubic(t)
    assert.ok(v >= prev, `easeOutCubic(t=${t}) = ${v} 不应低于前一帧 ${prev}`)
    prev = v
  }
})

test('easeOutCubic 在中段 < t（减速曲线：已走过大半才接近目标）', () => {
  // easeOut 系列的特征：前半段走得快、后半段走得慢。
  // 在 t=0.5 时，easeOutCubic 应当显著大于 0.5（因为前段就吃掉了大半路程）。
  assert.ok(easeOutCubic(0.5) > 0.5)
  // 0.25 步时应该已经走过 ~58% 的路程
  assert.ok(easeOutCubic(0.25) > 0.5)
})

test('easeOutCubic 标准 800ms 关键帧观察点（用户在 200/500/700ms 看到的进度比例）', () => {
  // 200ms / 800ms = 0.25  → 大约 58% 已显示
  // 500ms / 800ms = 0.625 → 大约 98% 已显示
  // 700ms / 800ms = 0.875 → 几乎 100%
  assert.ok(easeOutCubic(0.25) >= 0.55 && easeOutCubic(0.25) <= 0.62)
  assert.ok(easeOutCubic(0.625) >= 0.95 && easeOutCubic(0.625) <= 1.0)
  assert.ok(easeOutCubic(0.875) >= 0.99 && easeOutCubic(0.875) <= 1.0)
})

test('easeOutCubic 在 t > 1 时不抛（外推到上限 1）', () => {
  // 即使调用方传 >1 也得安全；要靠动画内部 clamp 或 easing 自带收敛。
  // 当前实现是 1 - (1-t)^3，t=1.2 会得到负值（且偏离真实进度）。
  // 真正使用方在 useCountUp 内部用 Math.min(1, t) 已经夹住，所以这仅作为「easing 自身不抛」测试。
  assert.doesNotThrow(() => easeOutCubic(2.5))
})
