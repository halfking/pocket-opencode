/**
 * mobileSyncRuntime 集成测试：离线触发跳过 → 联网 trigger 一轮收敛
 * （drain outbox + syncSessions + 审批快照回填）。
 * 跑真实 SQLite + 假 fetch 服务，不依赖 DOM / Capacitor。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import { NodeSqlDb } from './helpers.mjs'
import { MobileSyncRuntime } from '../mobileSyncRuntime.ts'
import { SqliteOutboxStore } from '../outboxStore.ts'
import { SqliteMobileSyncStore } from '../mobileSync.ts'
import { SqliteApprovalStore } from '../approvalStore.ts'
import { createSessionLocally, enqueuePromptLocally, enqueueApprovalReplyLocally } from '../mobileOffline.ts'

const WS = 'ws-a'
const INST = 'inst-1'
const TOKEN = 'jwt-token'

/**
 * 假后端：内存态的上游会话 / 审批，模拟 /api/mobile/* 路由。
 * 幂等创建用 Idempotency-Key 去重（与生产 Go 行为一致）。
 */
function makeFakeBackend() {
  const upstreamSessions = new Map() // id -> {id,title,status,timeUpdatedMs}
  const idempotencyCache = new Map() // key -> session id
  const repliedApprovals = new Set()
  const pendingPermissions = new Map() // id -> request
  let upstreamSessionCount = 0

  return {
    addPendingPermission(request) {
      pendingPermissions.set(request.id, request)
    },
    clearPendingPermissions() {
      pendingPermissions.clear()
    },
    sessionCount() {
      return upstreamSessionCount
    },
    upstreamSessions,
    fetch: async (url, init = {}) => {
      const method = init.method ?? 'GET'
      const u = new URL(url, 'http://fake.local')
      const path = u.pathname

      if (path === '/api/mobile/sessions' && method === 'GET') {
        const since = Number(u.searchParams.get('since') ?? '0')
        const data = [...upstreamSessions.values()]
          .filter((s) => s.timeUpdatedMs > since)
          .sort((a, b) => a.timeUpdatedMs - b.timeUpdatedMs)
        return { status: 200, json: async () => ({ data, total: data.length, sinceMs: since }) }
      }
      if (path === '/api/mobile/sessions' && method === 'POST') {
        const key = init.headers?.['Idempotency-Key'] ?? ''
        if (idempotencyCache.has(key)) {
          return { status: 200, json: async () => upstreamSessions.get(idempotencyCache.get(key)) }
        }
        upstreamSessionCount++
        const created = {
          id: `ses_srv_${upstreamSessionCount}`,
          title: '',
          status: 'idle',
          timeUpdatedMs: Date.now(),
        }
        upstreamSessions.set(created.id, created)
        idempotencyCache.set(key, created.id)
        return { status: 200, json: async () => created }
      }
      if (path.startsWith(`/api/mobile/sessions/`) && method === 'DELETE') {
        const id = path.split('/')[3]
        upstreamSessions.delete(id)
        return { status: 204, json: async () => ({}) }
      }
      if (path.startsWith('/api/mobile/sessions/') && method === 'POST') {
        // prompt：返回 messageID 游标。
        return { status: 202, json: async () => ({ messageID: `msg_${Date.now()}` }) }
      }
      if (path === '/api/mobile/approvals' && method === 'GET') {
        return {
          status: 200,
          json: async () => ({
            permissions: [...pendingPermissions.values()],
            questions: [],
          }),
        }
      }
      if (path.startsWith('/api/mobile/approvals/permission/') && method === 'POST') {
        const id = path.split('/')[5]
        if (!pendingPermissions.has(id)) {
          return { status: 404, json: async () => ({}) }
        }
        if (repliedApprovals.has(id)) {
          return { status: 409, json: async () => ({}) }
        }
        repliedApprovals.add(id)
        pendingPermissions.delete(id)
        return { status: 200, json: async () => ({ confirmed: true }) }
      }
      return { status: 404, json: async () => ({}) }
    },
  }
}

function makeRuntime({ online, backend }) {
  const db = new NodeSqlDb()
  const events = []
  const runtime = new MobileSyncRuntime({
    isOnline: () => online.value,
    isReady: () => true,
    auth: () => ({ token: TOKEN, workspaceId: WS }),
    db: () => db,
    fetchImpl: (...args) => backend.fetch(...args),
    apiBase: '',
    onEvent: (e) => events.push(e),
  })
  return { db, runtime, events }
}

