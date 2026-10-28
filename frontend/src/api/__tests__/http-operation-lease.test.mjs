import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createServer } from 'vite'
import { createPinia, setActivePinia } from 'pinia'

const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r }); return { promise, resolve } }
const aborted = error => error?.name === 'AbortError'
const json = (body = {}, status = 200) => Response.json(body, { status })

async function withClient(run) {
  const previousStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const previousFetch = globalThis.fetch
  const values = new Map()
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    get length() { return values.size },
    key: index => [...values.keys()][index] ?? null,
    getItem: key => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, String(value)),
    removeItem: key => values.delete(key),
  } })
  const vite = await createServer({ configFile: false, server: { middlewareMode: true, hmr: false }, optimizeDeps: { noDiscovery: true } })
  setActivePinia(createPinia())
  try {
    const { useAuthStore } = await vite.ssrLoadModule('/src/stores/auth.ts')
    const { localDB } = await vite.ssrLoadModule('/src/native/local-db.ts')
    const { http } = await vite.ssrLoadModule('/src/api/http.ts')
    const auth = useAuthStore()
    await auth.setAuthWithWorkspace('token-a', 'User A', 'user-a', 'workspace-a')
    await localDB.requestPolicyActivation('meetings', 'cloud', () => undefined)
    const operation = localDB.bindOperation(localDB.captureOperation({ kind: 'cloud-sync', module: 'meetings' }))
    const revoke = async () => {
      await localDB.requestPolicyActivation('meetings', 'local', () => undefined)
      await localDB.requestPolicyActivation('meetings', 'cloud', () => undefined)
    }
    await run({ http, auth, operation, revoke, vite, localDB })
  } finally {
    await vite.close()
    globalThis.fetch = previousFetch
    if (previousStorage) Object.defineProperty(globalThis, 'localStorage', previousStorage)
    else delete globalThis.localStorage
  }
}

test('an expired operation is refused before proactive refresh or HTTP dispatch', () => withClient(async ({ http, auth, operation, revoke }) => {
  let requests = 0, refreshes = 0
  globalThis.fetch = async () => { requests++; return json() }
  auth.maybeRefresh = async () => { refreshes++ }
  await revoke()
  await assert.rejects(http('/api/private', { assertCurrent: operation.assertCurrent }), aborted)
  assert.equal(requests, 0)
  assert.equal(refreshes, 0)
}))

test('policy ABA during proactive refresh cannot dispatch an old operation', () => withClient(async ({ http, auth, operation, revoke }) => {
  const entered = deferred(), release = deferred()
  let requests = 0
  auth.maybeRefresh = async () => { entered.resolve(); await release.promise }
  globalThis.fetch = async () => { requests++; return json() }
  const pending = assert.rejects(http('/api/private', { assertCurrent: operation.assertCurrent }), aborted)
  await entered.promise
  await revoke()
  release.resolve()
  await pending
  assert.equal(requests, 0)
}))

for (const status of [200, 204, 401, 503]) {
  test(`expired response (${status}) cannot deliver data, refresh or clear authentication`, () => withClient(async ({ http, auth, operation, revoke }) => {
    const entered = deferred(), response = deferred()
    let requests = 0, refreshes = 0, clears = 0
    auth.refreshSession = async () => { refreshes++; return false }
    auth.clearLocal = async () => { clears++ }
    globalThis.fetch = async () => { requests++; entered.resolve(); return response.promise }
    const pending = assert.rejects(http('/api/private', { assertCurrent: operation.assertCurrent }), aborted)
    await entered.promise
    await revoke()
    response.resolve(status === 204 ? new Response(null, { status }) : json({ content: 'old result' }, status))
    await pending
    assert.deepEqual({ requests, refreshes, clears }, { requests: 1, refreshes: 0, clears: 0 })
    assert.equal(auth.token, 'token-a')
  }))
}

for (const status of [200, 401]) {
  test(`policy expiry during ${status} body parsing is not swallowed`, () => withClient(async ({ http, auth, operation, revoke }) => {
    const entered = deferred(), release = deferred()
    let refreshes = 0
    auth.refreshSession = async () => { refreshes++; return true }
    globalThis.fetch = async () => ({
      ok: status === 200, status, headers: new Headers({ 'content-type': 'application/json' }),
      json: async () => { entered.resolve(); await release.promise; return { content: 'old result' } },
      text: async () => { entered.resolve(); await release.promise; return '{"error":"expired"}' },
    })
    const pending = assert.rejects(http('/api/private', { assertCurrent: operation.assertCurrent }), aborted)
    await entered.promise
    await revoke()
    release.resolve()
    await pending
    assert.equal(refreshes, 0)
  }))
}

