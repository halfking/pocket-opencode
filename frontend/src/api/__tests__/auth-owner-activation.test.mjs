import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createServer } from 'vite'
import { createPinia, setActivePinia } from 'pinia'

const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r }); return { promise, resolve } }
const aborted = error => error?.name === 'AbortError'
async function fixture(run) {
  const originals = Object.fromEntries(['localStorage', 'window', 'fetch'].map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]))
  const values = new Map()
  const storage = { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: key => values.delete(key) }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
  const browser = new EventTarget()
  browser.location = { origin: 'http://localhost', protocol: 'http:', hostname: 'localhost' }
  Object.defineProperty(globalThis, 'window', { configurable: true, value: browser })
  const vite = await createServer({ configFile: false, server: { middlewareMode: true, hmr: false }, optimizeDeps: { noDiscovery: true } })
  setActivePinia(createPinia())
  try {
    const authModule = await vite.ssrLoadModule('/src/stores/auth.ts')
    const { localDB } = await vite.ssrLoadModule('/src/native/local-db.ts')
    const auth = authModule.useAuthStore()
    const login = name => auth.setAuthWithWorkspace(`token-${name}`, `User ${name}`, `user-${name}`, `ws-${name}`)
    await login('a')
    await run({ auth, localDB, login, values, storage, browser, vite, ...authModule })
  } finally {
    await vite.close()
    for (const [key, descriptor] of Object.entries(originals)) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor)
      else delete globalThis[key]
    }
  }
}

test('account activation closes new request and DB admission while old work drains', () => fixture(async f => {
  const gate = deferred()
  const old = f.captureAuthSession(f.auth)
  f.localDB.pending = gate.promise
  const changing = f.login('b')
  assert.equal(f.auth.activationStatus, 'pending')
  assert.equal(f.auth.isAuthenticated, false)
  assert.equal(f.auth.token, 'token-a')
  assert.equal(f.isCurrentAuthSession(f.auth, old), false)
  assert.throws(() => f.captureAuthSession(f.auth), aborted)
  assert.throws(() => f.localDB.captureOperation({ kind: 'local-edit' }), aborted)
  gate.resolve()
  await changing
  assert.equal(f.auth.activationStatus, 'ready')
  assert.equal(f.auth.token, 'token-b')
  assert.equal(f.localDB.captureOperation({ kind: 'local-edit' }).owner.principal.userId, 'user-b')
}))

test('superseded B cannot publish or clear C readiness', () => fixture(async f => {
  const gate = deferred()
  f.localDB.pending = gate.promise
  const b = assert.rejects(f.login('b'), aborted)
  const c = f.login('c')
  gate.resolve()
  await Promise.all([b, c])
  assert.equal(f.auth.token, 'token-c')
  assert.equal(f.auth.activationStatus, 'ready')
  assert.equal(f.auth.activationError, '')
}))

test('snapshot failure keeps admission closed and a successful retry can activate', () => fixture(async f => {
  const original = f.storage.setItem
  f.storage.setItem = (key, value) => { if (key === 'pocket_auth_snapshot_v1') throw new Error('quota fixture'); original(key, value) }
  await assert.rejects(f.login('b'), /could not be saved/)
  assert.equal(f.auth.activationStatus, 'error')
  assert.equal(f.auth.sessionPersistFailed, true)
  assert.equal(f.auth.isAuthenticated, false)
  assert.throws(() => f.captureAuthSession(f.auth), aborted)
  f.storage.setItem = original
  await f.login('c')
  assert.equal(f.auth.token, 'token-c')
  assert.equal(f.auth.sessionPersistFailed, false)
}))

test('atomic snapshot wins over torn compatibility keys and restore has no asserted owner', () => fixture(async f => {
  f.values.set('pocket_token', 'torn-token-b')
  f.values.set('pocket_workspace_id', 'torn-workspace-c')
  await f.auth.syncFromStorage(true)
  assert.equal(f.auth.token, 'token-a')
  assert.equal(f.auth.workspaceId, 'ws-a')
  assert.equal(f.auth.userId, '')
  assert.equal(f.localDB.captureOperation({ kind: 'local-edit' }).owner.principal, null)
}))

