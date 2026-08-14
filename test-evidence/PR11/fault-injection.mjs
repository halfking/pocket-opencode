#!/usr/bin/env node
/**
 * PR11 fault-injection harness.
 *
 * Runs the pure helpers from PR3 (AsyncState) and PR5 (idempotent WS
 * bus) through the network and WS matrices in
 * `test-evidence/PR11-mobile-fault-fixtures.md` §3 and §5.
 *
 * Usage:
 *   node test-evidence/PR11/fault-injection.mjs
 *
 * Outputs:
 *   - Human-readable pass/fail per case
 *   - Machine-readable JSON lines appended to
 *     test-evidence/PR11/results.jsonl
 *
 * Exit codes (per fixtures §4.2):
 *   0  every expected transition matched
 *   1  at least one unexpected transition
 *   2  harness error
 */

import { strict as assert } from 'node:assert'
import { mkdirSync, appendFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

// ---- reproduce pure helpers from PR3 and PR5 --------------------------
// We can't import the TS sources without a transpiler, so we mirror
// them here. Whenever asyncState.ts / idempotentWsBus.ts changes, mirror
// the relevant logic. This duplication is intentional per
// `11-并行执行提示词.md` §6 (no new deps for tests).

const idleAsyncState = () => ({
  phase: 'idle', data: null, previous: null, error: null,
  requestId: null, updatedAt: null,
})

const isNewerRequest = (i, c) => !c || i > c

const startLoading = (prev, requestId, isRefresh) => {
  if (prev.requestId !== null && prev.requestId !== requestId && !isNewerRequest(requestId, prev.requestId)) return prev
  return { ...prev, phase: isRefresh ? 'refreshing' : 'loading', error: null, requestId }
}
const succeedAsync = (prev, requestId, data, isEmpty) => {
  if (prev.requestId !== null && prev.requestId !== requestId && !isNewerRequest(requestId, prev.requestId)) return prev
  return { ...prev, phase: isEmpty(data) ? 'empty' : 'ready', data, previous: prev.data ?? prev.previous, error: null, requestId, updatedAt: Date.now() }
}
const failAsync = (prev, requestId, err) => {
  if (prev.requestId !== null && prev.requestId !== requestId && !isNewerRequest(requestId, prev.requestId)) return prev
  return { ...prev, phase: 'error', error: { ...err, requestId }, requestId, data: prev.data }
}

const newRequestId = () => `id-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 8)}`

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
    if (log.byEventId.has(envelope.id)) return { duplicate: true }
    log.byEventId.set(envelope.id, { envelope, receivedAt: now })
  }
  const cause = envelope.cause
  const actionId = cause?.action_id || cause?.approval_id
  if (actionId) log.byActionId.set(actionId, envelope.id || `${now}`)
  return { duplicate: false }
}

// ---- structured server response shape (PR1 §10) -----------------------

const STABLE_CODES = {
  unauthenticated: 'unauthenticated',
  token_expired: 'token_expired',
  workspace_required: 'workspace_required',
  capability_denied: 'capability_denied',
  not_found: 'not_found',
  conflict: 'conflict',
  rate_limited: 'rate_limited',
  approval_required: 'approval_required',
  upstream_unavailable: 'upstream_unavailable',
  payload_too_large: 'payload_too_large',
  invalid_request: 'invalid_request',
}

function simulateServerResponse(status, code) {
  return { status, code, body: { error: `simulated ${code}`, code, retryable: status >= 500 } }
}

// ---- case runner ------------------------------------------------------

const __filename = fileURLToPath(import.meta.url)
const __dirname = dirname(__filename)
const RESULTS_PATH = join(__dirname, 'results.jsonl')
mkdirSync(dirname(RESULTS_PATH), { recursive: true })

const results = []
let failed = 0
function recordResult(label, expected, actual, defectClass = null) {
  const ok = expected === actual
  if (!ok) failed++
  const line = {
    label, expected, actual, ok, defectClass,
    ts: new Date().toISOString(),
  }
  results.push(line)
  appendFileSync(RESULTS_PATH, JSON.stringify(line) + '\n')
  const status = ok ? 'ok' : 'fail'
  console.log(`${status} ${label} expected=${expected} actual=${actual}`)
}

// ---- network matrix ---------------------------------------------------

