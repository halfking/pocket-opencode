/**
 * PR5 idempotent WS bus tests (pure ESM mirror).
 *
 * Run with: `node --test frontend/src/services/__tests__/idempotentWsBus.test.mjs`
 */

import { strict as assert } from 'node:assert'
import { test } from 'node:test'

// ---- mirror of pure helpers (no DOM, no wsClient) ----

const normalizeEnvelope = (raw) => {
  if (!raw || typeof raw !== 'object') return null
  const r = raw
  if (r.v === 1 && typeof r.type === 'string') return raw
  if (typeof r.type === 'string') {
    return {
      v: 0,
      id: typeof r.id === 'string' ? r.id : undefined,
      ts: typeof r.ts === 'number' ? r.ts : Date.now(),
      channel: typeof r.channel === 'string' ? r.channel : 'legacy',
      topic: typeof r.topic === 'string' ? r.topic : '',
      type: r.type,
      data: r.payload ?? r.data ?? null,
    }
  }
  return null
}

const createDispatchLog = (retentionMs = 5 * 60 * 1000) => ({
  byEventId: new Map(),
  byActionId: new Map(),
  retentionMs,
})

const recordEnvelope = (log, envelope, now = Date.now()) => {
  if (envelope.id) {
    const prior = log.byEventId.get(envelope.id)
    if (prior) return { duplicate: true }
    log.byEventId.set(envelope.id, { envelope, receivedAt: now })
  }
  const cause = envelope.cause
  const actionId = cause?.action_id || cause?.approval_id
  if (actionId) log.byActionId.set(actionId, envelope.id || `${now}`)
  if (log.byEventId.size >= 1024) {
    const cutoff = now - log.retentionMs
    for (const [id, rec] of log.byEventId) {
      if (rec.receivedAt < cutoff) log.byEventId.delete(id)
    }
    if (log.byEventId.size >= 1024) {
      const sorted = Array.from(log.byEventId.entries()).sort(
        (a, b) => a[1].receivedAt - b[1].receivedAt,
      )
      const toDrop = log.byEventId.size - 512
      for (let i = 0; i < toDrop; i++) log.byEventId.delete(sorted[i][0])
    }
  }
  return { duplicate: false }
}

// ---- normalize ----

test('normalizeEnvelope: rejects non-object', () => {
  assert.equal(normalizeEnvelope(null), null)
  assert.equal(normalizeEnvelope('string'), null)
  assert.equal(normalizeEnvelope(42), null)
})

test('normalizeEnvelope: v1 envelope passes through', () => {
  const raw = { v: 1, id: '01H', ts: 1, channel: 'c', topic: 't', type: 'approval.asked', data: {} }
  assert.deepEqual(normalizeEnvelope(raw), raw)
})

test('normalizeEnvelope: legacy { type, payload } becomes v0', () => {
  const env = normalizeEnvelope({ type: 'note.created', payload: { id: 'n1' } })
  assert.equal(env.v, 0)
  assert.equal(env.type, 'note.created')
  assert.deepEqual(env.data, { id: 'n1' })
})

test('normalizeEnvelope: unknown shape returns null', () => {
  assert.equal(normalizeEnvelope({ foo: 1 }), null)
})

// ---- dedupe ----

test('recordEnvelope: first id → not duplicate', () => {
  const log = createDispatchLog()
  const env = { v: 1, id: '01A', type: 'x', data: {} }
  assert.deepEqual(recordEnvelope(log, env), { duplicate: false })
})

test('recordEnvelope: same id → duplicate', () => {
  const log = createDispatchLog()
  const env = { v: 1, id: '01A', type: 'x', data: {} }
  recordEnvelope(log, env)
  assert.deepEqual(recordEnvelope(log, env), { duplicate: true })
})

test('recordEnvelope: action_id is recorded', () => {
  const log = createDispatchLog()
  const env = { v: 1, id: '01A', type: 'x', cause: { action_id: 'aid-1' }, data: {} }
  recordEnvelope(log, env)
  assert.equal(log.byActionId.get('aid-1'), '01A')
})

test('recordEnvelope: action_id overwritten by newer event', () => {
  const log = createDispatchLog()
  recordEnvelope(log, { v: 1, id: '01A', type: 'x', cause: { action_id: 'aid-1' }, data: {} })
  recordEnvelope(log, { v: 1, id: '02B', type: 'x', cause: { action_id: 'aid-1' }, data: {} })
  assert.equal(log.byActionId.get('aid-1'), '02B')
})

test('recordEnvelope: trims old entries when over capacity', () => {
  // Use a long retention so the LRU branch (not the time cutoff) is what
  // triggers trimming. With 1500 entries and a 1h retention, the time
  // cutoff never fires but the size cap does.
  const log = createDispatchLog(60 * 60 * 1000)
  const now = Date.now()
  for (let i = 0; i < 1500; i++) {
    recordEnvelope(log, { v: 1, id: `e-${i}`, type: 'x', data: {} }, now)
  }
  // The LRU branch should drop the oldest entries.
  assert.equal(log.byEventId.has('e-0'), false)
  assert.equal(log.byEventId.has('e-500'), false)
  // The newest ones must remain.
  assert.equal(log.byEventId.has('e-1499'), true)
  assert.ok(log.byEventId.size < 1500)
})

// ---- subscriber fan-out (mock wsClient) ----

test('subscribe: callbacks invoked once per id even with replay', () => {
  const log = createDispatchLog()
  const calls = []
  const listener = (env) => calls.push(env.id)
  // Simulate the bus dispatcher:
  const dispatch = (msg) => {
    const env = normalizeEnvelope(msg)
    if (!env) return
    if (recordEnvelope(log, env).duplicate) return
    listener(env)
  }

  const msg = { v: 1, id: '01A', type: 'approval.asked', data: { foo: 1 } }
  dispatch(msg)
  dispatch({ ...msg }) // exact replay
  dispatch({ v: 1, id: '02B', type: 'approval.asked', data: { foo: 2 } }) // different id

  assert.deepEqual(calls, ['01A', '02B'])
})

test('subscribe: unknown shape is silently dropped, no throw', () => {
  const log = createDispatchLog()
  let thrown = null
  try {
    const env = normalizeEnvelope({})
    if (!env) return
    recordEnvelope(log, env)
  } catch (e) {
    thrown = e
  }
  assert.equal(thrown, null)
})

test('subscribe: legacy and v1 envelopes both reach the same dispatcher', () => {
  const log = createDispatchLog()
  const seen = []
  const dispatch = (msg) => {
    const env = normalizeEnvelope(msg)
    if (!env) return
    if (recordEnvelope(log, env).duplicate) return
    seen.push(env.type)
  }

  dispatch({ type: 'note.created', payload: { id: 'n1' } })
  dispatch({ v: 1, id: '01', type: 'email.classified', data: { email_id: 'e1' } })
  assert.deepEqual(seen, ['note.created', 'email.classified'])
})