test('display-only update and token renewal preserve the operation owner', () => fixture(async f => {
  const before = f.localDB.captureOperation({ kind: 'local-edit' }).owner
  const session = f.captureAuthSession(f.auth)
  f.auth.updateDisplayUser('New display name')
  assert.equal(f.isCurrentAuthSession(f.auth, session), true)
  globalThis.fetch = async () => new Response(JSON.stringify({ token: 'renewed-a', user_id: 'user-a', workspace_id: 'ws-a' }), { status: 200, headers: { 'Content-Type': 'application/json' } })
  assert.equal(await f.auth.refreshSession(), true)
  assert.equal(f.localDB.captureOperation({ kind: 'local-edit' }).owner, before)
  assert.equal(f.isCurrentAuthSession(f.auth, session), true)
}))

test('logout revokes admission immediately and uses only its captured credential', () => fixture(async f => {
  const response = deferred(), gate = deferred()
  const headers = []
  globalThis.fetch = (_url, init) => { headers.push(init.headers.Authorization); return response.promise }
  f.localDB.pending = gate.promise
  const loggingOut = f.auth.logout()
  assert.equal(f.auth.activationStatus, 'pending')
  assert.throws(() => f.captureAuthSession(f.auth), aborted)
  gate.resolve()
  await new Promise(resolve => setImmediate(resolve))
  await f.login('b')
  response.resolve(new Response('{}', { status: 200 }))
  await loggingOut
  assert.deepEqual(headers, ['Bearer token-a'])
  assert.equal(f.auth.token, 'token-b')
}))

test('cleanup failure is visible and the anonymous snapshot prevents stale-key resurrection', () => fixture(async f => {
  f.storage.removeItem = () => { throw new Error('fixture removal failure') }
  await assert.rejects(f.auth.clearLocal(), /cleanup failed/)
  assert.equal(f.auth.activationStatus, 'error')
  assert.equal(f.auth.sessionClearFailed, true)
  assert.deepEqual(f.auth.sessionClearMissing, ['pocket_token', 'pocket_user', 'pocket_workspace_id', 'pocket_auth_method'])
  assert.equal(f.auth.isAuthenticated, false)
  await f.auth.syncFromStorage(true)
  assert.equal(f.auth.token, '')
}))

test('shared HTTP client cannot send while owner activation is pending', () => fixture(async f => {
  const { http } = await f.vite.ssrLoadModule('/src/api/http.ts')
  const gate = deferred()
  f.localDB.pending = gate.promise
  const change = f.login('b')
  let requests = 0
  globalThis.fetch = async () => { requests++; return new Response('{}') }
  await assert.rejects(http('/api/private'), aborted)
  assert.equal(requests, 0)
  gate.resolve(); await change
}))

test('real route helper waits for owner activation and recovery failure does not loop login', () => fixture(async f => {
  const { evaluateRoute } = await f.vite.ssrLoadModule('/src/app/routeGuards.ts')
  const gate = deferred()
  f.localDB.pending = gate.promise
  const change = f.login('b')
  const to = { path: '/private', fullPath: '/private', meta: { requiresAuth: true }, query: {} }
  let finished = false
  const routed = evaluateRoute(to).then(outcome => { finished = true; return outcome })
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(finished, false)
  gate.resolve(); await change
  assert.equal((await routed).kind, 'allow')
  f.values.set('pocket_auth_snapshot_v1', '{invalid')
  assert.equal((await evaluateRoute(to)).kind, 'redirectLogin')
  assert.equal((await evaluateRoute({ ...to, path: '/login', meta: {} })).kind, 'allow')
}))

test('WebSocket callbacks and reconnection cannot deliver through a pending/new owner', () => fixture(async f => {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'WebSocket')
  const sockets = []
  class Socket {
    static OPEN = 1; static CLOSED = 3
    readyState = 1
    constructor(url) { this.url = url; sockets.push(this) }
    close() { this.readyState = 3; this.onclose?.() }
  }
  Object.defineProperty(globalThis, 'WebSocket', { configurable: true, value: Socket })
  try {
    const { wsClient, connectWs } = await f.vite.ssrLoadModule('/src/api/websocket.ts')
    const received = []
    wsClient.on('fixture', data => received.push(data))
    connectWs()
    const old = sockets[0]
    old.onmessage({ data: JSON.stringify({ type: 'fixture', value: 'A' }) })
    const gate = deferred(); f.localDB.pending = gate.promise
    const change = f.login('b')
    old.onmessage({ data: JSON.stringify({ type: 'fixture', value: 'pending' }) })
    wsClient.disconnect()
    connectWs()
    assert.equal(sockets.length, 1)
    gate.resolve(); await change
    connectWs()
    sockets[1].onmessage({ data: JSON.stringify({ type: 'fixture', value: 'B' }) })
    assert.equal(received.length, 2)
    assert.equal(old.onclose, null)
    wsClient.disconnect()
  } finally {
    if (original) Object.defineProperty(globalThis, 'WebSocket', original); else delete globalThis.WebSocket
  }
}))

