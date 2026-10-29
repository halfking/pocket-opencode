import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import { createServer } from 'vite'
import initSqlJs from 'sql.js'
import { createPinia, setActivePinia } from 'pinia'

const SQL = await initSqlJs()

async function fixture(run) {
  const previousStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const previousFetch = globalThis.fetch
  const values = new Map(), stored = new Map(), pending = []
  const storage = { keys: () => values.keys(), getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, String(value)), removeItem: key => values.delete(key) }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
  setActivePinia(createPinia())
  let afterQuery = () => {}, afterRun = () => {}, beforeRun = () => {}, db, raw, driver, UtilsSQLite, originalDriverRun
  const track = promise => { pending.push(promise); promise.catch(() => {}); return promise }
  const settled = async () => {
    while (pending.length) {
      const results = await Promise.allSettled(pending.splice(0))
      for (const result of results) if (result.status === 'rejected' && result.reason?.name !== 'AbortError') throw result.reason
    }
  }
  const key = `__configSQLite_${Math.random().toString(36).slice(2)}`
  const requests = []
  let respond = () => ({})
  globalThis.fetch = async (url, init) => {
    requests.push({ url: String(url), method: init.method || 'GET', authorization: init.headers.Authorization, body: init.body })
    const response = await respond(String(url), init)
    return response instanceof Response ? response : Response.json(response)
  }
  const vite = await createServer({ root: fileURLToPath(new URL('../../../../', import.meta.url)), configFile: false,
    server: { middlewareMode: true, hmr: false }, ssr: { noExternal: ['@capacitor-community/sqlite'] }, optimizeDeps: { noDiscovery: true }, plugins: [{
      name: 'config-real-sqlite-platform', enforce: 'pre',
      resolveId(source, importer) {
        if (source === '@capacitor-community/sqlite' && importer?.endsWith('/local-db.ts')) return '\0config-sqlite'
        if (importer?.endsWith('/local-db.ts') && source === './sqlite-web-init') return '\0config-web'
        if (importer?.endsWith('/local-db.ts') && source === './runtime-platform') return '\0config-platform'
        if (source.endsWith('/native/lobster-init')) return '\0config-ready'
        if (importer?.endsWith('/config-sync/apply.ts') && source === '../../i18n') return '\0config-i18n'
        if (importer?.endsWith('/config-sync/apply.ts') && source === '../../stores/locale') return '\0config-locale'
      },
      load(id) {
        if (id === '\0config-sqlite') return `export const { SQLiteConnection, SQLiteDBConnection, CapacitorSQLite } = globalThis[${JSON.stringify(key)}]`
        if (id === '\0config-web') return 'export async function initSqliteWeb() {}'
        if (id === '\0config-platform') return 'export const isWebFallbackRuntime = () => true'
        if (id === '\0config-ready') return "import {ref} from 'vue'; export const lobsterReady = ref(true)"
        if (id === '\0config-i18n') return `export const matchLocale = values => values[0]; export const applyLocale = locale => globalThis[${JSON.stringify(key)}].localeEvents.push(locale)`
        if (id === '\0config-locale') return `export const useLocaleStore = () => ({setLocale: locale => globalThis[${JSON.stringify(key)}].localeEvents.push(locale)})`
      },
    }] })
  try {
    const { Database } = await vite.ssrLoadModule('/node_modules/jeep-sqlite/dist/collection/utils/database.js')
    ;({ UtilsSQLite } = await vite.ssrLoadModule('/node_modules/jeep-sqlite/dist/collection/utils/utils-sqlite.js'))
    originalDriverRun = UtilsSQLite.run
    const definitions = await vite.ssrLoadModule('/node_modules/@capacitor-community/sqlite/dist/esm/definitions.js')
    const { registerPlugin } = await vite.ssrLoadModule('/node_modules/@capacitor/core/dist/index.js')
    raw = new SQL.Database()
    const store = { getItem: async key => stored.get(key), setItem: async (key, value) => stored.set(key, value), removeItem: async key => stored.delete(key) }
    driver = new Database('lobster', 1, {}, store, false, '')
    driver.mDb = raw
    const platform = {
      initWebStore: async () => {}, checkConnectionsConsistency: async () => ({ result: true }), createConnection: async () => {},
      open: async () => { driver._isDBOpen = true }, close: async () => driver.close(), closeConnection: async () => {},
      saveToStore: async () => driver.saveToStore(), loadExtension: async () => {},
      execute: async o => ({ changes: { changes: await driver.executeSQL(o.statements, o.transaction) } }),
      executeSet: async o => ({ changes: await driver.execSet(o.set, o.transaction, o.returnMode) }),
      run: async o => { await beforeRun(o.statement); const changes = await driver.runSQL(o.statement, o.values, o.transaction, o.returnMode); await afterRun(o.statement); return { changes } },
      query: async o => { const rows = await driver.selectSQL(o.statement, o.values); await afterQuery(o.statement); return { values: rows } },
      beginTransaction: async () => ({ changes: { changes: await driver.beginTransaction() } }),
      commitTransaction: async () => ({ changes: { changes: await driver.commitTransaction() } }),
      rollbackTransaction: async () => ({ changes: { changes: await driver.rollbackTransaction() } }),
      isTransactionActive: async () => ({ result: driver.isTransActive() }),
    }
    globalThis[key] = { ...definitions, localeEvents: [], CapacitorSQLite: registerPlugin(key, { web: async () => platform }) }
    ;({ localDB: db } = await vite.ssrLoadModule('/src/native/local-db.ts'))
    await db.init('')
    const policy = await vite.ssrLoadModule('/src/stores/syncPolicyStore.ts')
    policy.installSyncPolicyStorage(storage)
    const policyStore = policy.useSyncPolicyStore()
    const { useAuthStore, captureAuthSession, isCurrentAuthSession } = await vite.ssrLoadModule('/src/stores/auth.ts')
    const auth = useAuthStore()
    await auth.setAuthWithWorkspace('fixture-token', 'Fixture', 'fixture-user', 'fixture-workspace')
    const scope = () => JSON.stringify([auth.user, auth.workspaceId])
    policy.installSyncPolicyScopeResolver(scope)
    policy.installSyncPolicyActivationHost({
      captureContext() {
        if (auth.activationStatus !== 'ready') return null
        const session = captureAuthSession(auth), version = auth.activationVersion, captured = scope()
        return { scope: captured, intentScope: auth.userId ? JSON.stringify([auth.userId, auth.workspaceId]) : '', workspace: auth.workspaceId,
          isCurrent: () => scope() === captured && auth.activationVersion === version && isCurrentAuthSession(auth, session) }
      },
      activate: (modes, publish) => db.requestPolicySnapshotActivation(modes, publish),
    })
    const setMode = mode => track(policyStore.applyPolicy({ system_default: 'cloud', allow_override: true, modules: { 'ai-chat': mode, tasks: mode, email: mode } }, Date.now(), false))
    await setMode('cloud')
    const runtime = await vite.ssrLoadModule('/src/native/config-sync/runtime.ts')
    const settings = await vite.ssrLoadModule('/src/native/config-sync/settings-store.ts')
    const outbox = await vite.ssrLoadModule('/src/native/config-sync/outbox.ts')
    const operation = await vite.ssrLoadModule('/src/native/config-sync/operation.ts')
    const apply = await vite.ssrLoadModule('/src/native/config-sync/apply.ts')
    // 账号切换助手：模拟一次真实登录（带已验证 userId/workspaceId）。
    const login = async (user) => {
      const id = user === 'A' ? 'fixture-user' : 'fixture-' + user
      const ws = user === 'A' ? 'fixture-workspace' : 'workspace-' + user
      await auth.setAuthWithWorkspace('fixture-' + user, user, id, ws)
      await setMode('cloud')
    }
    // 夹具的默认身份就是 fixture-user / fixture-workspace（A）。
    const ownerOf = (user) => (user === 'A'
      ? { userId: 'fixture-user', workspaceId: 'fixture-workspace' }
      : { userId: 'fixture-' + user, workspaceId: 'workspace-' + user })
    // 以某个账号的身份入队：走真实的 operation 捕获路径，归属由租约给出。
    const enqueueAs = async (user, input) => {
      if (user !== 'anon') await login(user)
      const op = operation.captureConfigOperation()
      await outbox.enqueueConfigPush(input, op.owner, op.db)
    }
    await run({ runtime, settings, outbox, db, auth, requests, setMode, track, settled, vite, driver, sqlite: raw, UtilsSQLite,
      operation, apply, values, localeEvents: globalThis[key].localeEvents, login, ownerOf, enqueueAs,
      setResponse: fn => { respond = fn }, onQuery: fn => { afterQuery = fn }, onRun: fn => { afterRun = fn }, onBeforeRun: fn => { beforeRun = fn } })
    await settled()
  } finally {
    afterQuery = afterRun = beforeRun = () => {}
    if (UtilsSQLite) UtilsSQLite.run = originalDriverRun
    await settled().catch(() => {})
    if (db) await db.close().catch(() => {})
    try { raw?.close() } catch { /* driver may already have closed it */ }
    await vite.close()
    globalThis.fetch = previousFetch
    delete globalThis[key]
    if (previousStorage) Object.defineProperty(globalThis, 'localStorage', previousStorage)
    else delete globalThis.localStorage
  }
}


