/**
 * 移动离线持久化集成测试（P1 核心场景）：
 *
 *   断网 → 本地创建 session + 发 prompt（全部落 SQLite）
 *        → 恢复网络 → syncSessions + drainOutbox 自动收敛
 *        → 断言上游状态与本地镜像一致，且重复 drain 不产生重复上游实体
 *
 * 后端用 node:http 起一个实现真实路由契约的假服务（幂等键重放缓存、
 * since 增量过滤），与 backend/internal/server/mobile_session_handler.go
 * 的行为一一对应；客户端走真实 fetch + createMobileOutboxSenders。
 */
import { strict as assert } from 'node:assert'
import http from 'node:http'
import { test } from 'node:test'

import { NodeSqlDb } from './helpers.mjs'
import { SqliteMobileSyncStore } from '../mobileSync.ts'
import { syncSessions } from '../mobileSync.ts'
import { SqliteOutboxStore } from '../outboxStore.ts'
import { createMobileOutboxSenders, drainOutbox } from '../outboxDrain.ts'
import { createSessionLocally, enqueuePromptLocally } from '../mobileOffline.ts'

const WS = 'ws-a'
const INST = 'inst-1'

// ---------------------------------------------------------------------------
// 假后端：镜像真实 backend 的幂等/增量语义
// ---------------------------------------------------------------------------

function startFakeBackend() {
  // 上游真相源：sessionId -> {title, timeUpdated}
  const upstream = new Map()
  const idemCache = new Map() // idempotencyKey -> sessionId
  const prompts = [] // {sessionId, text}
  let createCalls = 0
  let clock = 10_000

  const server = http.createServer((req, res) => {
    const url = new URL(req.url, 'http://localhost')
    const send = (status, body, headers = {}) => {
      res.writeHead(status, { 'Content-Type': 'application/json', ...headers })
      res.end(JSON.stringify(body))
    }

    if (req.method === 'POST' && url.pathname === '/api/mobile/sessions') {
      const key = req.headers['idempotency-key'] ?? ''
      createCalls++
      if (key && idemCache.has(key)) {
        const id = idemCache.get(key)
        return send(200, { id, time: { updated: upstream.get(id).timeUpdated } }, { 'Idempotency-Replayed': 'true' })
      }
      clock += 1
      const id = `ses_srv_${upstream.size + 1}`
      upstream.set(id, { title: '', timeUpdated: clock })
      if (key) idemCache.set(key, id)
      return send(200, { id, time: { updated: clock } })
    }

    if (req.method === 'GET' && url.pathname === '/api/mobile/sessions') {
      const since = Number(url.searchParams.get('since') ?? 0)
      const rows = [...upstream.entries()]
        .filter(([, v]) => v.timeUpdated > since)
        .map(([id, v]) => ({ id, title: v.title, status: 'idle', timeUpdatedMs: v.timeUpdated }))
      return send(200, { data: rows, total: rows.length, sinceMs: since, serverTimeMs: clock })
    }

    const promptMatch = url.pathname.match(/^\/api\/mobile\/sessions\/([^/]+)\/prompt$/)
    if (req.method === 'POST' && promptMatch) {
      const sessionId = decodeURIComponent(promptMatch[1])
      if (!upstream.has(sessionId)) return send(404, { code: 'not_found' })
      let body = ''
      req.on('data', (c) => (body += c))
      req.on('end', () => {
        clock += 1
        const { text } = JSON.parse(body || '{}')
        prompts.push({ sessionId, text })
        send(202, { messageID: `msg_srv_${prompts.length}`, sessionID: sessionId })
      })
      return
    }

    send(404, { code: 'not_found' })
  })

  return {
    upstream,
    prompts,
    stats: {
      get createCalls() { return createCalls },
    },
    start: () => new Promise((resolve) => server.listen(0, '127.0.0.1', resolve)),
    close: () => new Promise((resolve) => server.close(resolve)),
    port: () => server.address().port,
  }
}

// ---------------------------------------------------------------------------
// 真实 fetch 的 AuthorizedFetch 适配
// ---------------------------------------------------------------------------