test('first snapshot failure cannot leave a mixed legacy identity for compatibility recovery', () => fixture(async f => {
  f.values.delete('pocket_auth_snapshot_v1')
  const original = f.storage.setItem
  f.storage.setItem = (key, value) => { if (key === 'pocket_auth_snapshot_v1') throw new Error('fixture atomic failure'); original(key, value) }
  await assert.rejects(f.login('b'))
  assert.equal(f.values.get('pocket_token'), 'token-a')
  assert.equal(f.values.get('pocket_workspace_id'), 'ws-a')
  f.storage.setItem = original
  await f.auth.syncFromStorage(true)
  assert.equal(f.auth.token, 'token-a')
  assert.equal(f.auth.workspaceId, 'ws-a')
  assert.equal(f.localDB.captureOperation({ kind: 'local-edit' }).owner.principal, null)
}))

test('renewal of a restored session keeps missing identity metadata and owner unasserted', () => fixture(async f => {
  await f.auth.syncFromStorage(true)
  const owner = f.localDB.captureOperation({ kind: 'local-edit' }).owner
  globalThis.fetch = async () => new Response(JSON.stringify({ token: 'renewed-a', user_id: 'user-a', workspace_id: 'ws-a' }), { status: 200, headers: { 'Content-Type': 'application/json' } })
  assert.equal(await f.auth.refreshSession(), true)
  assert.equal(f.auth.userId, '')
  assert.equal(f.localDB.captureOperation({ kind: 'local-edit' }).owner, owner)
  assert.equal(owner.principal, null)
}))

test('mobile runtime rejects pending/stale credentials and delayed old response bodies', () => fixture(async f => {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'document')
  const documentFixture = new EventTarget()
  documentFixture.visibilityState = 'hidden'
  documentFixture.documentElement = { lang: '' }
  Object.defineProperty(globalThis, 'document', { configurable: true, value: documentFixture })
  let connectivity
  try {
    const { useConnectivityStore } = await f.vite.ssrLoadModule('/src/stores/connectivity.ts')
    connectivity = useConnectivityStore()
    connectivity.init()
    const deps = connectivity.runtime.deps
    let requests = 0
    globalThis.fetch = async () => { requests++; return new Response('{}') }
    const gate = deferred(); f.localDB.pending = gate.promise
    const change = f.login('b')
    assert.equal(deps.auth(), null)
    await assert.rejects(deps.fetchImpl('/api/sessions', { headers: { Authorization: 'Bearer token-a' } }), aborted)
    gate.resolve(); await change
    await assert.rejects(deps.fetchImpl('/api/sessions', { headers: { Authorization: 'Bearer token-a' } }), aborted)
    assert.equal(requests, 0)
    const body = deferred()
    globalThis.fetch = async () => {
      const response = new Response('{}')
      response.json = async () => body.promise
      return response
    }
    const response = await deps.fetchImpl('/api/sessions', { headers: { Authorization: 'Bearer token-b' } })
    const reading = assert.rejects(response.json(), aborted)
    await f.login('c')
    body.resolve({ private: 'old B' })
    await reading
  } finally {
    connectivity?.runtime?.stop()
    if (original) Object.defineProperty(globalThis, 'document', original); else delete globalThis.document
  }
}))

