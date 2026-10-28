import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createServer } from 'vite'
import { createPinia, setActivePinia } from 'pinia'
import { effectScope } from 'vue'

const json = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r }); return { promise, resolve } }
const isSessionAbort = error => error?.name === 'AbortError'

async function withClient(run, browserWindow) {
  const oldStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const oldFetch = globalThis.fetch
  const oldWindow = Object.getOwnPropertyDescriptor(globalThis, 'window')
  if (browserWindow) Object.defineProperty(globalThis, 'window', { configurable: true, value: browserWindow })
  const values = new Map()
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: key => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, value),
    removeItem: key => values.delete(key),
  } })
  const vite = await createServer({ configFile: false, server: { middlewareMode: true, hmr: false }, optimizeDeps: { noDiscovery: true } })
  setActivePinia(createPinia())
  try {
    const { useAuthStore, captureAuthSession, isCurrentAuthSession } = await vite.ssrLoadModule('/src/stores/auth.ts')
    const { http } = await vite.ssrLoadModule('/src/api/http.ts')
    const { api } = await vite.ssrLoadModule('/src/api/client.ts')
    const auth = useAuthStore()
    const login = (name = 'a', store = auth) => store.setAuthWithWorkspace(`token-${name}`, `User ${name}`, `user-${name}`, `ws-${name}`)
    login()
    await run({ auth, http, api, login, values, useAuthStore, captureAuthSession, isCurrentAuthSession })
  } finally {
    await vite.close()
    globalThis.fetch = oldFetch
    if (browserWindow) {
      if (oldWindow) Object.defineProperty(globalThis, 'window', oldWindow)
      else delete globalThis.window
    }
    if (oldStorage) Object.defineProperty(globalThis, 'localStorage', oldStorage)
    else delete globalThis.localStorage
  }
}

for (const change of ['login', 'roundtrip', 'storage']) {
  test(`late refresh cannot replace a changed session (${change})`, () => withClient(async ({ auth, login, values }) => {
    const started = deferred(), response = deferred()
    globalThis.fetch = async () => { started.resolve(); return response.promise }
    const pending = auth.refreshSession()
    await started.promise
    if (change === 'storage') {
      values.set('pocket_token', 'token-b'); values.set('pocket_user', 'User b'); values.set('pocket_workspace_id', 'ws-b')
      auth.syncFromStorage()
      values.set('pocket_token', 'token-a'); values.set('pocket_user', 'User a'); values.set('pocket_workspace_id', 'ws-a')
      auth.syncFromStorage()
    } else {
      auth.clearLocal(); login('b')
      if (change === 'roundtrip') { auth.clearLocal(); login('a') }
    }
    const expected = { token: auth.token, user: auth.user, userId: auth.userId, workspace: auth.workspaceId }
    response.resolve(json({ token: 'renewed-a', user_id: 'user-a', workspace_id: 'ws-a' }))
    assert.equal(await pending, false)
    assert.deepEqual({ token: auth.token, user: auth.user, userId: auth.userId, workspace: auth.workspaceId }, expected)
    assert.equal(values.get('pocket_token'), expected.token)
  }))
}

for (const change of ['storage', 'direct roundtrip']) {
  test(`session observation survives its first caller unmounting (${change})`, () => withClient(async ({ useAuthStore, values, captureAuthSession, isCurrentAuthSession }) => {
    const auth = useAuthStore(createPinia())
    const started = deferred(), response = deferred()
    globalThis.fetch = async () => { started.resolve(); return response.promise }
    const scope = effectScope()
    const session = scope.run(() => captureAuthSession(auth))
    const pending = scope.run(() => auth.refreshSession())
    await started.promise
    scope.stop()
    if (change === 'storage') {
      values.set('pocket_token', 'token-b'); values.set('pocket_user', 'User b'); values.set('pocket_workspace_id', 'ws-b')
      auth.syncFromStorage()
    } else {
      auth.$patch({ token: 'token-b', user: 'User b', workspaceId: 'ws-b' })
      auth.$patch({ token: 'token-a', user: 'User a', workspaceId: 'ws-a' })
    }
    const expectedToken = auth.token
    response.resolve(json({ token: 'renewed-a', user_id: 'user-a', workspace_id: 'ws-a' }))
    assert.equal(await pending, false)
    assert.equal(auth.token, expectedToken)
    assert.equal(isCurrentAuthSession(auth, session), false)
  }))
}