function gate() {
  let release, entered
  const wait = new Promise(resolve => { release = resolve })
  const started = new Promise(resolve => { entered = resolve })
  return { wait, started, release, entered }
}

test('cancelled scheduled update never creates old content under a new account', () => fixture(async f => {
  await f.outbox.enqueueConfigPush({ namespace: 'scheduled_task', id: 'task-A', payload: { name: 'A-private-task', kind: 'prompt', scheduleKind: 'once' }, updatedAt: 100 })
  const pause = gate()
  f.setResponse(async (_url, init) => { if (!init.method || init.method === 'GET') { pause.entered(); await pause.wait } return {} })
  const pending = f.runtime.drainConfigOutbox()
  await Promise.race([pause.started, pending.then(n => { throw new Error(`drain ended before GET: ${n}; ${JSON.stringify(f.requests)}; ${JSON.stringify([])}`) })])
  await f.auth.setAuthWithWorkspace('token-b', 'B', 'user-b', 'ws-b')
  pause.release()
  await pending.catch(() => {})
  assert.equal(f.requests.filter(r => r.method === 'POST').length, 0)
  assert.equal((await f.outbox.listQueuedConfigPushes(f.ownerOf('A')))[0]?.state, 'queued')
}))

test('queued config read retains its original owner across the driver await', () => fixture(async f => {
  await f.outbox.enqueueConfigPush({ namespace: 'app_prefs', id: 'default', payload: { theme: 'dark' }, updatedAt: 100 })
  const pause = gate()
  f.onQuery(async sql => { if (sql.includes('local_config_outbox')) { pause.entered(); await pause.wait } })
  const pending = f.runtime.drainConfigOutbox()
  await pause.started
  const changed = f.auth.setAuthWithWorkspace('token-b', 'B', 'user-b', 'ws-b')
  pause.release()
  await assert.rejects(pending, { name: 'AbortError' })
  await changed
  assert.equal(f.requests.length, 0)
  assert.equal((await f.outbox.listQueuedConfigPushes(f.ownerOf('A')))[0]?.attempts, 0)
}))

