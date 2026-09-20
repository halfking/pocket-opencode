/**
 * reconnectPolicy 单测(2026-09-20 通知体系 P2)。
 *
 * 覆盖:指数增长、上限钳制、抖动边界、确定性 rand 注入。
 */
import assert from 'node:assert/strict'
import { test } from 'node:test'
import {
  nextReconnectDelay,
  RECONNECT_BASE_MS,
  RECONNECT_MAX_MS,
} from '../reconnectPolicy.ts'

test('基础延迟:首次重连在 3s 附近(±20% 抖动)', () => {
  const fixed = nextReconnectDelay(0, () => 0.5) // 无抖动
  assert.equal(fixed, RECONNECT_BASE_MS)
})

test('指数增长:attempt 越大延迟越长(无抖动)', () => {
  const d0 = nextReconnectDelay(0, () => 0.5)
  const d2 = nextReconnectDelay(2, () => 0.5)
  const d5 = nextReconnectDelay(5, () => 0.5)
  assert.ok(d2 > d0)
  assert.ok(d5 > d2)
})

test('上限钳制:高 attempt 不超过 30s(抖动后的绝对上限)', () => {
  const max = nextReconnectDelay(20, () => 1) // +20% 抖动
  assert.ok(max <= RECONNECT_MAX_MS, `got ${max}`)
  const base = nextReconnectDelay(20, () => 0.5)
  assert.equal(base, RECONNECT_MAX_MS)
})

test('抖动边界:rand∈[0,1] 映射到 ±20%', () => {
  const low = nextReconnectDelay(0, () => 0)
  const high = nextReconnectDelay(0, () => 1)
  assert.equal(low, Math.round(RECONNECT_BASE_MS * 0.8))
  assert.equal(high, Math.round(RECONNECT_BASE_MS * 1.2))
})

test('负 attempt 按首次处理', () => {
  assert.equal(nextReconnectDelay(-3, () => 0.5), RECONNECT_BASE_MS)
})
