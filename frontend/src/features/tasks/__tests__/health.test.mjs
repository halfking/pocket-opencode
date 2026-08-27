/**
 * health.ts 五态模型测试（设计方案 v2 §4.1）。
 *
 * Run with: `node --test frontend/src/features/tasks/__tests__/health.test.mjs`
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import {
  ACTIVE_WITHIN_MS,
  STALLED_AFTER_MS,
  assessHealth,
  formatDuration,
  summarizeHealth,
} from '../health.ts'

const NOW = 1_700_000_000_000
const iso = (msAgo) => new Date(NOW - msAgo).toISOString()

test('formatDuration 人类可读分段', () => {
  assert.equal(formatDuration(5_000), '刚刚')
  assert.equal(formatDuration(40_000), '40 秒')
  assert.equal(formatDuration(5 * 60_000), '5 分钟')
  assert.equal(formatDuration(90 * 60_000), '1 小时')
  assert.equal(formatDuration(3 * 24 * 60 * 60_000), '3 天')
  assert.equal(formatDuration(-1), '')
})

test('pendingApprovals > 0 → needs-input，等待时长取 pendingSince', () => {
  const s = assessHealth({
    active: true,
    updatedAt: iso(60_000),
    pendingApprovals: 1,
    pendingSince: NOW - 5 * 60_000,
    now: NOW,
  })
  assert.equal(s.state, 'needs-input')
  assert.equal(s.tone, 'danger')
  assert.equal(s.action, '等审批')
  assert.equal(s.since, '5 分钟')
})

test('超过 STALLED_AFTER_MS 无更新 → stalled', () => {
  const s = assessHealth({ updatedAt: iso(STALLED_AFTER_MS + 1), now: NOW })
  assert.equal(s.state, 'stalled')
  assert.equal(s.tone, 'warning')
  assert.equal(s.action, '无响应')
})

test('活跃窗口内 → running', () => {
  const s = assessHealth({ updatedAt: iso(ACTIVE_WITHIN_MS), now: NOW })
  assert.equal(s.state, 'running')
  assert.equal(s.action, '进行中')
})

test('needs-input 优先级高于 stalled', () => {
  const s = assessHealth({
    updatedAt: iso(STALLED_AFTER_MS * 3),
    pendingApprovals: 2,
    now: NOW,
  })
  assert.equal(s.state, 'needs-input')
})

test('active=false / 缺 updatedAt → idle 弱化', () => {
  assert.equal(assessHealth({ active: false, updatedAt: iso(1000), now: NOW }).state, 'idle')
  assert.equal(assessHealth({ now: NOW }).state, 'idle')
})

test('hasError → error（低于 stalled）', () => {
  assert.equal(assessHealth({ hasError: true, updatedAt: iso(30_000), now: NOW }).state, 'error')
  // stalled 更早判定：无响应超阈值时优先 stalled
  assert.equal(
    assessHealth({ hasError: true, updatedAt: iso(STALLED_AFTER_MS + 5_000), now: NOW }).state,
    'stalled',
  )
})

test('summarizeHealth 聚合计数与 hasAttention', () => {
  const mk = (state) => ({ state, tone: 'muted', icon: '', action: '', since: '', sinceMs: 0 })
  const summary = summarizeHealth([
    mk('needs-input'),
    mk('needs-input'),
    mk('stalled'),
    mk('running'),
    mk('idle'),
  ])
  assert.deepEqual(summary, {
    needsInput: 2,
    stalled: 1,
    running: 1,
    hasAttention: true,
  })
  assert.equal(summarizeHealth([mk('running')]).hasAttention, false)
})