test('disposing the store cancels its in-flight session work', () => withClient(async ({ auth, captureAuthSession, isCurrentAuthSession }) => {
  const started = deferred(), response = deferred()
  globalThis.fetch = async () => { started.resolve(); return response.promise }
  const session = captureAuthSession(auth)
  const pending = auth.refreshSession()
  await started.promise
  auth.$dispose()
  response.resolve(json({ token: 'renewed-a', user_id: 'user-a', workspace_id: 'ws-a' }))
  assert.equal(await pending, false)
  assert.equal(isCurrentAuthSession(auth, session), false)
  assert.equal(auth.token, 'token-a')
}))

test('refresh single flight belongs to its store and session, and old finally does not clear the new flight', () => withClient(async ({ auth, login, useAuthStore }) => {
  const first = deferred(), second = deferred(), third = deferred()
  const starts = [deferred(), deferred(), deferred()]
  let calls = 0
  globalThis.fetch = async () => { const index = calls++; starts[index].resolve(); return [first, second, third][index].promise }
  const a = auth.refreshSession(); await starts[0].promise
  auth.clearLocal(); login('b')
  const b = auth.refreshSession()
  await Promise.race([starts[1].promise, new Promise((_, reject) => setTimeout(() => reject(new Error('new session joined old refresh')), 300))])
  first.resolve(json({ token: 'renewed-a', user_id: 'user-a', workspace_id: 'ws-a' }))
  assert.equal(await a, false)
  const bAgain = auth.refreshSession()
  setActivePinia(createPinia())
  const other = useAuthStore(); login('c', other)
  const c = other.refreshSession(); await starts[2].promise
  second.resolve(json({ token: 'renewed-b', user_id: 'user-b', workspace_id: 'ws-b' }))
  third.resolve(json({ token: 'renewed-c', user_id: 'user-c', workspace_id: 'ws-c' }))
  assert.equal(await b, true); assert.equal(await bAgain, true); assert.equal(await c, true)
  assert.equal(calls, 3)
  assert.equal(auth.token, 'renewed-b'); assert.equal(other.token, 'renewed-c')
}))

test('late logout does not clear the next login', () => withClient(async ({ auth, login }) => {
  const started = deferred(), response = deferred()
  globalThis.fetch = async () => { started.resolve(); return response.promise }
  const pending = auth.logout(); await started.promise
  login('b')
  response.resolve(json({ ok: true }))
  await pending
  assert.equal(auth.token, 'token-b'); assert.equal(auth.workspaceId, 'ws-b')
}))

for (const status of [200, 401]) {
  test(`old http response (${status}) cannot return data, refresh or replay as the next identity`, () => withClient(async ({ auth, http, login }) => {
    const started = deferred(), response = deferred()
    const calls = []
    globalThis.fetch = async (url, init) => { calls.push({ url, auth: init.headers.Authorization }); started.resolve(); return response.promise }
    const pending = assert.rejects(http('/api/private', { method: 'POST', body: JSON.stringify({ text: 'private fixture' }) }), isSessionAbort)
    await started.promise; login('b')
    response.resolve(json({ content: 'old private fixture' }, status))
    await pending
    assert.equal(calls.length, 1)
    assert.equal(auth.token, 'token-b')
  }))
}

test('request waiting for proactive refresh cannot send as the next identity', () => withClient(async ({ auth, http, login }) => {
  const started = deferred(), release = deferred(); let calls = 0
  auth.maybeRefresh = async () => { started.resolve(); await release.promise }
  globalThis.fetch = async () => { calls++; return json({}) }
  const pending = assert.rejects(http('/api/private', { method: 'POST', body: '{}' }), isSessionAbort)
  await started.promise; login('b'); release.resolve()
  await pending; assert.equal(calls, 0); assert.equal(auth.token, 'token-b')
}))

test('same session refresh still replays once with its new credential', () => withClient(async ({ auth, http }) => {
  const calls = []
  globalThis.fetch = async (url, init) => {
    calls.push({ url, authorization: init.headers.Authorization })
    if (url === '/api/auth/refresh') return json({ token: 'renewed-a', user_id: 'user-a', workspace_id: 'ws-a' })
    return calls.length === 1 ? json({ error: 'expired' }, 401) : json({ ok: true })
  }
  assert.deepEqual(await http('/api/private', { method: 'POST', body: '{}' }), { ok: true })
  assert.equal(calls.length, 3); assert.equal(calls[2].authorization, 'Bearer renewed-a'); assert.equal(auth.token, 'renewed-a')
}))

for (const status of [200, 401]) {
  test(`legacy client old response (${status}) cannot return data or clear the next session`, () => withClient(async ({ auth, api, login }) => {
    const started = deferred(), response = deferred()
    globalThis.fetch = async () => { started.resolve(); return response.promise }
    const pending = assert.rejects(api.getTasks(), isSessionAbort)
    await started.promise; login('b')
    response.resolve(json({ tasks: [{ id: 'old-private' }] }, status))
    await pending; assert.equal(auth.token, 'token-b')
  }))
}

