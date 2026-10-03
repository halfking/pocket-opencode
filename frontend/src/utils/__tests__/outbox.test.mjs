/**
 * PR13 outbox primitives tests (pure ESM mirror).
 */

import { strict as assert } from 'node:assert'
import { test } from 'node:test'

// Mirror of outbox.ts.

const enqueue = (opts, now = Date.now()) => ({
  id: `id-${now}-${Math.random().toString(36).slice(2, 8)}`,
  idempotencyKey: opts.idempotencyKey,
  workspaceId: opts.workspaceId,
  action: opts.action,
  payload: opts.payload,
  createdAt: now,
  nextAttemptAt: now + (opts.delayMs ?? 0),
  attempts: 0,
  state: 'queued',
  ttlMs: opts.ttlMs ?? 24 * 60 * 60 * 1000,
})

const claim = (record) => record.state === 'inflight'
  ? record
  : { ...record, state: 'inflight', attempts: record.attempts + 1 }

const succeed = (record, cursor) => ({
  ...record, state: 'succeeded',
  cursor: cursor ?? record.cursor, lastError: undefined,
})

const backoffMs = (record, attempt, base = 1000, cap = 60000) => {
  const exp = Math.min(cap, base * 2 ** Math.max(0, attempt - 1))
  return Math.floor(Math.random() * exp)
}

const shouldDeadLetter = (record, now, maxAttempts = 8) => {
  if (record.attempts >= maxAttempts) return true
  if (now - record.createdAt > record.ttlMs) return true
  return false
}

const failForRetry = (record, errorCode, now = Date.now()) => ({
  ...record, state: 'queued', lastError: errorCode,
  nextAttemptAt: now + backoffMs(record, record.attempts),
})

const deadLetter = (record, errorCode) => ({
  ...record, state: 'dead_letter', lastError: errorCode,
})

const matchesWorkspace = (record, workspaceId) => record.workspaceId === workspaceId

test('enqueue: builds a queued record with required fields', () => {
  const r = enqueue({
    workspaceId: 'ws-1', action: 'note.create',
    payload: { title: 't' }, idempotencyKey: 'k-1',
  }, 1000)
  assert.equal(r.state, 'queued')
  assert.equal(r.attempts, 0)
  assert.equal(r.workspaceId, 'ws-1')
  assert.equal(r.action, 'note.create')
  assert.equal(r.idempotencyKey, 'k-1')
  assert.equal(r.createdAt, 1000)
  assert.equal(r.nextAttemptAt, 1000) // default no delay
})

test('enqueue: respects delayMs', () => {
  const r = enqueue({
    workspaceId: 'ws-1', action: 'note.create',
    payload: {}, idempotencyKey: 'k-2', delayMs: 5000,
  }, 1000)
  assert.equal(r.nextAttemptAt, 6000)
})

test('enqueue: respects ttlMs', () => {
  const r = enqueue({
    workspaceId: 'ws-1', action: 'note.create',
    payload: {}, idempotencyKey: 'k-3', ttlMs: 60_000,
  })
  assert.equal(r.ttlMs, 60_000)
})

test('claim: increments attempts and switches to inflight', () => {
  const r = enqueue({ workspaceId: 'w', action: 'x', payload: {}, idempotencyKey: 'k' })
  const claimed = claim(r)
  assert.equal(claimed.state, 'inflight')
  assert.equal(claimed.attempts, 1)
  // claim of an already inflight record is a no-op
  const again = claim(claimed)
  assert.equal(again.attempts, 1)
})

test('succeed: sets state and preserves cursor', () => {
  const r = enqueue({ workspaceId: 'w', action: 'x', payload: {}, idempotencyKey: 'k' })
  const c = claim(r)
  const s = succeed(c, 'cursor-42')
  assert.equal(s.state, 'succeeded')
  assert.equal(s.cursor, 'cursor-42')
})

test('failForRetry: 退回 queued，退避落在 [now, now+base) 且允许为 0', () => {
  // 旧断言是 `f.nextAttemptAt > c.createdAt`，而 backoffMs 是 **full jitter**：
  //   Math.floor(Math.random() * exp)，exp = 1000 ⇒ 取到 0 的概率 1/1000。
  // enqueue 与 failForRetry 落在同一毫秒（本测试里几乎必然如此）时，
  // `next=0` 就让 nextAttemptAt === createdAt，旧断言假红。
  // 也就是说这条断言和实现保证的算法**自相矛盾**，不是实现坏了。
  // 能断言的只有两条：调度不早于 now，且不超过上限。
  const r = enqueue({ workspaceId: 'w', action: 'x', payload: {}, idempotencyKey: 'k' }, 1000)
  const c = claim(r) // attempts = 1
  const now = 20_000
  const f = failForRetry(c, 'network', now)
  assert.equal(f.state, 'queued')
  assert.equal(f.lastError, 'network')
  assert.ok(f.nextAttemptAt >= now, `nextAttemptAt=${f.nextAttemptAt} 早于 now=${now}`)
  assert.ok(f.nextAttemptAt < now + 1000, `nextAttemptAt=${f.nextAttemptAt} 超出 base 上限`)

  // 负控：jitter 取 0 时 nextAttemptAt === now 是**合法**取值。
  // 这条把契约钉死——若哪天实现改成保证正延迟，这里会红并提示同步更新测试。
  const realRandom = Math.random
  Math.random = () => 0
  try {
    assert.equal(failForRetry(c, 'network', now).nextAttemptAt, now)
  } finally {
    Math.random = realRandom
  }
})

test('backoffMs: 落在 [0, exp) 内并被 cap 封顶', () => {
  // 只验区间，不验「一定为正」——那正是旧断言的错处。
  for (let i = 0; i < 200; i++) {
    const v = backoffMs({ attempts: 1 }, 1, 1000, 5000)
    assert.ok(Number.isInteger(v) && v >= 0 && v < 1000, `越界: ${v}`)
  }
  const big = backoffMs({ attempts: 20 }, 20, 20, 1000)
  assert.ok(big >= 0 && big <= 1000)
})

test('shouldDeadLetter: by attempts', () => {
  const r = { attempts: 10, createdAt: 0, ttlMs: 999999999 }
  assert.equal(shouldDeadLetter(r, 1), true)
})

test('shouldDeadLetter: by ttl', () => {
  const r = { attempts: 1, createdAt: 0, ttlMs: 1000 }
  assert.equal(shouldDeadLetter(r, 2000), true)
})

test('shouldDeadLetter: still healthy', () => {
  const r = { attempts: 1, createdAt: 1000, ttlMs: 60000 }
  assert.equal(shouldDeadLetter(r, 2000), false)
})

test('deadLetter: terminal state', () => {
  const r = enqueue({ workspaceId: 'w', action: 'x', payload: {}, idempotencyKey: 'k' })
  const d = deadLetter(r, 'permanent_4xx')
  assert.equal(d.state, 'dead_letter')
  assert.equal(d.lastError, 'permanent_4xx')
})

test('matchesWorkspace: true on match', () => {
  const r = enqueue({ workspaceId: 'ws-1', action: 'x', payload: {}, idempotencyKey: 'k' })
  assert.equal(matchesWorkspace(r, 'ws-1'), true)
})

test('matchesWorkspace: false on switch', () => {
  const r = enqueue({ workspaceId: 'ws-1', action: 'x', payload: {}, idempotencyKey: 'k' })
  assert.equal(matchesWorkspace(r, 'ws-2'), false)
})
