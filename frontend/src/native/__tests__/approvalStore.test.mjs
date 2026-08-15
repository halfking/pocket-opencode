/**
 * approvalStore 单元测试：pull 回填、本地决定、drain 终态回写。
 * 跑真实 SQLite（node:sqlite）+ 真实模块。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import { NodeSqlDb } from './helpers.mjs'
import {
  SqliteApprovalStore,
  backfillApprovals,
} from '../approvalStore.ts'
import { SqliteMobileSyncStore } from '../mobileSync.ts'
import { SqliteOutboxStore } from '../outboxStore.ts'
import { createMobileOutboxSenders } from '../outboxDrain.ts'
import { enqueueApprovalReplyLocally } from '../mobileOffline.ts'
import { drainOutbox } from '../outboxDrain.ts'

const WS = 'ws-a'
const INST = 'inst-1'

function newStores() {
  const db = new NodeSqlDb()
  return {
    db,
    approvals: new SqliteApprovalStore(db),
    sync: new SqliteMobileSyncStore(db),
    outbox: new SqliteOutboxStore(db),
  }
}

function perm(id, overrides = {}) {
  return { id, sessionID: 'ses_1', action: 'bash', resources: ['ls /tmp'], ...overrides }
}

// ---------------------------------------------------------------------------
// backfillApprovals（pull 回填）
// ---------------------------------------------------------------------------

test('backfill upserts server pending snapshots', async () => {
  const { approvals } = newStores()
  const result = await backfillApprovals(approvals, {
    workspaceId: WS,
    instanceId: INST,
    server: { permissions: [perm('per_1')], questions: [{ id: 'que_1', sessionID: 'ses_1', questions: [] }] },
    now: 1000,
  })
  assert.equal(result.upserted, 2)
  assert.equal(result.expired, 0)

  const row = await approvals.find('per_1')
  assert.equal(row.kind, 'permission')
  assert.equal(row.state, 'pending')
  assert.equal(row.sessionId, 'ses_1')
  assert.equal(row.payload.action, 'bash')

  const q = await approvals.find('que_1')
  assert.equal(q.kind, 'question')
})

test('backfill re-pull keeps local decision and does not duplicate', async () => {
  const { approvals } = newStores()
  await backfillApprovals(approvals, {
    workspaceId: WS,
    instanceId: INST,
    server: { permissions: [perm('per_1')], questions: [] },
    now: 1000,
  })
  await approvals.saveDecision('per_1', 'always', 2000)
  await approvals.markReplied('per_1', 'always', 2000)

  // 服务端 pending 列表里已没有该行：不覆盖本地 sent 终态。
  await backfillApprovals(approvals, {
    workspaceId: WS,
    instanceId: INST,
    server: { permissions: [], questions: [] },
    now: 3000,
  })
  const row = await approvals.find('per_1')
  assert.equal(row.state, 'sent')
  assert.equal(row.decision, 'always')
  assert.notEqual(row.repliedAt, null)
})

test('backfill expires local pending rows missing from server list (same instance only)', async () => {
  const { approvals } = newStores()
  await backfillApprovals(approvals, {
    workspaceId: WS,
    instanceId: INST,
    server: { permissions: [perm('per_1'), perm('per_2')], questions: [] },
    now: 1000,
  })
  // 另一实例的 pending 行不应被本次回填过期。
  await approvals.upsertSnapshot({
    id: 'per_other',
    workspaceId: WS,
    instanceId: 'inst-2',
    sessionId: 'ses_1',
    kind: 'permission',
    payload: {},
    decision: null,
    state: 'pending',
    createdAt: 900,
    updatedAt: 900,
    repliedAt: null,
  })

  const result = await backfillApprovals(approvals, {
    workspaceId: WS,
    instanceId: INST,
    server: { permissions: [perm('per_1')], questions: [] },
    now: 2000,
  })
  assert.equal(result.expired, 1)

  assert.equal((await approvals.find('per_1')).state, 'pending')
  assert.equal((await approvals.find('per_2')).state, 'expired')
  assert.equal((await approvals.find('per_other')).state, 'pending')
})

test('backfill does not expire rows with a local decision', async () => {
  const { approvals } = newStores()
  await backfillApprovals(approvals, {
    workspaceId: WS,
    instanceId: INST,
    server: { permissions: [perm('per_1')], questions: [] },
    now: 1000,
  })
  await approvals.saveDecision('per_1', 'once', 1500)
  const result = await backfillApprovals(approvals, {
    workspaceId: WS,
    instanceId: INST,
    server: { permissions: [], questions: [] },
    now: 2000,
  })
  assert.equal(result.expired, 0)
  assert.equal((await approvals.find('per_1')).state, 'pending')
  assert.equal((await approvals.find('per_1')).decision, 'once')
})

// ---------------------------------------------------------------------------
// 离线回复 → drain → 终态回写
// ---------------------------------------------------------------------------

function fakeFetch(routes) {
  return async (input, init) => {
    for (const route of routes) {
      if (typeof input === 'string' && input.startsWith(route.match)) {
        return {
          status: route.status,
          json: async () => route.body ?? {},
        }
      }
    }
    return { status: 404, json: async () => ({}) }
  }
}

test('offline reply + drain success marks approval sent', async () => {
  const { approvals, sync, outbox } = newStores()
  await backfillApprovals(approvals, {
    workspaceId: WS,
    instanceId: INST,
    server: { permissions: [perm('per_1')], questions: [] },
    now: 1000,
  })

  await enqueueApprovalReplyLocally({
    outbox,
    workspaceId: WS,
    approvalStore: approvals,
    reply: {
      kind: 'permission',
      requestId: 'per_1',
      instanceId: INST,
      sessionId: 'ses_1',
      decision: 'always',
    },
    now: 1500,
  })
  // 本地决定已记录，但服务端确认前仍是 pending（08 §4.5）。
  assert.equal((await approvals.find('per_1')).decision, 'always')
  assert.equal((await approvals.find('per_1')).state, 'pending')

  const senders = createMobileOutboxSenders({
    doFetch: fakeFetch([{ match: '/api/mobile/approvals/permission/per_1', status: 200, body: { confirmed: true } }]),
    syncStore: sync,
    approvalStore: approvals,
  })
  const result = await drainOutbox(outbox, { workspaceId: WS, senders })
  assert.equal(result.succeeded, 1)

  const row = await approvals.find('per_1')
  assert.equal(row.state, 'sent')
  assert.equal(row.decision, 'always')
  assert.notEqual(row.repliedAt, null)
})

test('drain 409 (no longer pending) marks approval expired', async () => {
  const { approvals, sync, outbox } = newStores()
  await backfillApprovals(approvals, {
    workspaceId: WS,
    instanceId: INST,
    server: { permissions: [perm('per_9')], questions: [] },
    now: 1000,
  })
  await enqueueApprovalReplyLocally({
    outbox,
    workspaceId: WS,
    approvalStore: approvals,
    reply: { kind: 'permission', requestId: 'per_9', instanceId: INST, sessionId: 'ses_1', decision: 'once' },
    now: 1500,
  })

  const senders = createMobileOutboxSenders({
    doFetch: fakeFetch([{ match: '/api/mobile/approvals/permission/per_9', status: 409, body: {} }]),
    syncStore: sync,
    approvalStore: approvals,
  })
  const result = await drainOutbox(outbox, { workspaceId: WS, senders })
  assert.equal(result.deadLettered, 1)
  assert.equal((await approvals.find('per_9')).state, 'expired')
})