test('trigger is skipped while offline, then converges after network recovery', async () => {
  const backend = makeFakeBackend()
  const online = { value: false }
  const { db, runtime, events } = makeRuntime({ online, backend })

  // 离线：建会话 + 发 prompt + 审批回复。
  const sync = new SqliteMobileSyncStore(db)
  const outbox = new SqliteOutboxStore(db)
  const approvals = new SqliteApprovalStore(db)
  backend.addPendingPermission({ id: 'per_1', sessionID: 'ses_x', action: 'bash', resources: ['ls'] })

  const session = await createSessionLocally({
    store: sync, outbox, workspaceId: WS, instanceId: INST, title: 'offline work', now: 1000,
  })
  await enqueuePromptLocally({
    store: sync, outbox, workspaceId: WS, instanceId: INST,
    sessionClientId: session.id, text: 'do the thing', now: 1100,
  })
  await enqueuePromptLocally({
    store: sync, outbox, workspaceId: WS, instanceId: INST,
    sessionClientId: session.id, text: 'do the thing', now: 1100,
  })

  // 离线 trigger：跳过，队列不变。
  await runtime.trigger('network-online')
  assert.ok(events.some((e) => e.type === 'skipped' && e.reason.includes('offline')))
  assert.equal(await outbox.countByState(['queued']), 3)
  assert.equal(backend.sessionCount(), 0)

  // 网络恢复：一轮 drain + sync 收敛。
  online.value = true
  await runtime.trigger('network-online')

  const drained = events.find((e) => e.type === 'drained')
  assert.ok(drained, 'drain event emitted')
  assert.equal(drained.succeeded, 3)
  assert.equal(backend.sessionCount(), 1, '幂等创建只产生一个上游会话')
  assert.equal(await outbox.countByState(['queued', 'inflight']), 0)

  const bound = await sync.findSessionById(session.id)
  assert.equal(bound.serverId, 'ses_srv_1')
  assert.equal(bound.dirty, false)

  const status = runtime.getStatus()
  assert.equal(status.pendingCount, 0)
  assert.equal(status.lastError, '')
})

test('trigger backfills approval snapshots from server pending list', async () => {
  const backend = makeFakeBackend()
  backend.addPendingPermission({ id: 'per_1', sessionID: 'ses_x', action: 'edit', resources: ['/tmp/f'] })
  const online = { value: true }
  const { db, runtime, events } = makeRuntime({ online, backend })

  // 先有本地会话行，runtime 才会同步该实例（同步范围 = 本地已知实例）。
  const sync = new SqliteMobileSyncStore(db)
  const outbox = new SqliteOutboxStore(db)
  await createSessionLocally({
    store: sync, outbox, workspaceId: WS, instanceId: INST, title: 'anchor', now: 1000,
  })

  await runtime.trigger('app-resume')

  const backfill = events.find((e) => e.type === 'approvals-backfilled' && e.instanceId === INST)
  assert.ok(backfill, 'approvals backfilled')
  assert.equal(backfill.upserted, 1)

  const approvals = new SqliteApprovalStore(db)
  const row = await approvals.find('per_1')
  assert.equal(row.state, 'pending')
  assert.equal(row.payload.action, 'edit')

  // 服务端 pending 消失后再次 trigger：本地行过期。
  backend.clearPendingPermissions()
  await runtime.trigger('app-resume')
  assert.equal((await approvals.find('per_1')).state, 'expired')
})

test('trigger is skipped when unauthenticated or db locked', async () => {
  const backend = makeFakeBackend()
  const db = new NodeSqlDb()
  const events = []
  const runtime = new MobileSyncRuntime({
    isOnline: () => true,
    isReady: () => true,
    auth: () => null,
    db: () => db,
    fetchImpl: (...args) => backend.fetch(...args),
    onEvent: (e) => events.push(e),
  })
  await runtime.trigger('manual')
  assert.ok(events.some((e) => e.type === 'skipped' && e.reason.includes('unauthenticated')))

  const runtime2 = new MobileSyncRuntime({
    isOnline: () => true,
    isReady: () => false,
    auth: () => ({ token: TOKEN, workspaceId: WS }),
    db: () => null,
    fetchImpl: (...args) => backend.fetch(...args),
    onEvent: (e) => events.push(e),
  })
  await runtime2.trigger('manual')
  assert.ok(events.some((e) => e.type === 'skipped' && e.reason.includes('db-locked')))
})

test('approval reply drained through runtime marks local snapshot sent', async () => {
  const backend = makeFakeBackend()
  backend.addPendingPermission({ id: 'per_2', sessionID: 'ses_x', action: 'bash', resources: ['ls'] })
  const online = { value: true }
  const { db, runtime, events } = makeRuntime({ online, backend })

  const sync = new SqliteMobileSyncStore(db)
  const outbox = new SqliteOutboxStore(db)
  const approvals = new SqliteApprovalStore(db)
  // 锚点会话：runtime 的同步范围 = 本地已知实例。
  await createSessionLocally({
    store: sync, outbox, workspaceId: WS, instanceId: INST, title: 'anchor', now: 1000,
  })

  // 在线 trigger 回填快照（此时看到这条审批）。
  await runtime.trigger('app-resume')
  assert.ok(events.some((e) => e.type === 'approvals-backfilled'))
  // 手工重放服务端 pending（trigger 已把锚点会话 drain 掉）。
  backend.addPendingPermission({ id: 'per_2', sessionID: 'ses_x', action: 'bash', resources: ['ls'] })

  await enqueueApprovalReplyLocally({
    outbox,
    workspaceId: WS,
    approvalStore: approvals,
    reply: { kind: 'permission', requestId: 'per_2', instanceId: INST, sessionId: 'ses_x', decision: 'once' },
    now: 2000,
  })
  await runtime.trigger('manual')

  const row = await approvals.find('per_2')
  assert.equal(row.state, 'sent')
  assert.equal(row.decision, 'once')
})