for (const namespace of ['scheduled_task', 'chat_agent']) test(`${namespace} cancellation never falls back to create or marks failure`, () => fixture(async f => {
  await f.outbox.enqueueConfigPush({ namespace, id: 'old-A', payload: { name: 'A-private' }, updatedAt: 100 })
  const pause = gate()
  f.setResponse(async () => { pause.entered(); await pause.wait; return {} })
  const pending = f.runtime.drainConfigOutbox()
  await pause.started
  await f.auth.setAuthWithWorkspace('token-b', 'B', 'user-b', 'ws-b')
  await f.auth.setAuthWithWorkspace('fixture-token', 'Fixture', 'fixture-user', 'fixture-workspace')
  pause.release()
  await assert.rejects(pending, { name: 'AbortError' })
  assert.equal(f.requests.length, 1)
  assert.equal((await f.outbox.listQueuedConfigPushes(f.ownerOf('A')))[0]?.attempts, 0)
}))

test('only a confirmed 404 creates a missing task; transport failure stays queued', () => fixture(async f => {
  await f.outbox.enqueueConfigPush({ namespace: 'scheduled_task', id: 'missing', payload: { name: 'new task', kind: 'prompt', scheduleKind: 'once' }, updatedAt: 100 })
  f.setResponse((_url, init) => !init.method || init.method === 'GET' ? Response.json({ error: 'not_found' }, { status: 404 }) : {})
  assert.equal(await f.runtime.drainConfigOutbox(), 1)
  assert.equal(f.requests.filter(r => r.method === 'POST').length, 1)
  await f.outbox.enqueueConfigPush({ namespace: 'scheduled_task', id: 'unavailable', payload: { name: 'existing task' }, updatedAt: 101 })
  f.setResponse(() => Response.json({ error: 'unavailable' }, { status: 503 }))
  assert.equal(await f.runtime.drainConfigOutbox(), 0)
  assert.equal(f.requests.filter(r => r.method === 'POST').length, 1)
  assert.equal((await f.outbox.listQueuedConfigPushes())[0]?.attempts, 1)
}))

