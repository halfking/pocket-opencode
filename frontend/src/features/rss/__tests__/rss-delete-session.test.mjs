// rss-delete-session.test.mjs — RSS 删除必须绑定**发起时会话**。
//
// 审计发现（2026-10-29）：`RssListView.deleteSource` 原来是
// 「先 await 确认框，再发 DELETE」。确认框是可等待的 UI 交互，
// 弹窗期间若发生 A→B 账号切换，随后发出的 DELETE 会带上 B 的凭据，
// 于是 A 的订阅源被 B 身份删除。
//
// 本测试用真实 SQLite 驱动（jeep-sqlite + SQL.js）与真实 Pinia 会话
// 代际验证：捕获会话 → 确认框期间换号 → 删除被拒绝且请求未发出。
//
// ⚠ 只隔离平台 I/O（fetch / localStorage），其余走真模块。
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import { createServer } from 'vite'
import initSqlJs from 'sql.js'
import { createPinia, setActivePinia } from 'pinia'

const SQL = await initSqlJs()

async function until(predicate) {
  for (let i = 0; i < 100; i++) {
    if (predicate()) return
    await new Promise(resolve => setImmediate(resolve))
  }
  assert.fail('operation did not settle')
}

/** Real LocalDB + real jeep-sqlite engine; only platform I/O is isolated. */
async function fixture(run) {
  const oldStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const oldFetch = globalThis.fetch, oldWarn = console.warn
  const values = new Map(), stored = new Map(), warnings = [], requests = []
  const storage = {
    keys: () => values.keys(),
    getItem: key => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, String(value)),
    removeItem: key => values.delete(key),
  }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
  setActivePinia(createPinia())
  let respond = async () => ({}), db, raw
  const key = `__rssSQLite_${Math.random().toString(36).slice(2)}`
  console.warn = (...args) => { warnings.push(args); oldWarn(...args) }
  globalThis.fetch = async (url, options) => {
    requests.push({ url: String(url), options })
    return Response.json(await respond(String(url), options))
  }
  const vite = await createServer({
    root: fileURLToPath(new URL('../../../../', import.meta.url)), configFile: false,
    server: { middlewareMode: true, hmr: false }, ssr: { noExternal: ['@capacitor-community/sqlite'] },
    optimizeDeps: { noDiscovery: true },
    plugins: [{
      name: 'rss-real-driver', enforce: 'pre',
      resolveId(source, importer) {
        if (source === '@capacitor-community/sqlite' && importer?.endsWith('/local-db.ts')) return '\0rss-sqlite'
        if (importer?.endsWith('/local-db.ts') && source === './sqlite-web-init') return '\0rss-web'
        if (importer?.endsWith('/local-db.ts') && source === './runtime-platform') return '\0rss-platform'
      },
      load(id) {
        if (id === '\0rss-sqlite') return `export const { SQLiteConnection, SQLiteDBConnection, CapacitorSQLite } = globalThis[${JSON.stringify(key)}]`
        if (id === '\0rss-web') return 'export async function initSqliteWeb() {}'
        if (id === '\0rss-platform') return 'export const isWebFallbackRuntime = () => true'
      },
    }],
  })
  try {
    const { Database } = await vite.ssrLoadModule('/node_modules/jeep-sqlite/dist/collection/utils/database.js')
    const definitions = await vite.ssrLoadModule('/node_modules/@capacitor-community/sqlite/dist/esm/definitions.js')
    const { registerPlugin } = await vite.ssrLoadModule('/node_modules/@capacitor/core/dist/index.js')
    raw = new SQL.Database()
    const store = {
      getItem: async key => stored.get(key), setItem: async (key, value) => stored.set(key, value),
      removeItem: async key => stored.delete(key),
    }
    const driver = new Database('lobster', 1, {}, store, false, '')
    driver.mDb = raw
    const platform = {
      initWebStore: async () => {}, checkConnectionsConsistency: async () => ({ result: true }),
      createConnection: async () => {},
      open: async () => { driver._isDBOpen = true }, close: async () => driver.close(), closeConnection: async () => {},
      saveToStore: async () => driver.saveToStore(), loadExtension: async () => {},
      execute: async o => ({ changes: { changes: await driver.executeSQL(o.statements, o.transaction) } }),
      executeSet: async o => ({ changes: await driver.execSet(o.set, o.transaction, o.returnMode) }),
      run: async o => ({ changes: await driver.runSQL(o.statement, o.values, o.transaction, o.returnMode) }),
      query: async o => ({ values: await driver.selectSQL(o.statement, o.values) }),
      beginTransaction: async () => ({ changes: { changes: await driver.beginTransaction() } }),
      commitTransaction: async () => ({ changes: { changes: await driver.commitTransaction() } }),
      rollbackTransaction: async () => ({ changes: { changes: await driver.rollbackTransaction() } }),
      isTransactionActive: async () => ({ result: driver.isTransActive() }),
    }
    globalThis[key] = { ...definitions, CapacitorSQLite: registerPlugin(key, { web: async () => platform }) }
    ;({ localDB: db } = await vite.ssrLoadModule('/src/native/local-db.ts'))
    await db.init('')
    // SQL.js omits FTS5. This fixture exercises session ownership, not FTS.
    raw.run('DROP TRIGGER IF EXISTS local_notes_ai; DROP TRIGGER IF EXISTS local_notes_ad; DROP TRIGGER IF EXISTS local_notes_au; DROP TRIGGER IF EXISTS local_assets_ai; DROP TRIGGER IF EXISTS local_assets_ad; DROP TRIGGER IF EXISTS local_assets_au;')
    const policy = await vite.ssrLoadModule('/src/stores/syncPolicyStore.ts')
    policy.installSyncPolicyStorage(storage)
    const policyStore = policy.useSyncPolicyStore()
    const { useAuthStore, captureAuthSession, isCurrentAuthSession } = await vite.ssrLoadModule('/src/stores/auth.ts')
    const auth = useAuthStore()
    const scope = () => JSON.stringify([auth.user, auth.workspaceId])
    policy.installSyncPolicyScopeResolver(scope)
    policy.installSyncPolicyActivationHost({
      captureContext() {
        if (auth.activationStatus !== 'ready') return null
        const session = captureAuthSession(auth), version = auth.activationVersion, captured = scope()
        return {
          scope: captured,
          intentScope: auth.userId ? JSON.stringify([auth.userId, auth.workspaceId]) : '',
          workspace: auth.workspaceId,
          isCurrent: () => scope() === captured && auth.activationVersion === version && isCurrentAuthSession(auth, session),
        }
      },
      activate: (modes, publish) => db.requestPolicySnapshotActivation(modes, publish),
    })
    const login = user => {
      return auth.setAuthWithWorkspace(`token-${user}`, user, `user-${user}`, 'workspace')
        .then(() => policyStore.applyPolicy({ system_default: 'cloud', allow_override: true, modules: { rss: 'cloud' } }, Date.now()))
    }
    await login('A')
    const rssModule = await vite.ssrLoadModule('/src/api/rss.ts')
    const rss = rssModule.rssApi ?? rssModule.default?.rssApi
    const rssOperation = await vite.ssrLoadModule('/src/features/rss/rss-operation.ts')
    await run({ rss, rssOperation, auth, requests, warnings, db, raw, login, respond: fn => { respond = fn } })
  } finally {
    await db?.close().catch(() => {})
    await vite.close()
    delete globalThis[key]
    globalThis.fetch = oldFetch
    console.warn = oldWarn
    if (oldStorage) Object.defineProperty(globalThis, 'localStorage', oldStorage)
    else delete globalThis.localStorage
  }
}