function fetchAdapter(baseUrl) {
  return async (input, init = {}) => {
    const res = await fetch(baseUrl + input, {
      method: init.method ?? 'GET',
      headers: init.headers,
      body: init.body,
    })
    return { status: res.status, json: async () => (await res.json()) }
  }
}

test('offline create → online sync → upstream consistency (end-to-end)', async (t) => {
  const backend = startFakeBackend()
  await backend.start()
  t.after(() => backend.close())

  const db = new NodeSqlDb()
  const store = new SqliteMobileSyncStore(db)
  const outbox = new SqliteOutboxStore(db)

  // ------------------------------------------------------------------
  // 阶段 1：断网。离线创建会话 + 发送 prompt，全部落本地。
  // ------------------------------------------------------------------
  let online = false
  const doFetch = async (input, init) => {
    if (!online) throw new Error('fetch failed: network unreachable')
    return fetchAdapter(`http://127.0.0.1:${backend.port()}`)(input, init)
  }
  const senders = createMobileOutboxSenders({ doFetch, syncStore: store })

  const session = await createSessionLocally({
    store,
    outbox,
    workspaceId: WS,
    instanceId: INST,
    title: 'offline plan',
    now: 1000,
  })
  const message = await enqueuePromptLocally({
    store,
    outbox,
    workspaceId: WS,
    instanceId: INST,
    sessionClientId: session.id,
    text: '帮我把这个想法整理成方案',
    now: 1000,
  })

  // 断网时 drain：全部退避，不丢数据。
  const offlineDrain = await drainOutbox(outbox, {
    workspaceId: WS,
    senders,
    now: () => 2000,
  })
  assert.equal(offlineDrain.retried >= 1, true)
  assert.equal(backend.stats.createCalls, 0)
  assert.equal(await outbox.countByState(['queued']), 2)
  assert.equal(message.state, 'pending')

  // 断网时 syncSessions：push 失败抛出，本地行保持 dirty（宿主捕获）。
  await assert.rejects(
    () => syncSessions(store, { ...httpTransport(doFetch) }, { workspaceId: WS, instanceId: INST }),
  )
  assert.equal((await store.findSessionById(session.id)).dirty, true)

  // ------------------------------------------------------------------
  // 阶段 2：网络恢复。drain 自动重放 outbox；sync 收敛上游镜像。
  // prompt 依赖 session.create 先完成（同 created_at 时顺序不定），
  // 宿主按退避节奏反复 drain 直到队列收敛 —— 这里模拟同样的循环。
  // ------------------------------------------------------------------
  online = true
  let clock = 200_000 // 跳过退避窗口
  let totalSucceeded = 0
  for (let round = 0; round < 5; round++) {
    const drain = await drainOutbox(outbox, { workspaceId: WS, senders, now: () => clock })
    totalSucceeded += drain.succeeded
    if ((await outbox.countByState(['queued'])) === 0) break
    clock += 61_000
  }
  assert.equal(totalSucceeded, 2, 'session.create 与 session.prompt 都应重放成功')
  assert.equal(await outbox.countByState(['queued', 'dead_letter']), 0)

  const sync1 = await syncSessions(store, httpTransport(doFetch), { workspaceId: WS, instanceId: INST })
  assert.equal(sync1.pushedCreates, 0, 'outbox 已创建，sync 无需再推')

  // ------------------------------------------------------------------
  // 阶段 3：验证上游一致性。
  // ------------------------------------------------------------------
  // 上游恰有一个会话，且 prompt 已送达该会话。
  assert.equal(backend.upstream.size, 1)
  const [upstreamId] = [...backend.upstream.keys()]
  assert.equal(backend.prompts.length, 1)
  assert.equal(backend.prompts[0].sessionId, upstreamId)
  assert.equal(backend.prompts[0].text, '帮我把这个想法整理成方案')

  // 本地行已绑定 serverId、清 dirty；消息已确认 sent。
  const localSession = await store.findSessionById(session.id)
  assert.equal(localSession.serverId, upstreamId)
  assert.equal(localSession.dirty, false)
  assert.ok(localSession.serverRev > 0)

  const msgRows = await db.all('SELECT * FROM local_mobile_messages WHERE id = ?', [message.id])
  assert.equal(msgRows[0].state, 'sent')
  assert.equal(msgRows[0].server_message_id, 'msg_srv_1')

  // pull 镜像与上游一致（游标推进，再次 sync 为空操作）。
  const sync2 = await syncSessions(store, httpTransport(doFetch), { workspaceId: WS, instanceId: INST })
  assert.equal(sync2.pushedCreates, 0)
  assert.equal(sync2.pulledUpserts, 0)
  assert.equal(backend.upstream.size, 1)

  // ------------------------------------------------------------------
  // 阶段 4：重复 drain / 重复入队同一幂等键 → 不产生重复上游实体。
  // ------------------------------------------------------------------
  clock += 120_000
  const { enqueue } = await import('../../utils/outbox.ts')
  // 同一幂等键重复入队（UI 双击 / 重放风暴）：put 幂等合并为一条。
  await outbox.put(
    enqueue(
      {
        workspaceId: WS,
        action: 'session.create',
        payload: { clientId: session.id, instanceId: INST },
        idempotencyKey: session.idempotencyKey,
      },
      clock,
    ),
  )
  const before = backend.upstream.size
  const createsBefore = backend.stats.createCalls
  const drain2 = await drainOutbox(outbox, { workspaceId: WS, senders, now: () => clock })
  assert.equal(drain2.succeeded, 1, '重复入队的记录重放一次')
  assert.equal(backend.upstream.size, before, '上游不产生第二个会话')
  assert.equal(backend.stats.createCalls, createsBefore + 1, '到达后端，但被幂等缓存去重')
  assert.equal((await store.findSessionById(session.id)).serverId, upstreamId)
})