test('email LWW conflict completes the old queue item with its original base version', () => fixture(async f => {
  await f.outbox.enqueueConfigPush({ namespace: 'email_account', id: 'account-A', payload: { displayName: 'Old name' }, updatedAt: 100 })
  f.setResponse(() => Response.json({ error: 'stale write: server copy is newer', updatedAt: 200 }, { status: 409 }))
  assert.equal(await f.runtime.drainConfigOutbox(), 1)
  assert.equal((await f.outbox.listQueuedConfigPushes()).length, 0)
  assert.equal(JSON.parse(f.requests[0].body).updatedAt, 100)
}))

test('module-policy rejection never completes or retries a queued config item', () => fixture(async f => {
  await f.outbox.enqueueConfigPush({ namespace: 'email_account', id: 'account-A', payload: { displayName: 'Old name' }, updatedAt: 100 })
  f.setResponse(() => Response.json({ error: 'module_policy_local', module: 'email' }, { status: 409 }))
  await assert.rejects(f.runtime.drainConfigOutbox(), error => error.body?.error === 'module_policy_local')
  assert.equal((await f.outbox.listQueuedConfigPushes())[0].attempts, 0)
}))

test('successful upload cannot clear a same-second newer local edit', () => fixture(async f => {
  await f.settings.writeLocalSetting({ namespace: 'app_prefs', id: 'default', payload: { theme: 'dark' }, updatedAt: 100 })
  await f.outbox.enqueueConfigPush({ namespace: 'app_prefs', id: 'default', payload: { theme: 'dark' }, updatedAt: 100 })
  f.setResponse(async () => {
    await f.settings.writeLocalSetting({ namespace: 'app_prefs', id: 'default', payload: { theme: 'light' }, updatedAt: 100 })
    return { applied: true, conflict: false }
  })
  assert.equal(await f.runtime.drainConfigOutbox(), 1)
  const current = await f.settings.getLocalSetting('app_prefs', 'default')
  assert.deepEqual(current.payload, { theme: 'light' })
  assert.equal(current.dirty, 1)
}))

test('atomic remote LWW write does not overwrite a newer value present at SQL dispatch', () => fixture(async f => {
  await f.settings.writeLocalSetting({ namespace: 'app_prefs', id: 'default', payload: { theme: 'dark' }, updatedAt: 100 })
  f.onBeforeRun(sql => {
    if (sql.includes('INSERT INTO local_user_settings')) {
      f.sqlite.run("UPDATE local_user_settings SET payload = '{\"theme\":\"light\"}', updated_at = 300, dirty = 1")
    }
  })
  assert.equal(await f.settings.writeLocalIfNewer({ namespace: 'app_prefs', id: 'default', payload: { theme: 'system' }, updatedAt: 200 }), false)
  const current = await f.settings.getLocalSetting('app_prefs', 'default')
  assert.equal(current.updatedAt, 300)
  assert.deepEqual(current.payload, { theme: 'light' })
  assert.equal(current.dirty, 1)
}))

