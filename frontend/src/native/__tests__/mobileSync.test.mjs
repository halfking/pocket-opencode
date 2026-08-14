/**
 * mobileSync 单元测试：LWW merge、pull 游标、push 脏行收敛。
 * 全部跑真实 SQLite（node:sqlite）+ 真实模块。
 */
import { strict as assert } from 'node:assert'
import { test } from 'node:test'

import { NodeSqlDb } from './helpers.mjs'
import {
  SqliteMobileSyncStore,
  mergeSessionRow,
  pushSessions,
  pullSessions,
  syncSessions,
} from '../mobileSync.ts'

const WS = 'ws-a'
const INST = 'inst-1'

function newStore() {
  return new SqliteMobileSyncStore(new NodeSqlDb())
}

function remoteRow(overrides = {}) {
  return { id: 'ses_up_1', title: 't', status: 'idle', timeUpdatedMs: 1000, ...overrides }
}

function localDirtyRow(overrides = {}) {
  return {
    id: 'loc_abc',
    serverId: null,
    workspaceId: WS,
    instanceId: INST,
    title: 'offline note',
    status: 'idle',
    idempotencyKey: 'sess_loc_abc',
    clientRev: 1,
    serverRev: 0,
    dirty: true,
    createdAt: 100,
    updatedAt: 200,
    deletedAt: null,
    ...overrides,
  }
}

// ---------------------------------------------------------------------------
// mergeSessionRow（纯函数）
// ---------------------------------------------------------------------------

test('mergeSessionRow inserts a new synced row when local is missing', () => {
  const merged = mergeSessionRow(null, remoteRow())
  assert.equal(merged.id, 'ses_up_1')
  assert.equal(merged.serverId, 'ses_up_1')
  assert.equal(merged.serverRev, 1000)
  assert.equal(merged.dirty, false)
})

test('mergeSessionRow never overwrites a dirty local row', () => {
  const local = localDirtyRow({ serverId: 'ses_up_1', serverRev: 500 })
  assert.equal(mergeSessionRow(local, remoteRow({ timeUpdatedMs: 2000 })), null)
})

test('mergeSessionRow overwrites a clean local row only when remote is newer', () => {
  const clean = localDirtyRow({ dirty: false, serverId: 'ses_up_1', serverRev: 1000 })
  assert.equal(mergeSessionRow(clean, remoteRow({ timeUpdatedMs: 1000 })), null)
  assert.equal(mergeSessionRow(clean, remoteRow({ timeUpdatedMs: 999 })), null)
  const merged = mergeSessionRow(clean, remoteRow({ timeUpdatedMs: 1500, title: 'renamed' }))
  assert.equal(merged.title, 'renamed')
  assert.equal(merged.serverRev, 1500)
})

test('mergeSessionRow rejects invalid remote rows', () => {
  assert.equal(mergeSessionRow(null, remoteRow({ id: '' })), null)
  assert.equal(mergeSessionRow(null, remoteRow({ timeUpdatedMs: 0 })), null)
})

// ---------------------------------------------------------------------------
// pullSessions：游标高水位 + 增量
// ---------------------------------------------------------------------------

test('pullSessions applies upstream rows and advances the data cursor', async () => {
  const store = newStore()
  const transport = {
    listSessions: async () => ({
      sessions: [
        remoteRow({ id: 'ses_1', timeUpdatedMs: 100 }),
        remoteRow({ id: 'ses_2', timeUpdatedMs: 200 }),
      ],
    }),
    createSession: async () => { throw new Error('unused') },
    deleteSession: async () => {},
  }
  const r1 = await pullSessions(store, transport, { workspaceId: WS, instanceId: INST })
  assert.equal(r1.upserts, 2)
  assert.equal(r1.cursor, 200)
  assert.equal(await store.getCursor(`mobile_sessions:${INST}`), 200)

  // 第二次 pull：无新数据 → 0 upsert，游标不变。
  const r2 = await pullSessions(store, transport, { workspaceId: WS, instanceId: INST })
  assert.equal(r2.upserts, 0)
  assert.equal(r2.cursor, 200)
})

test('pullSessions skips rows at or below the cursor (server-side since equivalent)', async () => {
  const store = newStore()
  await store.setCursor(`mobile_sessions:${INST}`, 200)
  const calls = []
  const transport = {
    listSessions: async ({ sinceMs }) => {
      calls.push(sinceMs)
      const all = [
        remoteRow({ id: 'ses_1', timeUpdatedMs: 100 }),
        remoteRow({ id: 'ses_2', timeUpdatedMs: 300 }),
      ]
      return { sessions: all.filter((s) => s.timeUpdatedMs > sinceMs) }
    },
    createSession: async () => { throw new Error('unused') },
    deleteSession: async () => {},
  }
  const r = await pullSessions(store, transport, { workspaceId: WS, instanceId: INST })
  assert.deepEqual(calls, [200])
  assert.equal(r.upserts, 1)
})