test('replayed create with the same idempotency key does not duplicate upstream', async (t) => {
  const backend = startFakeBackend()
  await backend.start()
  t.after(() => backend.close())

  const db = new NodeSqlDb()
  const store = new SqliteMobileSyncStore(db)
  const outbox = new SqliteOutboxStore(db)
  const doFetch = fetchAdapter(`http://127.0.0.1:${backend.port()}`)

  await createSessionLocally({ store, outbox, workspaceId: WS, instanceId: INST, now: 1000 })
  // 直接用同一 idempotencyKey 重复入队（模拟重放风暴中的重复投递）。
  const { enqueue } = await import('../../utils/outbox.ts')
  const ready = await outbox.listReady(2000, 10)
  const dup = enqueue(
    {
      workspaceId: WS,
      action: 'session.create',
      payload: { clientId: 'loc_dup', instanceId: INST },
      idempotencyKey: ready[0].idempotencyKey,
    },
    5000,
  )
  // put 幂等：同 key 更新而非插入第二条。
  await outbox.put(dup)
  assert.equal((await outbox.listReady(6000, 10)).length, 1)
})

// ---------------------------------------------------------------------------
// 工具：由 AuthorizedFetch 构造 SyncTransport
// ---------------------------------------------------------------------------

function httpTransport(doFetch) {
  return {
    listSessions: async ({ instanceId, sinceMs }) => {
      const res = await doFetch(`/api/mobile/sessions?instance_id=${encodeURIComponent(instanceId)}&since=${sinceMs}`)
      if (res.status !== 200) throw new Error(`list sessions http_${res.status}`)
      const body = await res.json()
      return {
        sessions: (body.data ?? []).map((r) => ({
          id: r.id,
          title: r.title,
          status: r.status,
          timeUpdatedMs: r.timeUpdatedMs,
        })),
      }
    },
    createSession: async ({ instanceId, idempotencyKey }) => {
      const res = await doFetch(`/api/mobile/sessions?instance_id=${encodeURIComponent(instanceId)}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey },
        body: '{}',
      })
      if (res.status !== 200) throw new Error(`create session http_${res.status}`)
      const body = await res.json()
      return { id: body.id, title: '', status: 'idle', timeUpdatedMs: body.time?.updated ?? 0 }
    },
    deleteSession: async ({ instanceId, serverId }) => {
      const res = await doFetch(`/api/mobile/sessions/${encodeURIComponent(serverId)}?instance_id=${encodeURIComponent(instanceId)}`, {
        method: 'DELETE',
      })
      if (res.status !== 204 && res.status !== 200) throw new Error(`delete session http_${res.status}`)
    },
  }
}
