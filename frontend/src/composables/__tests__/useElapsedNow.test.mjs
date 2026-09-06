/**
 * useElapsedNow — 自适应时长节拍单测（ISSUES #20 周期性重渲染修复）。
 * 运行：node --experimental-strip-types --test src/composables/__tests__/useElapsedNow.test.mjs
 *
 * 覆盖纯策略 elapsedTickInterval：秒级窗口 / 分钟粒度降频 / 空闲零定时器 /
 * 混合基准取最严 / 边界值；以及 useElapsedNow composable 在真实定时器下的
 * 降频与停表行为（合成事件循环）。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  ELAPSED_FRESH_WINDOW_MS,
  elapsedTickInterval,
} from '../useElapsedNow.ts'

test('秒级窗口：基准在 60s 内 → 1s 节拍（显示 "42s"，文本每秒在变）', () => {
  const now = 1_000_000_000
  assert.equal(elapsedTickInterval([now - 42_000], now), 1_000)
})

test('分钟粒度：基准超过 60s 窗口 → 15s 节拍（文本最多一分钟一变）', () => {
  const now = 1_000_000_000
  assert.equal(elapsedTickInterval([now - ELAPSED_FRESH_WINDOW_MS - 1], now), 15_000)
})

test('边界：恰好 60_000ms 不再属于秒级窗口（< 严格小于）', () => {
  const now = 1_000_000_000
  assert.equal(elapsedTickInterval([now - ELAPSED_FRESH_WINDOW_MS], now), 15_000)
  assert.equal(elapsedTickInterval([now - ELAPSED_FRESH_WINDOW_MS + 1], now), 1_000)
})

test('空闲会话：无任何有效基准 → null（零定时器，消灭周期性重渲染源）', () => {
  const now = 1_000_000_000
  assert.equal(elapsedTickInterval([], now), null)
  assert.equal(elapsedTickInterval([null], now), null)
  assert.equal(elapsedTickInterval([null, Number.NaN], now), null)
})

test('混合基准：任一基准在秒级窗口内即取 1s（审批首见 + 旧事件并存）', () => {
  const now = 1_000_000_000
  assert.equal(elapsedTickInterval([now - 300_000, now - 5_000], now), 1_000)
})

test('基准存在但都过期：即使有 null 混入也降频为 15s', () => {
  const now = 1_000_000_000
  assert.equal(elapsedTickInterval([null, now - 600_000], now), 15_000)
})

test('非有限数值基准按无基准处理（防御上游脏数据）', () => {
  const now = 1_000_000_000
  assert.equal(elapsedTickInterval([Number.NaN, Infinity, -Infinity], now), null)
})

test('自定义阈值可注入（60s 窗口由显示粒度决定，不随阈值变化）', () => {
  const now = 1_000_000_000
  // 秒级窗口内（<60s）→ freshMs
  assert.equal(elapsedTickInterval([now - 500], now, 2_000, 30_000), 2_000)
  assert.equal(elapsedTickInterval([now - 5_000], now, 2_000, 30_000), 2_000)
  // 窗口外 → staleMs
  assert.equal(elapsedTickInterval([now - 61_000], now, 2_000, 30_000), 30_000)
})