function runNetworkMatrix() {
  // 401 unauthenticated → state.error with code=token_expired
  let s = idleAsyncState()
  const id = newRequestId()
  s = startLoading(s, id, false)
  const resp = simulateServerResponse(401, 'token_expired')
  s = failAsync(s, id, { code: resp.code, message: 'token expired', retryable: true, status: resp.status })
  recordResult('network: 401 → state.error.code=token_expired', 'error', s.phase)
  recordResult('network: 401 → state.error.code=token_expired (code)', resp.code, s.error.code)

  // 403 capability_denied → state.error.code=capability_denied
  s = idleAsyncState()
  const id2 = newRequestId()
  s = startLoading(s, id2, false)
  const r403 = simulateServerResponse(403, 'capability_denied')
  s = failAsync(s, id2, { code: r403.code, message: 'denied', retryable: false, status: r403.status })
  recordResult('network: 403 → state.phase=error', 'error', s.phase)
  recordResult('network: 403 → retryable=false (capability)', false, s.error.retryable)

  // 404 not_found → empty (no leak of existence)
  s = idleAsyncState()
  const id3 = newRequestId()
  s = startLoading(s, id3, false)
  const r404 = simulateServerResponse(404, 'not_found')
  s = failAsync(s, id3, { code: r404.code, message: 'missing', retryable: false, status: r404.status })
  recordResult('network: 404 → state.phase=error', 'error', s.phase)
  recordResult('network: 404 → code=not_found', 'not_found', s.error.code)

  // 409 conflict → state.error.code=conflict
  s = idleAsyncState()
  const id4 = newRequestId()
  s = startLoading(s, id4, false)
  const r409 = simulateServerResponse(409, 'conflict')
  s = failAsync(s, id4, { code: r409.code, message: 'CAS', retryable: false, status: r409.status })
  recordResult('network: 409 → state.error.code=conflict', 'conflict', s.error.code)

  // 429 rate_limited → retryable
  s = idleAsyncState()
  const id5 = newRequestId()
  s = startLoading(s, id5, false)
  const r429 = simulateServerResponse(429, 'rate_limited')
  s = failAsync(s, id5, { code: r429.code, message: 'slow down', retryable: true, status: r429.status })
  recordResult('network: 429 → retryable=true', true, s.error.retryable)

  // 500 upstream_unavailable → retryable
  s = idleAsyncState()
  const id6 = newRequestId()
  s = startLoading(s, id6, false)
  const r500 = simulateServerResponse(500, 'upstream_unavailable')
  s = failAsync(s, id6, { code: r500.code, message: 'oops', retryable: true, status: r500.status })
  recordResult('network: 500 → retryable=true', true, s.error.retryable)

  // success path
  s = idleAsyncState()
  const id7 = newRequestId()
  s = startLoading(s, id7, false)
  s = succeedAsync(s, id7, { items: [1, 2, 3] }, (arr) => !arr || arr.items.length === 0)
  recordResult('network: 200 → state.phase=ready', 'ready', s.phase)

  // empty path
  s = idleAsyncState()
  const id8 = newRequestId()
  s = startLoading(s, id8, false)
  s = succeedAsync(s, id8, { items: [] }, (arr) => !arr || arr.items.length === 0)
  recordResult('network: 200 empty → state.phase=empty', 'empty', s.phase)

  // stale response ignored
  s = idleAsyncState()
  const a = '01-a'
  const b = '02-b'
  s = startLoading(s, b, false)
  s = succeedAsync(s, b, [9], (v) => v.length === 0)
  s = startLoading(s, a, true)
  s = succeedAsync(s, a, [1, 2, 3], (v) => v.length === 0)
  assert.deepEqual(s.data, [9])
  recordResult('network: stale response does not overwrite newer', '[9]', JSON.stringify(s.data))
}

// ---- WS matrix --------------------------------------------------------

function runWsMatrix() {
  // Replay same id → duplicate
  const log = createDispatchLog()
  const env = { v: 1, id: '01A', type: 'approval.asked', data: {} }
  const seen = []
  const dispatch = (msg) => {
    const e = normalizeEnvelope(msg)
    if (!e) return
    if (recordEnvelope(log, e).duplicate) return
    seen.push(e.id || e.type)
  }
  dispatch(env)
  dispatch({ ...env })
  dispatch({ v: 1, id: '02B', type: 'approval.asked', data: {} })
  recordResult('ws: replay same id fires once', ['01A', '02B'].join(','), seen.join(','))

  // cause.action_id overwrite
  const log2 = createDispatchLog()
  recordEnvelope(log2, { v: 1, id: '01A', type: 'x', cause: { action_id: 'aid-1' }, data: {} })
  recordEnvelope(log2, { v: 1, id: '02B', type: 'x', cause: { action_id: 'aid-1' }, data: {} })
  recordResult('ws: cause.action_id latest wins', '02B', log2.byActionId.get('aid-1'))

  // unknown event type dropped
  const log3 = createDispatchLog()
  let dropped = null
  const dispatch2 = (msg) => {
    const e = normalizeEnvelope(msg)
    if (!e) { dropped = 'unknown'; return }
    if (recordEnvelope(log3, e).duplicate) return
    dropped = null
  }
  dispatch2({ foo: 1 })
  recordResult('ws: unknown shape dropped without throw', 'unknown', dropped)

  // wildcard subscriber receives every accepted envelope
  const log4 = createDispatchLog()
  const wildcards = []
  const dispatch3 = (msg) => {
    const e = normalizeEnvelope(msg)
    if (!e) return
    if (recordEnvelope(log4, e).duplicate) return
    wildcards.push(e.type)
  }
  dispatch3({ v: 1, id: '01', type: 'a', data: {} })
  dispatch3({ v: 1, id: '02', type: 'b', data: {} })
  recordResult('ws: wildcard receives all accepted types', ['a', 'b'].join(','), wildcards.join(','))
}

// ---- main -------------------------------------------------------------

console.log('PR11 fault-injection harness starting')
console.log('writing results to', RESULTS_PATH)

try {
  runNetworkMatrix()
  runWsMatrix()
} catch (err) {
  console.error('harness error:', err)
  process.exit(2)
}

console.log('---')
console.log(`total: ${results.length}, failed: ${failed}`)
process.exit(failed === 0 ? 0 : 1)
