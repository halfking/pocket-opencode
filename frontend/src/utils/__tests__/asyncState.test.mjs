/**
 * PR3 helper tests (pure Node ESM).
 *
 * Runs with: `node --test src/utils/__tests__/asyncState.test.mjs`
 *
 * Mirrors the contents of asyncState.ts as plain ESM, so the tests do
 * not depend on tsx/ts-node. Whenever asyncState.ts changes, mirror the
 * relevant logic here. This is the cheapest way to add regression
 * coverage without introducing a new dependency.
 */

import { strict as assert } from 'node:assert'
import { test } from 'node:test'

// ---- mirror of asyncState.ts (kept small on purpose) ----

const idleAsyncState = () => ({
  phase: 'idle',
  data: null,
  previous: null,
  error: null,
  requestId: null,
  updatedAt: null,
})

const isNewerRequest = (incoming, current) => {
  if (!current) return true
  return incoming > current
}

const startLoading = (prev, requestId, isRefresh) => {
  if (prev.requestId !== null && prev.requestId !== requestId && !isNewerRequest(requestId, prev.requestId)) {
    return prev
  }
  return { ...prev, phase: isRefresh ? 'refreshing' : 'loading', error: null, requestId }
}

const succeedAsync = (prev, requestId, data, isEmpty) => {
  if (prev.requestId !== null && prev.requestId !== requestId && !isNewerRequest(requestId, prev.requestId)) {
    return prev
  }
  return {
    ...prev,
    phase: isEmpty(data) ? 'empty' : 'ready',
    data,
    previous: prev.data ?? prev.previous,
    error: null,
    requestId,
    updatedAt: Date.now(),
  }
}

const failAsync = (prev, requestId, err) => {
  if (prev.requestId !== null && prev.requestId !== requestId && !isNewerRequest(requestId, prev.requestId)) {
    return prev
  }
  return {
    ...prev,
    phase: 'error',
    error: { ...err, requestId },
    requestId,
    data: prev.data,
  }
}

const goOffline = (prev) => (prev.phase === 'offline' ? prev : { ...prev, phase: 'offline' })
const goOnline = (prev) => {
  if (prev.phase !== 'offline') return prev
  const next = prev.error ? 'error' : prev.data ? 'ready' : 'idle'
  return { ...prev, phase: next }
}

const cancelledAsync = (prev) => ({ ...prev, requestId: null })

const newRequestId = () => {
  const t = Date.now().toString(36)
  const r = Math.random().toString(36).slice(2, 10)
  return `${t}-${r}`
}

// Helper: produce strictly monotonically increasing ids for tests.
let __seq = 0
const nextId = () => `id-${++__seq}-${Date.now().toString(36)}`

const toAsyncError = (err) => {
  if (err && typeof err === 'object' && 'code' in err) {
    const e = err
    const isRetryable = (s) => {
      if (typeof s !== 'number') return true
      if (s === 0) return true
      if (s === 408 || s === 425 || s === 429) return true
      if (s >= 500) return true
      return false
    }
    return {
      code: typeof e.code === 'string' ? e.code : 'unknown_error',
      message: typeof e.message === 'string' ? e.message : '未知错误，请稍后重试。',
      retryable: typeof e.retryable === 'boolean' ? e.retryable : isRetryable(e.status),
      status: typeof e.status === 'number' ? e.status : undefined,
    }
  }
  if (err instanceof Error) {
    return { code: 'unknown_error', message: err.message || '未知错误，请稍后重试。', retryable: true }
  }
  return { code: 'unknown_error', message: '未知错误，请稍后重试。', retryable: true }
}

const describePhase = (state) => {
  switch (state.phase) {
    case 'idle': return ''
    case 'loading': return '正在加载…'
    case 'ready': return ''
    case 'empty': return '当前没有内容'
    case 'error': return state.error?.message || '加载失败'
    case 'refreshing': return '正在刷新…'
    case 'offline': return '离线，仅本地内容可见'
  }
}

// ---- tests ----

test('isNewerRequest: null/empty current always accepts', () => {
  assert.equal(isNewerRequest('01', null), true)
  assert.equal(isNewerRequest('01', undefined), true)
  assert.equal(isNewerRequest('01', ''), true)
})

test('isNewerRequest: lexical comparison', () => {
  assert.equal(isNewerRequest('b', 'a'), true)
  assert.equal(isNewerRequest('a', 'a'), false)
  assert.equal(isNewerRequest('a', 'b'), false)
})