test('remote pull does not project an older conflict over a newer local edit', () => fixture(async f => {
  await f.settings.writeLocalSetting({ namespace: 'chat_settings', id: 'default', payload: { temperature: 1 }, updatedAt: 100 })
  await f.outbox.enqueueConfigPush({ namespace: 'chat_settings', id: 'default', payload: { temperature: 1 }, updatedAt: 100 })
  f.setResponse(async () => {
    await f.settings.writeLocalSetting({ namespace: 'chat_settings', id: 'default', payload: { temperature: 2 }, updatedAt: 300 })
    return { conflict: true, record: { namespace: 'chat_settings', id: 'default', workspaceId: 'fixture-workspace', payload: { temperature: 0 }, updatedAt: 200 } }
  })
  await f.runtime.drainConfigOutbox()
  assert.equal(f.values.has('pocket:ai-chat:settings:v2:fixture-workspace'), false)
  assert.equal((await f.settings.getLocalSetting('chat_settings', 'default')).dirty, 1)
}))

test('local-only edits remain local and queued without cloud authorization', () => fixture(async f => {
  await f.setMode('local')
  await f.runtime.saveSettingLocalFirst('chat_settings', 'default', { temperature: 1 })
  assert.equal(f.requests.length, 0)
  assert.equal((await f.outbox.listQueuedConfigPushes()).length, 1)
  assert.equal(f.values.get('pocket:ai-chat:settings:v2:fixture-workspace'), '{"temperature":1}')
}))

test('old setting projection cannot choose the next workspace', () => fixture(async f => {
  const operation = f.operation.captureConfigOperation().forNamespace('chat_settings')
  await f.auth.setAuthWithWorkspace('token-b', 'B', 'user-b', 'ws-b')
  assert.throws(() => f.apply.applyRemoteSetting({ namespace: 'chat_settings', id: 'default', payload: { systemPrompt: 'A-private' } }, operation), { name: 'AbortError' })
  assert.equal(f.values.has('pocket:ai-chat:settings:v2:ws-b'), false)
}))

test('a local-first edit cancelled inside SQL neither projects nor queues for the next owner', () => fixture(async f => {
  f.onRun(sql => {
    if (sql.includes('INSERT INTO local_user_settings')) {
      f.onRun(() => {})
      f.track(f.auth.setAuthWithWorkspace('token-b', 'B', 'user-b', 'ws-b'))
    }
  })
  await assert.rejects(f.runtime.saveSettingLocalFirst('chat_settings', 'default', { systemPrompt: 'A-private' }), { name: 'AbortError' })
  await f.settled()
  assert.equal(await f.settings.getLocalSetting('chat_settings', 'default'), null)
  assert.equal(f.values.has('pocket:ai-chat:settings:v2:ws-b'), false)
  assert.equal((await f.outbox.listQueuedConfigPushes()).length, 0)
  assert.equal(f.requests.length, 0)
}))

test('revoking cloud inside a clean write rolls it back and retains the queue', () => fixture(async f => {
  await f.settings.writeLocalSetting({ namespace: 'chat_settings', id: 'default', payload: { temperature: 1 }, updatedAt: 100 })
  await f.outbox.enqueueConfigPush({ namespace: 'chat_settings', id: 'default', payload: { temperature: 1 }, updatedAt: 100 })
  f.setResponse(() => ({ applied: true, conflict: false }))
  f.onRun(sql => {
    if (sql.startsWith('UPDATE local_user_settings SET dirty')) {
      f.onRun(() => {})
      f.setMode('local')
    }
  })
  await assert.rejects(f.runtime.drainConfigOutbox(), { name: 'AbortError' })
  await f.settled()
  assert.equal((await f.settings.getLocalSetting('chat_settings', 'default')).dirty, 1)
  assert.equal((await f.outbox.listQueuedConfigPushes())[0].attempts, 0)
}))

test('restored sessions with unknown principal retain opaque operation protection', () => fixture(async f => {
  // 恢复会话：服务端**确认不出**身份（离线 / 401）⇒ 归属保持未知。
  f.setResponse(() => Response.json({ error: 'unauthorized' }, { status: 401 }))
  await f.auth.syncFromStorage(true)
  await f.auth.whenOwnerReady()
  assert.equal(f.auth.userId, '', '服务端确认失败时不得凭空得到 userId')
  await f.setMode('cloud')
  await f.outbox.enqueueConfigPush({ namespace: 'chat_settings', id: 'default', payload: { temperature: 1 }, updatedAt: 100 })
  f.setResponse(() => ({ applied: true, conflict: false }))
  // 归属未知 ⇒ 这一行**不上传**（未认领 ≠ 属于当前账号）。
  assert.equal(await f.runtime.drainConfigOutbox(), 0)
  assert.equal(f.requests.filter(r => r.method === 'PUT').length, 0)
}))

