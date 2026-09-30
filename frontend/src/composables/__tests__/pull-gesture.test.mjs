import { strict as assert } from 'node:assert'
import { test } from 'node:test'
import {
  FLING_TRIGGER_VELOCITY,
  MAX_PULL_RATIO,
  PULL_RESPONSE,
  REFRESH_HOLD_RATIO,
  arrowRotation,
  flingVelocity,
  indicatorScale,
  pullDistanceFor,
  pullHint,
  pullHintText,
  pullProgress,
  pushSample,
  refreshHoldOffset,
  resetSamples,
  rubberBand,
  shouldTrigger,
  shouldTriggerByFling,
} from '../../composables/pull-gesture.ts'

const cap = (t) => t * MAX_PULL_RATIO

test('rubberBand: 原点为 0，负位移夹到 0，零维度不崩', () => {
  assert.equal(rubberBand(0, 60), 0)
  assert.equal(rubberBand(-50, 60), 0)
  assert.equal(rubberBand(30, 0), 0)
})

test('rubberBand: 单调递增且始终小于线性位移（越拉越沉）', () => {
  const pts = [10, 30, 60, 120, 240, 480]
  for (let i = 1; i < pts.length; i++) {
    const prev = rubberBand(pts[i - 1], 60)
    const cur = rubberBand(pts[i], 60)
    assert.ok(cur > prev, `位移应递增：${pts[i - 1]} -> ${pts[i]}`)
    assert.ok(cur < pts[i], `应小于原始位移：${cur} < ${pts[i]}`)
  }
})

test('rubberBand: 起手段近似 PULL_RESPONSE，兼顾跟手', () => {
  // f(x) 在 0 处的斜率就是 k，x 极小时映射值 ≈ k·x。
  const small = rubberBand(0.01, 60)
  assert.ok(Math.abs(small / 0.01 - PULL_RESPONSE) < 0.01, `实际 ${small / 0.01}`)
  // 不能完全 1:1（旧线性阻尼是 0.5，明显滞后）；也不能 >0.9（会窜出去）。
  assert.ok(PULL_RESPONSE > 0.6 && PULL_RESPONSE < 0.9)
})

test('rubberBand: 渐近上限 = threshold × MAX_PULL_RATIO，且永不超过', () => {
  assert.ok(Math.abs(rubberBand(1e9, 60) - cap(60)) < 1e-6)
  assert.ok(rubberBand(1e6, 60) <= cap(60) + 1e-9)
  // 越拉越沉：手指位移翻倍，实际位移远不到翻倍。
  const a = rubberBand(100, 60)
  const b = rubberBand(200, 60)
  assert.ok(b < a * 2)
})

test('触发行程与旧实现持平：约 2.1 倍 threshold 的手指位移触发', () => {
  // 旧实现是 0.5 线性阻尼 → 120px = 2×threshold 触发。
  // 曲线若明显更费力就是手感退步，回归点应卡在这条线上。
  const rawToTrigger = 2.1 * 60
  assert.ok(rubberBand(rawToTrigger, 60) >= 60, `实际 ${rubberBand(rawToTrigger, 60)}`)
  // 略少于旧实现的行程时不该触发（不能变得太容易，避免误触）
  assert.ok(rubberBand(1.9 * 60, 60) < 60)
  // 只拉一半手指距离时更不该触发
  assert.ok(rubberBand(0.7 * 60, 60) < 60)
})

test('pullDistanceFor 与 rubberBand 等价（别名不引入第二套语义）', () => {
  for (const d of [0, 10, 60, 137, 1e6]) {
    assert.equal(pullDistanceFor(d, 60), rubberBand(d, 60))
  }
})

test('pullProgress: 夹在 0~1，越界不外泄', () => {
  assert.equal(pullProgress(0, 60), 0)
  assert.equal(pullProgress(30, 60), 0.5)
  assert.equal(pullProgress(60, 60), 1)
  assert.equal(pullProgress(999, 60), 1)
  assert.equal(pullProgress(10, 0), 0)
})

