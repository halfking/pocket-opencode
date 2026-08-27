/**
 * sessionEvents.test.mjs — P1 契约双向锁定（TS 侧）。
 *
 * Run with: `node --test frontend/src/services/__tests__/sessionEvents.test.mjs`
 *
 * 样本与 Go fixture（backend/internal/opencode/session_event_broadcaster_test.go）
 * 完全相同：固定时钟 1750000000000ms（UnixNano=1750000000000000000）下，Go 广播
 * 器真实产出的 {type,payload} wire JSON。任何一侧改字段名/枚举值，另一侧打红。
 * 契约：docs/2026-08-27-p1-contracts-frozen.md §2。
 *
 * 注意两层信封：WS 线上是 {type,payload}（hub.Message）；idempotentWsBus 订阅
 * 回调拿到的是 {type, data:WsEnvelopeV1}（parse 函数的入参），故样本先按 wire
 * 形状书写（与 Go 逐字节同义），再经 wireToBus 转成 parse 入参 —— 这同时锁定了
 * 两层信封的字段名（payload ↔ data）。
 */

import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  SESSION_EVENT_TYPES,
  parseSessionActivity,
  parseRoundCompleted,
  parseTaskHealth,
} from '../sessionEvents.ts'

// ---- 双向锁定样本（与 Go fixture 常量逐字节同义）----

const activityWire = {
  type: 'session.activity',
  payload: {
    v: 1,
    id: 'session_activity_1750000000000000000_2',
    ts: 1750000000000,
    channel: 'sessions',
    topic: 'ses_fx_1',
    type: 'session.activity',
    data: {
      instance_id: 'inst_fx',
      session_id: 'ses_fx_1',
      phase: 'file_write',
      last_event_at: 1750000000000,
      round_index: 1,
    },
  },
}

const taskHealthWire = {
  type: 'task.health',
  payload: {
    v: 1,
    id: 'task_health_1750000000000000000_3',
    ts: 1750000000000,
    channel: 'tasks',
    topic: 'task_fx_1',
    type: 'task.health',
    data: {
      task_id: 'task_fx_1',
      instance_id: 'inst_fx',
      health: 'running',
      pending_count: 0,
      computed_at: 1750000000000,
    },
  },
}

const roundCompletedWire = {
  type: 'round.completed',
  payload: {
    v: 1,
    id: 'round_completed_1750000000000000000_4',
    ts: 1750000000000,
    channel: 'sessions',
    topic: 'ses_fx_1',
    type: 'round.completed',
    data: {
      instance_id: 'inst_fx',
      session_id: 'ses_fx_1',
      round_index: 1,
      summary: '已完成登录页修复并补齐回归测试',
      changes: { added: 12, removed: 3, files: 2 },
      status: 'completed',
      completed_at: 1750000000000,
    },
    cause: { correlation_id: 'ses_fx_1:1' },
  },
}

/** WS 线上 {type,payload} → idempotentWsBus 订阅回调入参 {type, data}。 */
const wireToBus = (wire) => ({ type: wire.type, data: wire.payload })

// ---- parseSessionActivity ----

test('parseSessionActivity 解出与 Go fixture 相同的字段值', () => {
  const evt = parseSessionActivity(wireToBus(activityWire))
  assert.ok(evt, '结构合法必须解出')
  assert.equal(evt.eventId, 'session_activity_1750000000000000000_2')
  assert.deepEqual(evt.data, {
    instance_id: 'inst_fx',
    session_id: 'ses_fx_1',
    phase: 'file_write',
    last_event_at: 1750000000000,
    round_index: 1,
  })
})

// ---- parseRoundCompleted ----

test('parseRoundCompleted 解出与 Go fixture 相同的字段值（含 changes 与枚举）', () => {
  const evt = parseRoundCompleted(wireToBus(roundCompletedWire))
  assert.ok(evt, '结构合法必须解出')
  assert.equal(evt.eventId, 'round_completed_1750000000000000000_4')
  assert.deepEqual(evt.data, {
    instance_id: 'inst_fx',
    session_id: 'ses_fx_1',
    round_index: 1,
    summary: '已完成登录页修复并补齐回归测试',
    changes: { added: 12, removed: 3, files: 2 },
    status: 'completed',
    completed_at: 1750000000000,
  })
})

// ---- parseTaskHealth ----

test('parseTaskHealth 解出与 Go fixture 相同的字段值（含可选 instance_id）', () => {
  const evt = parseTaskHealth(wireToBus(taskHealthWire))
  assert.ok(evt, '结构合法必须解出')
  assert.equal(evt.eventId, 'task_health_1750000000000000000_3')
  assert.deepEqual(evt.data, {
    task_id: 'task_fx_1',
    instance_id: 'inst_fx',
    health: 'running',
    pending_count: 0,
    computed_at: 1750000000000,
  })
})

test('parseTaskHealth 接受省略 instance_id（Go omitempty）', () => {
  const noInstance = wireToBus(taskHealthWire)
  delete noInstance.data.data.instance_id
  const evt = parseTaskHealth(noInstance)
  assert.ok(evt)
  assert.equal(evt.data.instance_id, undefined)
  assert.equal(evt.data.task_id, 'task_fx_1')
})

// ---- 非法输入 ----

test('类型不匹配 / 结构不符 / 非对象输入返回 null', () => {
  assert.equal(parseSessionActivity(wireToBus(roundCompletedWire)), null)
  assert.equal(parseRoundCompleted(wireToBus(taskHealthWire)), null)
  assert.equal(parseTaskHealth(wireToBus(activityWire)), null)
  assert.equal(parseSessionActivity(null), null)
  assert.equal(parseSessionActivity('x'), null)
  assert.equal(parseTaskHealth(undefined), null)

  // 枚举外的 phase / health / status 一律拒绝（冻结枚举锁定）。
  const badPhase = wireToBus(activityWire)
  badPhase.data.data.phase = 'sleeping'
  assert.equal(parseSessionActivity(badPhase), null)
  const badHealth = wireToBus(taskHealthWire)
  badHealth.data.data.health = 'paused'
  assert.equal(parseTaskHealth(badHealth), null)
  const badStatus = wireToBus(roundCompletedWire)
  badStatus.data.data.status = 'timeout'
  assert.equal(parseRoundCompleted(badStatus), null)
})

test('SESSION_EVENT_TYPES 与后端三类事件常量一一对应', () => {
  assert.deepEqual([...SESSION_EVENT_TYPES], [
    'session.activity',
    'round.completed',
    'task.health',
  ])
})