test('a restored session re-earns its owner from the server attestation', () => fixture(async f => {
  // 服务端确认过的身份（/api/auth/identity 从已验证 JWT 推导）。
  f.setResponse((url) => {
    if (String(url).includes('/api/auth/identity')) {
      return { user_id: 'fixture-user', workspace_id: 'fixture-workspace' }
    }
    return {}
  })
  await f.auth.syncFromStorage(true)
  await f.auth.whenOwnerReady()
  assert.equal(f.auth.userId, 'fixture-user', JSON.stringify({ userId: f.auth.userId, ws: f.auth.workspaceId,
    reqs: f.requests.map(r => r.url), snap: f.values.get('pocket_auth_snapshot_v1'),
    token: f.auth.token, activation: f.auth.activationStatus }))
  assert.equal(f.auth.workspaceId, 'fixture-workspace')

  await f.setMode('cloud')
  await f.outbox.enqueueConfigPush({ namespace: 'chat_settings', id: 'default', payload: { temperature: 1 }, updatedAt: 100 })
  const queued = await f.outbox.listQueuedConfigPushes(f.ownerOf('A'))
  assert.equal(queued.length, 1, '恢复后的会话应当重新拥有归属')
  assert.equal(queued[0].owner?.userId, 'fixture-user')

  f.setResponse(() => ({ applied: true, conflict: false }))
  assert.equal(await f.runtime.drainConfigOutbox(), 1)
  assert.equal(f.requests.filter(r => r.method === 'PUT').length, 1)
}))

test('an unversioned remote response cannot clean a newer local secret or payload', () => fixture(async f => {
  const original = await f.settings.writeLocalSetting({ namespace: 'app_prefs', id: 'default', payload: { theme: 'dark' }, secretEncrypted: 'old', updatedAt: 100 })
  await f.settings.writeLocalSetting({ namespace: 'app_prefs', id: 'default', payload: { theme: 'dark' }, secretEncrypted: 'new', updatedAt: 100 })
  await f.settings.markSettingClean('app_prefs', 'default', original)
  assert.equal((await f.settings.getLocalSetting('app_prefs', 'default')).dirty, 1)
}))

test('normal remote pull stores and projects the confirmed original workspace record', () => fixture(async f => {
  f.setResponse(() => ({ settings: [{ namespace: 'app_prefs', id: 'default', workspaceId: 'fixture-workspace', payload: { theme: 'dark' }, updatedAt: 100 }] }))
  const report = await f.runtime.syncUserSettings()
  assert.equal(report.pulled, 1)
  assert.equal(f.values.get('app_theme'), 'dark')
  assert.equal((await f.settings.getLocalSetting('app_prefs', 'default')).dirty, 0)
}))

test('remote pull cannot commit or project after policy revocation inside SQL', () => fixture(async f => {
  f.setResponse(() => ({ settings: [{ namespace: 'chat_settings', id: 'default', workspaceId: 'fixture-workspace', payload: { systemPrompt: 'old-cloud' }, updatedAt: 100 }] }))
  f.onRun(sql => { if (sql.includes('INSERT INTO local_user_settings')) { f.onRun(() => {}); f.setMode('local') } })
  await assert.rejects(f.runtime.syncUserSettings(), { name: 'AbortError' })
  await f.settled()
  assert.equal(await f.settings.getLocalSetting('chat_settings', 'default'), null)
  assert.equal(f.values.has('pocket:ai-chat:settings:v2:fixture-workspace'), false)
}))