test('refresh refuses an unexpected identity or workspace in a successful response', () => withClient(async ({ auth }) => {
  globalThis.fetch = async () => json({ token: 'unrelated', user_id: 'user-b', workspace_id: 'ws-b' })
  assert.equal(await auth.refreshSession(), false)
  assert.equal(auth.token, 'token-a'); assert.equal(auth.workspaceId, 'ws-a')
}))

for (const kind of ['body', 'network']) {
  test(`legacy client checks the session after delayed ${kind} completion`, () => withClient(async ({ auth, api, login }) => {
    const started = deferred(), release = deferred()
    globalThis.fetch = async () => {
      if (kind === 'network') {
        started.resolve(); await release.promise
        throw new TypeError('fixture network failure')
      }
      const response = json({})
      response.json = async () => { started.resolve(); await release.promise; return { tasks: [{ id: 'private-fixture' }] } }
      return response
    }
    const pending = assert.rejects(api.getTasks(), isSessionAbort)
    await started.promise; login('b'); release.resolve()
    await pending; assert.equal(auth.token, 'token-b')
  }))
}

test('storage account switch clears unpersisted user metadata', () => withClient(async ({ auth, values }) => {
  values.set('pocket_token', 'token-b'); values.set('pocket_user', 'User b'); values.set('pocket_workspace_id', 'ws-b')
  auth.syncFromStorage()
  assert.equal(auth.userId, '')
  assert.equal(auth.token, 'token-b'); assert.equal(auth.workspaceId, 'ws-b')
}))

test('new legacy login does not inherit the prior workspace or server user id', () => withClient(async ({ auth, values }) => {
  auth.setAuth('token-b', 'User b')
  assert.equal(auth.userId, ''); assert.equal(auth.workspaceId, '')
  assert.equal(values.get('pocket_workspace_id'), '')
}))

test('a current irrecoverable 401 still clears its own session', () => withClient(async ({ auth, http }) => {
  globalThis.fetch = async () => json({ error: 'expired' }, 401)
  await assert.rejects(http('/api/private'), error => error.status === 401)
  assert.equal(auth.token, ''); assert.equal(auth.workspaceId, '')
}))

test('refresh and logout use their owning store even while another Pinia is active', () => withClient(async ({ auth, login, useAuthStore }) => {
  setActivePinia(createPinia())
  const other = useAuthStore(); login('b', other)
  const credentials = []
  globalThis.fetch = async (url, init) => {
    credentials.push(init.headers.Authorization)
    assert.equal('authSession' in init, false)
    return json(url === '/api/auth/refresh' ? { token: 'renewed-a', user_id: 'user-a', workspace_id: 'ws-a' } : { ok: true })
  }
  assert.equal(await auth.refreshSession(), true)
  await auth.logout()
  assert.deepEqual(credentials, ['Bearer token-a', 'Bearer renewed-a'])
  assert.equal(auth.token, ''); assert.equal(other.token, 'token-b')
}))

test('an awaiting request keeps its originating store when the active Pinia changes', () => withClient(async ({ auth, http, login, useAuthStore }) => {
  const started = deferred(), release = deferred()
  auth.maybeRefresh = async () => { started.resolve(); await release.promise }
  let authorization
  globalThis.fetch = async (_url, init) => { authorization = init.headers.Authorization; return json({ ok: true }) }
  const pending = http('/api/private')
  await started.promise
  setActivePinia(createPinia()); const other = useAuthStore(); login('b', other)
  release.resolve()
  assert.deepEqual(await pending, { ok: true })
  assert.equal(authorization, 'Bearer token-a'); assert.equal(other.token, 'token-b')
}))

test('queued storage events invalidate a session even when the final values match again', () => {
  const browserWindow = new EventTarget()
  browserWindow.location = { origin: 'http://localhost', protocol: 'http:', hostname: 'localhost' }
  return withClient(async ({ auth }) => {
    const started = deferred(), response = deferred()
    globalThis.fetch = async () => { started.resolve(); return response.promise }
    const pending = auth.refreshSession(); await started.promise
    const event = new Event('storage'); Object.defineProperty(event, 'key', { value: 'pocket_token' })
    browserWindow.dispatchEvent(event)
    response.resolve(json({ token: 'renewed-a', user_id: 'user-a', workspace_id: 'ws-a' }))
    assert.equal(await pending, false); assert.equal(auth.token, 'token-a')
  }, browserWindow)
})