test('pullSessions does not clobber a dirty offline row with the same serverId', async () => {
  const store = newStore()
  await store.insertSession(localDirtyRow({ serverId: 'ses_1', serverRev: 100 }))
  const transport = {
    listSessions: async () => ({ sessions: [remoteRow({ id: 'ses_1', timeUpdatedMs: 900 })] }),
    createSession: async () => { throw new Error('unused') },
    deleteSession: async () => {},
  }
  const r = await pullSessions(store, transport, { workspaceId: WS, instanceId: INST })
  assert.equal(r.skippedDirty, 1)
  const row = await store.findSessionById('loc_abc')
  assert.equal(row.dirty, true)
  assert.equal(row.serverRev, 100)
})

// ---------------------------------------------------------------------------
// pushSessions：离线创建 → 绑定 serverId；墓碑 → 上游删除
// ---------------------------------------------------------------------------

test('pushSessions creates offline sessions upstream and binds serverId', async () => {
  const store = newStore()
  await store.insertSession(localDirtyRow())
  const created = []
  const transport = {
    listSessions: async () => ({ sessions: [] }),
    createSession: async ({ idempotencyKey }) => {
      created.push(idempotencyKey)
      return remoteRow({ id: 'ses_new', timeUpdatedMs: 5000 })
    },
    deleteSession: async () => { throw new Error('unused') },
  }
  const r = await pushSessions(store, transport, { workspaceId: WS, instanceId: INST })
  assert.equal(r.creates, 1)
  assert.deepEqual(created, ['sess_loc_abc'])
  const row = await store.findSessionById('loc_abc')
  assert.equal(row.serverId, 'ses_new')
  assert.equal(row.dirty, false)
  assert.equal(row.serverRev, 5000)
})

test('pushSessions deletes tombstones upstream when serverId is bound', async () => {
  const store = newStore()
  await store.insertSession(localDirtyRow({ serverId: 'ses_del', deletedAt: 123 }))
  const deleted = []
  const transport = {
    listSessions: async () => ({ sessions: [] }),
    createSession: async () => { throw new Error('unused') },
    deleteSession: async ({ serverId }) => { deleted.push(serverId) },
  }
  const r = await pushSessions(store, transport, { workspaceId: WS, instanceId: INST })
  assert.equal(r.deletes, 1)
  assert.deepEqual(deleted, ['ses_del'])
  assert.equal(await store.findSessionById('loc_abc'), null)
})

test('pushSessions drops local-only tombstones without upstream calls', async () => {
  const store = newStore()
  await store.insertSession(localDirtyRow({ deletedAt: 123 }))
  const transport = {
    listSessions: async () => ({ sessions: [] }),
    createSession: async () => { throw new Error('must not create') },
    deleteSession: async () => { throw new Error('must not delete') },
  }
  const r = await pushSessions(store, transport, { workspaceId: WS, instanceId: INST })
  assert.equal(r.creates, 0)
  assert.equal(r.deletes, 0)
  assert.equal(await store.findSessionById('loc_abc'), null)
})

test('pushSessions only replays rows of the target instance', async () => {
  const store = newStore()
  await store.insertSession(localDirtyRow({ instanceId: 'inst-other' }))
  const transport = {
    listSessions: async () => ({ sessions: [] }),
    createSession: async () => { throw new Error('must not create') },
    deleteSession: async () => {},
  }
  const r = await pushSessions(store, transport, { workspaceId: WS, instanceId: INST })
  assert.equal(r.creates, 0)
})

// ---------------------------------------------------------------------------
// syncSessions 端到端（内存 transport）
// ---------------------------------------------------------------------------

test('syncSessions converges offline create then upstream rename without conflicts', async () => {
  const store = newStore()
  await store.insertSession(localDirtyRow())

  let upstreamTitle = 'offline note'
  let upstreamTime = 5000
  const upstream = new Map() // id -> {title, updated}
  const transport = {
    createSession: async ({ idempotencyKey }) => {
      assert.equal(idempotencyKey, 'sess_loc_abc')
      const id = 'ses_synced'
      upstream.set(id, { title: upstreamTitle, updated: upstreamTime })
      return remoteRow({ id, title: upstreamTitle, timeUpdatedMs: upstreamTime })
    },
    deleteSession: async () => {},
    listSessions: async () => {
      const sessions = []
      for (const [id, v] of upstream) {
        sessions.push(remoteRow({ id, title: v.title, timeUpdatedMs: v.updated }))
      }
      return { sessions }
    },
  }

  // 第一轮：push 创建 + pull 回填。
  const r1 = await syncSessions(store, transport, { workspaceId: WS, instanceId: INST })
  assert.equal(r1.pushedCreates, 1)

  // 上游随后被重命名（时间戳更新）。
  upstreamTime = 9000
  upstreamTitle = 'renamed upstream'
  upstream.set('ses_synced', { title: upstreamTitle, updated: upstreamTime })
  const r2 = await syncSessions(store, transport, { workspaceId: WS, instanceId: INST })
  assert.equal(r2.pushedCreates, 0)
  assert.equal(r2.pulledUpserts, 1)
  const row = await store.findSessionById('loc_abc')
  assert.equal(row.title, 'renamed upstream')
  assert.equal(row.serverRev, 9000)
  assert.equal(row.dirty, false)
})
