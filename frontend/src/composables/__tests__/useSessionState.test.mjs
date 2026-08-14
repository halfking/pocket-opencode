/**
 * PR7 session state surface tests (pure helpers mirror).
 *
 * The composable wraps Vue refs and requires a Vue runtime; we test
 * the pure helpers (deriveWaitingReason, worstPhase) and the
 * AsyncState-style transitions the composable emits.
 */

import { strict as assert } from 'node:assert'
import { test } from 'node:test'

// Mirror of deriveWaitingReason / worstPhase (kept in sync with the
// composable file).

const deriveWaitingReason = (item) => {
  if (!item.hasPending) return null
  if (item.pendingType === 'permission') return 'approval'
  if (item.pendingType === 'question') return 'question'
  return 'approval'
}

const worstPhase = (items) => {
  for (const it of items) {
    if (it.hasPending) return 'waiting_user'
  }
  return 'idle'
}

const sample = (overrides = {}) => ({
  id: 's-1',
  instanceId: 'i-1',
  title: '会话',
  updatedAt: Date.now(),
  hasPending: false,
  ...overrides,
})

test('deriveWaitingReason: no pending → null', () => {
  assert.equal(deriveWaitingReason(sample()), null)
})

test('deriveWaitingReason: permission → approval', () => {
  assert.equal(deriveWaitingReason(sample({ hasPending: true, pendingType: 'permission' })), 'approval')
})

test('deriveWaitingReason: question → question', () => {
  assert.equal(deriveWaitingReason(sample({ hasPending: true, pendingType: 'question' })), 'question')
})

test('deriveWaitingReason: pending without type → approval (default)', () => {
  assert.equal(deriveWaitingReason(sample({ hasPending: true })), 'approval')
})

test('worstPhase: empty list → idle', () => {
  assert.equal(worstPhase([]), 'idle')
})

test('worstPhase: no pending → idle', () => {
  const items = [sample(), sample({ id: 's-2' })]
  assert.equal(worstPhase(items), 'idle')
})

test('worstPhase: any pending → waiting_user', () => {
  const items = [sample(), sample({ id: 's-2', hasPending: true, pendingType: 'question' })]
  assert.equal(worstPhase(items), 'waiting_user')
})

test('worstPhase: first pending short-circuits', () => {
  const items = [
    sample({ id: 'a', hasPending: true }),
    sample({ id: 'b' }),
  ]
  assert.equal(worstPhase(items), 'waiting_user')
})

// Aggregate mapping mirror (matches aggregate() in useSessionState).

const aggregateForPhase = (phase) => {
  switch (phase) {
    case 'loading': return 'running'
    case 'refreshing': return 'running'
    case 'ready':
    case 'empty':
      return 'idle'
    case 'error':
      return 'failed'
    case 'offline':
      return 'unknown'
    default:
      return 'unknown'
  }
}

test('aggregate: loading → running', () => assert.equal(aggregateForPhase('loading'), 'running'))
test('aggregate: refreshing → running', () => assert.equal(aggregateForPhase('refreshing'), 'running'))
test('aggregate: ready → idle', () => assert.equal(aggregateForPhase('ready'), 'idle'))
test('aggregate: empty → idle', () => assert.equal(aggregateForPhase('empty'), 'idle'))
test('aggregate: error → failed', () => assert.equal(aggregateForPhase('error'), 'failed'))
test('aggregate: offline → unknown', () => assert.equal(aggregateForPhase('offline'), 'unknown'))