test('synchronous ready/version watchers cannot reopen admission over a newer owner ticket', () => fixture(async f => {
  const { watch } = await import('vue')
  let c, observed
  const stopVersion = watch(() => f.auth.activationVersion, () => {
    if (f.auth.token === 'token-b') c = f.login('c')
  }, { flush: 'sync' })
  const stopReady = watch(() => f.auth.activationStatus, status => {
    if (status === 'ready') {
      let db = 'ready'
      try { f.localDB.captureOperation({ kind: 'local-edit' }) } catch { db = 'pending' }
      observed = { token: f.auth.token, db }
    }
  }, { flush: 'sync' })
  try {
    await assert.rejects(f.login('b'), aborted)
    await c
    assert.deepEqual(observed, { token: 'token-c', db: 'ready' })
    assert.equal(f.auth.activationStatus, 'ready')
  } finally { stopVersion(); stopReady() }
}))

test('a failed local activation can retry the real public login API without old credentials', () => fixture(async f => {
  const original = f.storage.setItem
  f.storage.setItem = () => { throw new Error('fixture storage failure') }
  await assert.rejects(f.login('b'))
  const { http } = await f.vite.ssrLoadModule('/src/api/http.ts')
  const credentials = []
  globalThis.fetch = async (_url, init) => {
    credentials.push(new Headers(init.headers).get('Authorization'))
    return new Response(JSON.stringify({ token: 'token-c', user: 'User c', user_id: 'user-c', workspace_id: 'ws-c' }), { headers: { 'Content-Type': 'application/json' } })
  }
  const response = await http('/api/auth/login', { method: 'POST', headers: { authorization: 'Bearer stale' }, body: '{}' })
  assert.deepEqual(credentials, [null])
  f.storage.setItem = original
  await f.auth.setAuthWithWorkspace(response.token, response.user, response.user_id, response.workspace_id)
  assert.equal(f.auth.isAuthenticated, true)
  assert.equal(f.auth.token, 'token-c')
}))

test('a delayed public login response is cancelled after a newer activation', () => fixture(async f => {
  const { http } = await f.vite.ssrLoadModule('/src/api/http.ts')
  const response = deferred()
  globalThis.fetch = () => response.promise
  const request = assert.rejects(http('/api/auth/login', { method: 'POST', body: '{}' }), aborted)
  await f.login('b')
  response.resolve(new Response('{}', { headers: { 'Content-Type': 'application/json' } }))
  await request
}))

test('runtime transport retains the originating session through A→B→A with equal tokens', () => fixture(async f => {
  const { createRuntimeFetch } = await f.vite.ssrLoadModule('/src/native/mobileSyncRuntime.ts')
  const session = f.captureAuthSession(f.auth)
  const origin = { token: f.auth.token, workspaceId: f.auth.workspaceId, assertCurrent: () => f.assertAuthSession(f.auth, session) }
  let requests = 0
  const doFetch = createRuntimeFetch({ fetchImpl: async () => { requests++; return new Response('{}') } }, origin)
  await f.login('b'); await f.login('a')
  await assert.rejects(doFetch('/api/sessions'), aborted)
  assert.equal(requests, 0)
}))

async function withEventSource(f, run) {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'EventSource')
  const sources = []
  class Source {
    static CLOSED = 2
    readyState = 1
    listeners = new Map()
    constructor(url) { this.url = url; sources.push(this) }
    addEventListener(name, handler) { this.listeners.set(name, handler) }
    close() { this.readyState = 2 }
  }
  Object.defineProperty(globalThis, 'EventSource', { configurable: true, value: Source })
  Object.assign(f.browser, { setInterval, setTimeout, clearInterval, clearTimeout })
  try { await run(sources) }
  finally { if (original) Object.defineProperty(globalThis, 'EventSource', original); else delete globalThis.EventSource }
}

test('session SSE does not open after an old history abort or deliver old events', () => fixture(f => withEventSource(f, async sources => {
  const { useSessionStore } = await f.vite.ssrLoadModule('/src/stores/session.ts')
  const store = useSessionStore()
  const response = deferred()
  globalThis.fetch = () => response.promise
  const opening = store.open('session-a', 'instance-a')
  await new Promise(resolve => setImmediate(resolve))
  await f.login('b')
  response.resolve(new Response('{"messages":[]}'))
  await opening
  assert.equal(sources.length, 0)
  globalThis.fetch = async () => new Response('{"messages":[]}')
  await store.open('session-b', 'instance-b')
  const old = sources[0]
  const delta = old.listeners.get('session.next.text.delta')
  delta({ data: '{"delta":"B private"}' })
  assert.equal(store.messages.length, 1)
  await f.login('c')
  delta({ data: '{"delta":"late B private"}' })
  assert.equal(old.readyState, 2)
  assert.deepEqual(store.messages, [])
  await store.open('session-b', 'instance-b')
  assert.match(sources[1].url, /token=token-c/)
  store.close()
})))