for (const renewed of [true, false]) {
  test(`policy expiry during 401 refresh (${renewed}) cannot replay or log out`, () => withClient(async ({ http, auth, operation, revoke }) => {
    const entered = deferred(), release = deferred()
    let requests = 0, clears = 0
    globalThis.fetch = async () => { requests++; return json({ error: 'expired' }, 401) }
    auth.refreshSession = async () => { entered.resolve(); await release.promise; return renewed }
    auth.clearLocal = async () => { clears++ }
    const pending = assert.rejects(http('/api/private', { assertCurrent: operation.assertCurrent }), aborted)
    await entered.promise
    await revoke()
    release.resolve()
    await pending
    assert.equal(requests, 1)
    assert.equal(clears, 0)
  }))
}

test('a current operation preserves refresh replay and its callback is not a fetch option', () => withClient(async ({ http, auth, operation }) => {
  const calls = []
  globalThis.fetch = async (_url, init) => {
    calls.push(init)
    return calls.length === 1 ? json({ error: 'expired' }, 401) : json({ content: 'current result' })
  }
  auth.refreshSession = async () => true
  assert.deepEqual(await http('/api/private', { assertCurrent: operation.assertCurrent }), { content: 'current result' })
  assert.equal(calls.length, 2)
  for (const init of calls) {
    assert.equal(Object.hasOwn(init, 'assertCurrent'), false)
    assert.equal(init.headers.Authorization, 'Bearer token-a')
  }
}))

test('public authentication also respects a supplied initiating operation', () => withClient(async ({ http, operation, revoke }) => {
  let requests = 0
  globalThis.fetch = async () => { requests++; return json() }
  await revoke()
  await assert.rejects(http('/api/auth/login', { assertCurrent: operation.assertCurrent }), aborted)
  assert.equal(requests, 0)
}))

for (const reenterAt of [1, 3, 4]) {
  test(`a synchronous owner switch in assertion ${reenterAt} cannot dispatch`, () => withClient(async ({ http, auth }) => {
    let checks = 0, requests = 0, switched
    globalThis.fetch = async () => { requests++; return json() }
    const pending = http('/api/private', {
      method: 'POST', body: JSON.stringify({ from: 'A' }),
      assertCurrent: () => {
        if (++checks === reenterAt) switched = auth.setAuthWithWorkspace('token-b', 'User B', 'user-b', 'workspace-b')
      },
    })
    await assert.rejects(pending, aborted)
    await switched
    assert.equal(requests, 0)
  }))
}

test('an asynchronous assertion is rejected before dispatch', () => withClient(async ({ http }) => {
  let requests = 0
  globalThis.fetch = async () => { requests++; return json() }
  await assert.rejects(http('/api/private', { assertCurrent: async () => { throw new DOMException('expired', 'AbortError') } }), TypeError)
  assert.equal(requests, 0)
}))

for (const target of [
  { path: '/api/meetings' },
  { path: '/api/llm/chat', syncModule: 'meetings' },
]) {
  test(`a synchronous policy change blocks ${target.path} before dispatch`, () => withClient(async ({ http, vite, localDB }) => {
    const policy = await vite.ssrLoadModule('/src/stores/syncPolicyStore.ts')
    policy.installSyncPolicyStorage(globalThis.localStorage)
    policy.installSyncPolicyActivationHost({
      captureContext: () => ({ scope: JSON.stringify([localStorage.getItem('pocket_user') ?? '', localStorage.getItem('pocket_workspace_id') ?? '']), isCurrent: () => true }),
      activate: (modes, publish) => localDB.requestPolicySnapshotActivation(modes, publish),
    })
    const store = policy.useSyncPolicyStore()
    const snapshot = mode => ({ system_default: 'cloud', allow_override: true, modules: { meetings: mode } })
    assert.equal(await store.applyPolicy(snapshot('cloud'), 1), true)
    assert.equal(policy.readCachedModuleMode('meetings'), 'cloud')
    let checks = 0, requests = 0, changed
    globalThis.fetch = async () => { requests++; return json() }
    const request = http(target.path, {
      method: 'POST', body: '{}', syncModule: target.syncModule,
      assertCurrent: () => { if (++checks === 4) changed = store.applyPolicy(snapshot('local'), 2) },
    })
    await assert.rejects(request, error => error.status === 409)
    assert.ok(checks >= 4)
    assert.ok(changed)
    await changed
    assert.equal(requests, 0)
  }))
}