test('a source deleted after an account switch is refused and never reaches the server', async () => fixture(async f => {
  // The confirmation dialog is awaited by the flow; the account switch happens
  // while the dialog is open, exactly like a user leaving the dialog open.
  const outcome = await f.rssOperation.deleteRssSource(
    { id: 'source-1', title: 'Example' },
    async () => { await f.login('B'); return true },
    (id, options) => f.rss.deleteSource(id, options),
  )
  assert.equal(outcome, 'stale-session')
  assert.equal(f.requests.length, 0, 'no request may leave with the new account')
}))

test('a source deleted under the same account still reaches the server', async () => fixture(async f => {
  const outcome = await f.rssOperation.deleteRssSource(
    { id: 'source-1', title: 'Example' },
    async () => true,
    (id, options) => f.rss.deleteSource(id, options),
  )
  assert.equal(outcome, 'deleted')
  const deletes = f.requests.filter(r => r.options?.method === 'DELETE')
  assert.equal(deletes.length, 1)
  assert.match(deletes[0].url, /\/api\/rss\/sources\/source-1$/)
}))

test('a cancelled confirmation sends no request', async () => fixture(async f => {
  const outcome = await f.rssOperation.deleteRssSource(
    { id: 'source-1', title: 'Example' },
    async () => false,
    (id, options) => f.rss.deleteSource(id, options),
  )
  assert.equal(outcome, 'cancelled')
  assert.equal(f.requests.length, 0)
}))

test('an A to B to A switch still invalidates the captured session', async () => fixture(async f => {
  const outcome = await f.rssOperation.deleteRssSource(
    { id: 'source-1', title: 'Example' },
    async () => { await f.login('B'); await f.login('A'); return true },
    (id, options) => f.rss.deleteSource(id, options),
  )
  assert.equal(outcome, 'stale-session')
  assert.equal(f.requests.length, 0)
}))