test('shouldTrigger: 刚好到阈值也算达成', () => {
  assert.equal(shouldTrigger(59.9, 60), false)
  assert.equal(shouldTrigger(60, 60), true)
})

test('arrowRotation: 随进度 0→180，过阈值过冲到 200', () => {
  assert.equal(arrowRotation(0, 60), 0)
  assert.equal(arrowRotation(30, 60), 90)
  assert.equal(arrowRotation(60, 60), 200)
  assert.equal(arrowRotation(96, 60), 200)
})

test('indicatorScale: 0.6 起、收敛到 1，easeOut 前段更快', () => {
  assert.equal(indicatorScale(0, 60), 0.6)
  assert.equal(indicatorScale(60, 60), 1)
  // easeOutCubic：p=0.5 → 1-0.5^3 = 0.875 → scale = 0.6+0.35 = 0.95
  assert.ok(Math.abs(indicatorScale(30, 60) - 0.95) < 1e-9)
  // 小位移就要看得见，不能还是 0.6 出头。
  assert.ok(indicatorScale(6, 60) > 0.7)
})

test('pullHint / 文案：三态互斥且可读', () => {
  assert.equal(pullHint(10, 60, false), 'pull')
  assert.equal(pullHint(60, 60, false), 'release')
  // 刷新中优先级最高：已越过阈值也不该显示「松开」。
  assert.equal(pullHint(60, 60, true), 'refreshing')
  assert.equal(pullHintText('pull'), '下拉同步邮件')
  assert.equal(pullHintText('release'), '松开立即同步')
  assert.match(pullHintText('refreshing'), /同步/)
})

test('refreshHoldOffset: 刷新中停在阈值之上，指示器不会消失', () => {
  const hold = refreshHoldOffset(60)
  assert.ok(hold > 60)
  assert.ok(hold < 60 * MAX_PULL_RATIO)
  assert.equal(REFRESH_HOLD_RATIO, 1.15)
})

test('flingVelocity: 窗口内计算，窗口外样本不计', () => {
  const samples = []
  // 100ms 内下拉 100px → 1 px/ms
  pushSample(samples, 0, 0)
  pushSample(samples, 100, 100)
  assert.ok(Math.abs((flingVelocity(samples, 100) ?? 0) - 1) < 1e-9)

  // 慢速：最近 100ms 内只跨 5px → 0.05 px/ms（够不到甩动阈值）
  const slow = []
  pushSample(slow, 0, 900)
  pushSample(slow, 5, 1000)
  assert.ok(Math.abs((flingVelocity(slow, 1000) ?? 0) - 0.05) < 1e-9)
  // 上一次手势留下的老样本不得污染本次：只留窗口内的两个点
  assert.equal(slow.length, 2)
})

test('flingVelocity: 样本不足或 dt<=0 返回 null（不猜）', () => {
  assert.equal(flingVelocity([], 0), null)
  assert.equal(flingVelocity([{ y: 0, t: 0 }], 0), null)
  assert.equal(flingVelocity([{ y: 0, t: 0 }, { y: 10, t: 0 }], 0), null)
})

test('pushSample: 裁掉窗口外旧点，数组不会无限增长', () => {
  const samples = []
  for (let i = 0; i < 50; i++) pushSample(samples, i, i * 50)
  assert.ok(samples.length <= 3, `实际 ${samples.length}`)
  resetSamples(samples)
  assert.equal(samples.length, 0)
})

test('shouldTriggerByFling: 只在「已拉一段」且速度够时才提前触发', () => {
  // 拉了 40% 但几乎不动 → 不触发
  assert.equal(shouldTriggerByFling(24, 60, 0.1), false)
  // 拉了 40% 但甩得很快 → 触发
  assert.equal(shouldTriggerByFling(24, 60, FLING_TRIGGER_VELOCITY), true)
  // 拉得够多但速度慢 → 交给阈值判定
  assert.equal(shouldTriggerByFling(40, 60, 0.2), false)
  assert.equal(shouldTriggerByFling(24, 60, null), false)
  // 上甩（负速度）不触发
  assert.equal(shouldTriggerByFling(40, 60, -1.2), false)
})