test('gateway live streams cannot reconnect or emit under another session', () => fixture(f => withEventSource(f, async sources => {
  const { GatewayLiveClient } = await f.vite.ssrLoadModule('/src/api/gateway-live.ts')
  const owner = f.captureAuthSession(f.auth)
  const seen = []
  const client = new GatewayLiveClient(1, () => f.auth.token, { onEnvelope: value => seen.push(value) }, () => f.isCurrentAuthSession(f.auth, owner))
  client.open()
  const old = sources[0]
  old.onmessage({ data: '{"type":"request"}' })
  await f.login('b')
  old.onmessage({ data: '{"type":"request"}' })
  client.open()
  assert.equal(sources.length, 1)
  assert.equal(seen.length, 1)
  assert.equal(old.readyState, 2)
  client.close()
})))

test('OpenCode realtime WebSocket is closed and old callbacks cannot update the next session', () => fixture(async f => {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'WebSocket')
  const sockets = []
  class Socket { static OPEN = 1; static CONNECTING = 0; readyState = 1; constructor(url) { this.url = url; sockets.push(this) } close() { this.readyState = 3; this.onclose?.() } }
  Object.defineProperty(globalThis, 'WebSocket', { configurable: true, value: Socket })
  Object.assign(f.browser, { setTimeout, clearTimeout })
  let store
  try {
    const { useOpenCodeStore } = await f.vite.ssrLoadModule('/src/stores/opencode.ts')
    store = useOpenCodeStore()
    store.subscribeToRealTimeUpdates()
    const old = sockets[0], callback = old.onmessage
    callback({ data: '{"type":"session_updated","sessionId":"a","status":"running"}' })
    assert.equal(store.realTimeStatus.a, 'running')
    const gate = deferred(); f.localDB.pending = gate.promise
    const change = f.login('b')
    callback({ data: '{"type":"session_updated","sessionId":"a","status":"completed"}' })
    assert.equal(old.readyState, 3)
    assert.deepEqual(store.realTimeStatus, {})
    assert.equal(store.subscribeToRealTimeUpdates(), null)
    gate.resolve(); await change
    store.subscribeToRealTimeUpdates()
    assert.match(sockets[1].url, /token=token-b/)
    await f.auth.clearLocal()
  } finally { if (original) Object.defineProperty(globalThis, 'WebSocket', original); else delete globalThis.WebSocket }
}))

test('legacy recovery restores every persisted field without asserting ownership', () => fixture(async f => {
  f.values.delete('pocket_auth_snapshot_v1')
  f.values.set('pocket_token', 'legacy-token')
  f.values.set('pocket_user', 'Legacy user')
  f.values.set('pocket_workspace_id', 'legacy-workspace')
  f.values.set('pocket_auth_method', 'legacy')
  await f.auth.syncFromStorage(true)
  assert.equal(f.auth.token, 'legacy-token')
  assert.equal(f.auth.user, 'Legacy user')
  assert.equal(f.auth.workspaceId, 'legacy-workspace')
  assert.equal(f.auth.authMethod, 'legacy')
  assert.equal(f.auth.userId, '')
  assert.equal(f.localDB.captureOperation({ kind: 'local-edit' }).owner.principal, null)
}))

test('a compatibility-key write failure cannot change the authoritative snapshot into a mixed account', () => fixture(async f => {
  const original = f.storage.setItem
  f.storage.setItem = (key, value) => { if (key === 'pocket_workspace_id') throw new Error('fixture compatibility failure'); original(key, value) }
  await assert.rejects(f.login('b'))
  assert.equal(f.auth.sessionPersistFailed, true)
  assert.equal(f.auth.activationStatus, 'error')
  f.storage.setItem = original
  await f.auth.syncFromStorage(true)
  assert.equal(f.auth.token, 'token-b')
  assert.equal(f.auth.user, 'User b')
  assert.equal(f.auth.workspaceId, 'ws-b')
  assert.equal(f.auth.authMethod, 'password')
}))