test('remote values for a local module leave the local data unchanged', () => fixture(async f => {
  await f.setMode('local')
  await f.settings.writeLocalSetting({ namespace: 'chat_settings', id: 'default', payload: { systemPrompt: 'local' }, updatedAt: 50 })
  f.setResponse(() => ({ settings: [{ namespace: 'chat_settings', id: 'default', workspaceId: 'fixture-workspace', payload: { systemPrompt: 'remote' }, updatedAt: 100 }] }))
  assert.equal((await f.runtime.syncUserSettings()).pulled, 0)
  assert.deepEqual((await f.settings.getLocalSetting('chat_settings', 'default')).payload, { systemPrompt: 'local' })
}))

test('deferred locale projection is cancelled instead of changing the next session', () => fixture(async f => {
  await f.vite.ssrLoadModule('\0config-i18n')
  await f.vite.ssrLoadModule('\0config-locale')
  const operation = f.operation.captureConfigOperation()
  f.apply.applyRemoteSetting({ namespace: 'app_prefs', id: 'default', payload: { locale: 'de-DE' } }, operation)
  await f.auth.setAuthWithWorkspace('token-b', 'B', 'user-b', 'ws-b')
  await new Promise(resolve => setImmediate(resolve))
  await new Promise(resolve => setImmediate(resolve))
  assert.deepEqual(f.localeEvents, [])
}))

test('policy ABA while a task GET waits cannot send the later PATCH', () => fixture(async f => {
  await f.outbox.enqueueConfigPush({ namespace: 'scheduled_task', id: 'task-A', payload: { name: 'A-private', kind: 'prompt', scheduleKind: 'once' }, updatedAt: 100 })
  const pause = gate()
  f.setResponse(async (_url, init) => { if (!init.method || init.method === 'GET') { pause.entered(); await pause.wait } return {} })
  const pending = f.runtime.drainConfigOutbox()
  await pause.started
  await f.setMode('local')
  await f.setMode('cloud')
  pause.release()
  await assert.rejects(pending, { name: 'AbortError' })
  assert.equal(f.requests.length, 1)
  assert.equal((await f.outbox.listQueuedConfigPushes())[0]?.attempts, 0)
}))

test('policy ABA during proactive refresh cannot send the first request', () => fixture(async f => {
  await f.outbox.enqueueConfigPush({ namespace: 'chat_settings', id: 'default', payload: { systemPrompt: 'A-private' }, updatedAt: 100 })
  const pause = gate()
  f.auth.maybeRefresh = async () => { pause.entered(); await pause.wait }
  const pending = f.runtime.drainConfigOutbox()
  await pause.started
  await f.setMode('local')
  await f.setMode('cloud')
  pause.release()
  await assert.rejects(pending, { name: 'AbortError' })
  assert.equal(f.requests.length, 0)
}))

// ---------------------------------------------------------------------------
// 账号归属（2026-10-29）
//
// ⚠ 这组用例针对的是一个**真实的跨账号数据泄漏**：
//   local_config_outbox / local_user_settings 原本没有账号列，而切账号**不清理**
//   共享的 SQLite 库 ⇒ A 账号离线排队的一条 chat_settings，会在 B 账号登录后
//   被 drain 用 **B 的凭据**推上云端（A 的私密内容进了 B 的账号）。
//   已在隔离浏览器里实测复现：PUT 请求体是 A 的 prompt，Authorization 是 B 的令牌。
//
// 这组用例要求：
//   · 新写入的行带当前已验证会话的归属；
//   · 只列出/上传**属于当前归属**的行；
//   · 归属未知的行（历史行）既不上传，也不被任何账号认领。
// ---------------------------------------------------------------------------

test('a queued row stays with its own account and is never pushed by the next one', () => fixture(async f => {
  // A 离线保存 ⇒ 只入队，不上行。
  await f.enqueueAs('A', { namespace: 'chat_settings', id: 'default', payload: { systemPrompt: 'A-private-prompt' }, updatedAt: 100 })
  const queuedForA = await f.outbox.listQueuedConfigPushes(f.ownerOf('A'))
  assert.equal(queuedForA.length, 1, 'A 的队列应当只有一条')
  assert.equal(queuedForA[0].owner?.userId, 'fixture-user')

  // 切到 B：B 的队列**看不到** A 的行。
  await f.login('B')
  const queuedForB = await f.outbox.listQueuedConfigPushes(f.ownerOf('B'))
  assert.equal(queuedForB.length, 0, 'B 不该看到 A 的队列行')

  // B drain ⇒ 不得发出任何请求。
  f.setResponse(() => ({ applied: true, conflict: false }))
  assert.equal(await f.runtime.drainConfigOutbox(), 0)
  assert.equal(f.requests.length, 0, 'B 不得用 B 的凭据上传 A 的内容')

  // 切回 A ⇒ 行仍在，且归属未变。
  await f.login('A')
  const back = await f.outbox.listQueuedConfigPushes(f.ownerOf('A'))
  assert.equal(back.length, 1)
  assert.deepEqual(back[0].payload, { systemPrompt: 'A-private-prompt' })
}))