test('idle -> loading -> ready', () => {
  let s = idleAsyncState()
  assert.equal(s.phase, 'idle')
  const id = nextId()
  s = startLoading(s, id, false)
  assert.equal(s.phase, 'loading')
  s = succeedAsync(s, id, [1, 2, 3], (v) => v.length === 0)
  assert.equal(s.phase, 'ready')
  assert.deepEqual(s.data, [1, 2, 3])
})

test('ready -> refreshing preserves data', () => {
  let s = idleAsyncState()
  const id1 = nextId()
  s = startLoading(s, id1, false)
  s = succeedAsync(s, id1, [1, 2, 3], (v) => v.length === 0)
  const id2 = nextId()
  s = startLoading(s, id2, true)
  assert.equal(s.phase, 'refreshing')
  assert.deepEqual(s.data, [1, 2, 3])
})

test('empty transition when data is empty', () => {
  let s = idleAsyncState()
  const id = nextId()
  s = startLoading(s, id, false)
  s = succeedAsync(s, id, [], (v) => v.length === 0)
  assert.equal(s.phase, 'empty')
})

test('error preserves previous data and metadata', () => {
  let s = idleAsyncState()
  const id1 = nextId()
  s = startLoading(s, id1, false)
  s = succeedAsync(s, id1, [1], (v) => v.length === 0)
  const id2 = nextId()
  s = startLoading(s, id2, true)
  s = failAsync(s, id2, { code: 'rate_limited', message: '稍后重试', retryable: true, status: 429 })
  assert.equal(s.phase, 'error')
  assert.deepEqual(s.data, [1])
  assert.equal(s.error.code, 'rate_limited')
  assert.equal(s.error.status, 429)
})

test('stale response does not overwrite newer state', () => {
  let s = idleAsyncState()
  const a = "00-a"
  const b = "00-b"
  s = startLoading(s, b, false)
  s = succeedAsync(s, b, [9], (v) => v.length === 0)
  s = startLoading(s, a, true)
  s = succeedAsync(s, a, [1, 2, 3], (v) => v.length === 0)
  assert.deepEqual(s.data, [9])
})

test('cancel only clears requestId', () => {
  let s = idleAsyncState()
  const id = nextId()
  s = startLoading(s, id, false)
  s = cancelledAsync(s)
  assert.equal(s.phase, 'loading')
  assert.equal(s.requestId, null)
})

test('offline preserves data and toggles back', () => {
  let s = idleAsyncState()
  const id = nextId()
  s = startLoading(s, id, false)
  s = succeedAsync(s, id, [1], (v) => v.length === 0)
  s = goOffline(s)
  assert.equal(s.phase, 'offline')
  assert.deepEqual(s.data, [1])
  s = goOnline(s)
  assert.equal(s.phase, 'ready')
})

test('toAsyncError: Error -> unknown_error', () => {
  const e = toAsyncError(new Error('boom'))
  assert.equal(e.code, 'unknown_error')
  assert.equal(e.message, 'boom')
  assert.equal(e.retryable, true)
})

test('toAsyncError: object with code/retryable/status', () => {
  const e = toAsyncError({ code: 'capability_denied', message: 'no', retryable: false, status: 403 })
  assert.equal(e.code, 'capability_denied')
  assert.equal(e.retryable, false)
  assert.equal(e.status, 403)
})

test('toAsyncError: status 500 inferred retryable', () => {
  const e = toAsyncError({ code: 'upstream_unavailable', message: 'fail', status: 500 })
  assert.equal(e.retryable, true)
})

test('toAsyncError: status 403 inferred non-retryable', () => {
  const e = toAsyncError({ code: 'capability_denied', message: 'deny', status: 403 })
  assert.equal(e.retryable, false)
})

test('toAsyncError: network status 0 retryable', () => {
  const e = toAsyncError({ code: 'upstream_unavailable', message: 'offline', status: 0 })
  assert.equal(e.retryable, true)
})

test('describePhase produces expected messages', () => {
  const base = idleAsyncState()
  assert.equal(describePhase({ ...base, phase: 'loading' }), '正在加载…')
  assert.equal(describePhase({ ...base, phase: 'empty' }), '当前没有内容')
  assert.equal(describePhase({ ...base, phase: 'offline' }), '离线，仅本地内容可见')
  assert.equal(
    describePhase({ ...base, phase: 'error', error: { code: 'x', message: 'err', retryable: true } }),
    'err',
  )
  assert.equal(describePhase({ ...base, phase: 'ready' }), '')
})