test('the same namespace and id in two accounts do not overwrite each other', () => fixture(async f => {
  // 写的是**设置镜像表**（local_user_settings）：主键含归属 ⇒ 两个账号的
  // 同一 namespace+id 各自成行，不互相覆盖。
  await f.login('A')
  await f.settings.writeLocalSetting({ namespace: 'scheduled_task', id: 'task-1', payload: { name: 'A-task' }, dirty: 1, updatedAt: 100 }, f.ownerOf('A'))
  await f.login('B')
  await f.settings.writeLocalSetting({ namespace: 'scheduled_task', id: 'task-1', payload: { name: 'B-task' }, dirty: 1, updatedAt: 100 }, f.ownerOf('B'))

  await f.login('A')
  const aRows = await f.settings.listLocalSettings(f.ownerOf('A'))
  assert.equal(aRows.filter(r => r.id === 'task-1').length, 1)
  assert.deepEqual(aRows.find(r => r.id === 'task-1')?.payload, { name: 'A-task' })

  await f.login('B')
  const bRows = await f.settings.listLocalSettings(f.ownerOf('B'))
  assert.equal(bRows.filter(r => r.id === 'task-1').length, 1)
  assert.deepEqual(bRows.find(r => r.id === 'task-1')?.payload, { name: 'B-task' })
}))

test('rows without a verified owner stay unclaimed and are never uploaded', () => fixture(async f => {
  // 直接写入**无归属**的行（模拟加列之前的历史数据）。
  await f.db.run(
    `INSERT INTO local_user_settings (namespace, id, payload, secret_encrypted, updated_at, dirty, owner_user_id, owner_workspace_id)
     VALUES ('chat_settings', 'legacy', '{"systemPrompt":"legacy-unknown"}', '', 100, 1, '', '')`,
  )
  await f.db.run(
    `INSERT INTO local_config_outbox (id, namespace, entity_id, payload, secret, updated_at, created_at, attempts, state, owner_user_id, owner_workspace_id)
     VALUES ('legacy-1', 'chat_settings', 'legacy', '{"systemPrompt":"legacy-unknown"}', '', 100, 100, 0, 'queued', '', '')`,
  )

  await f.login('B')
  assert.equal((await f.outbox.listQueuedConfigPushes(f.ownerOf('B'))).length, 0, '未认领的行不该被 B 捡走')
  assert.equal((await f.settings.listLocalSettings(f.ownerOf('B'))).length, 0, '未认领的行不该出现在 B 的镜像里')

  f.setResponse(() => ({ applied: true, conflict: false }))
  assert.equal(await f.runtime.drainConfigOutbox(), 0)
  assert.equal(f.requests.length, 0, '未认领的行不得上传')
}))

test('a session without a verified identity writes rows that stay unclaimed', () => fixture(async f => {
  // 恢复会话：没有已验证的 userId（auth.ts：持久化元数据不是归属断言）。
  await f.auth.syncFromStorage(true)
  assert.equal(f.auth.userId, '')
  await f.enqueueAs('anon', { namespace: 'chat_settings', id: 'default', payload: { systemPrompt: 'restored' }, updatedAt: 100 })

  const rows = await f.db.query(
    `SELECT owner_user_id, owner_workspace_id FROM local_config_outbox WHERE namespace = 'chat_settings'`,
  )
  assert.equal(rows.length, 1)
  assert.equal(String(rows[0].owner_user_id), '', '恢复会话写下的行不得有归属')
  assert.equal(String(rows[0].owner_workspace_id), '')
}))
