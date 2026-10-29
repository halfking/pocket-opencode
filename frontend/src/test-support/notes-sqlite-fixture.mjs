import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import initSqlJs from 'sql.js'
import { createPinia, setActivePinia } from 'pinia'

const SQL = await initSqlJs()
export function gate() {
  let release, entered
  const waiting = new Promise(resolve => { release = resolve })
  const started = new Promise(resolve => { entered = resolve })
  return { started, release, pause: async () => { entered(); await waiting } }
}
export async function until(predicate) {
  for (let i = 0; i < 100; i++) {
    if (predicate()) return
    await new Promise(resolve => setImmediate(resolve))
  }
  assert.fail('operation did not settle')
}

/** Actual LocalDB, plugin Promise bridge and jeep SQL engine; only platform I/O is isolated. */
export async function fixture(run) {
  const oldStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const oldFetch = globalThis.fetch, oldReader = globalThis.FileReader, oldWarn = console.warn
  const values = new Map(), stored = new Map(), warnings = [], requests = []
  const storage = { keys: () => values.keys(), getItem: key => values.get(key) ?? null,
    setItem: (key, value) => values.set(key, String(value)), removeItem: key => values.delete(key) }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
  setActivePinia(createPinia())
  let afterQuery = async () => {}, afterRun = async () => {}, beforeEncrypt = async () => {}, beforeDecrypt = async () => {}, beforeRead = async () => {}
  let respond = async () => ({}), db, driver, raw
  const key = `__noteSQLite_${Math.random().toString(36).slice(2)}`
  console.warn = (...args) => { warnings.push(args); oldWarn(...args) }
  globalThis.FileReader = class {
    readAsDataURL(blob) {
      Promise.resolve().then(() => beforeRead()).then(() => blob.arrayBuffer()).then(buffer => {
        this.result = `data:${blob.type};base64,${Buffer.from(buffer).toString('base64')}`
        this.onloadend?.()
      }).catch(error => { this.error = error; this.onerror?.() })
    }
  }
  globalThis.fetch = async (url, options) => {
    requests.push({ url: String(url), options })
    return Response.json(await respond(String(url), options))
  }
  const vite = await createServer({ root: fileURLToPath(new URL('../../', import.meta.url)), configFile: false,
    server: { middlewareMode: true, hmr: false }, ssr: { noExternal: ['@capacitor-community/sqlite'] },
    optimizeDeps: { noDiscovery: true }, plugins: [{ name: 'notes-real-driver', enforce: 'pre',
      resolveId(source, importer) {
        if (source === '@capacitor-community/sqlite' && importer?.endsWith('/local-db.ts')) return '\0notes-sqlite'
        if (importer?.endsWith('/local-db.ts') && source === './sqlite-web-init') return '\0notes-web'
        if (importer?.endsWith('/local-db.ts') && source === './runtime-platform') return '\0notes-platform'
        if (importer?.endsWith('/notes-persist.ts') && source === '../../native/crypto') return '\0notes-crypto'
        if (importer?.endsWith('/notes-row.ts') && source === '../../native/crypto') return '\0notes-crypto'
        if (importer?.endsWith('/asset-store.ts') && source === './crypto') return '\0notes-crypto'
      },
      load(id) {
        if (id === '\0notes-sqlite') return `export const { SQLiteConnection, SQLiteDBConnection, CapacitorSQLite } = globalThis[${JSON.stringify(key)}]`
        if (id === '\0notes-web') return 'export async function initSqliteWeb() {}'
        if (id === '\0notes-platform') return 'export const isWebFallbackRuntime = () => true'
        if (id === '\0notes-crypto') return `import { encryptString as encrypt, decryptString as decrypt } from '/src/native/crypto.ts'; export async function encryptString(value) { await globalThis[${JSON.stringify(key)}].beforeEncrypt(); return encrypt(value) }; export async function decryptString(value) { await globalThis[${JSON.stringify(key)}].beforeDecrypt(); return decrypt(value) }`
      },
    }] })
  try {
    const { Database } = await vite.ssrLoadModule('/node_modules/jeep-sqlite/dist/collection/utils/database.js')
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
      run: async o => { const changes = await driver.runSQL(o.statement, o.values, o.transaction, o.returnMode); await afterRun(o.statement); return { changes } },
      query: async o => { const rows = await driver.selectSQL(o.statement, o.values); await afterQuery(o.statement); return { values: rows } },
      beginTransaction: async () => ({ changes: { changes: await driver.beginTransaction() } }),
      commitTransaction: async () => ({ changes: { changes: await driver.commitTransaction() } }),
      rollbackTransaction: async () => ({ changes: { changes: await driver.rollbackTransaction() } }),
      isTransactionActive: async () => ({ result: driver.isTransActive() }),
    }
    globalThis[key] = { ...definitions, CapacitorSQLite: registerPlugin(key, { web: async () => platform }), beforeEncrypt: () => beforeEncrypt(), beforeDecrypt: () => beforeDecrypt() }
    ;({ localDB: db } = await vite.ssrLoadModule('/src/native/local-db.ts'))
    await db.init('')
    // SQL.js omits FTS5. This fixture exercises transaction ownership, not FTS.
    raw.run('DROP TRIGGER IF EXISTS local_notes_ai; DROP TRIGGER IF EXISTS local_notes_ad; DROP TRIGGER IF EXISTS local_notes_au; DROP TRIGGER IF EXISTS local_assets_ai; DROP TRIGGER IF EXISTS local_assets_ad; DROP TRIGGER IF EXISTS local_assets_au;')
    const policy = await vite.ssrLoadModule('/src/stores/syncPolicyStore.ts')
    policy.installSyncPolicyStorage(storage)
    const policyStore = policy.useSyncPolicyStore()
    const { useAuthStore, captureAuthSession, isCurrentAuthSession } = await vite.ssrLoadModule('/src/stores/auth.ts')
    const auth = useAuthStore()
    const scope = () => JSON.stringify([auth.user, auth.workspaceId])
    policy.installSyncPolicyScopeResolver(scope)
    policy.installSyncPolicyActivationHost({ captureContext() {
      if (auth.activationStatus !== 'ready') return null
      const session = captureAuthSession(auth), version = auth.activationVersion, captured = scope()
      return { scope: captured, intentScope: auth.userId ? JSON.stringify([auth.userId, auth.workspaceId]) : '', workspace: auth.workspaceId,
        isCurrent: () => scope() === captured && auth.activationVersion === version && isCurrentAuthSession(auth, session) }
    }, activate: (modes, publish) => db.requestPolicySnapshotActivation(modes, publish) })
    const setMode = mode => policyStore.applyPolicy({ system_default: 'cloud', allow_override: true, modules: { notes: mode } }, Date.now())
    const login = async (user = 'A') => {
      await auth.setAuthWithWorkspace(`token-${user}`, user, `user-${user}`, 'workspace')
      await setMode('cloud')
    }
    await login()
    const notes = await vite.ssrLoadModule('/src/features/notes/notes-persist.ts')
    const { assetStore } = await vite.ssrLoadModule('/src/native/asset-store.ts')
    const pkm = await vite.ssrLoadModule('/src/features/pkm/pkm-store.ts')
    const { portalNotesStore } = await vite.ssrLoadModule('/src/native/portal-sync/storage.ts')
    await portalNotesStore(db.bindOperation(db.captureOperation({kind:'local-edit'}))).observeEpoch(0)
    const { vectorIndex } = await vite.ssrLoadModule('/src/native/vector.ts')
    const crypto = await vite.ssrLoadModule('/src/native/crypto.ts')
    const { useCryptoConfig } = await vite.ssrLoadModule('/src/stores/crypto-config.ts')
    await crypto.initAppCrypto('fixture-password')
    const count = table => Number(raw.exec(`SELECT count(*) FROM ${table}`)[0].values[0][0])
    respond = async url => url.endsWith('/api/embed') ? { embedding: Array(1536).fill(1), model: 'fixture-model' } : {}
    await run({ loadModule: path => vite.ssrLoadModule(path), notes, assetStore, pkm, portalNotesStore, db, raw, auth, requests, warnings, vectorIndex, setMode, setModules: modules => policyStore.applyPolicy({system_default:'cloud',allow_override:true,modules:{...policyStore.modules,...modules}},Date.now()), login: async user => {await login(user);await portalNotesStore(db.bindOperation(db.captureOperation({kind:'local-edit'}))).observeEpoch(0)}, count, crypto, cryptoConfig: useCryptoConfig(),
      onEncrypt: fn => { beforeEncrypt = fn }, onRead: fn => { beforeRead = fn }, onQuery: fn => { afterQuery = fn },
      onDecrypt: fn => { beforeDecrypt = fn },
      onRun: fn => { afterRun = fn }, respond: fn => { respond = fn } })
  } finally {
    afterQuery = afterRun = beforeEncrypt = beforeDecrypt = beforeRead = async () => {}
    await db?.close().catch(() => {})
    await vite.close()
    delete globalThis[key]
    globalThis.fetch = oldFetch
    globalThis.FileReader = oldReader
    console.warn = oldWarn
    if (oldStorage) Object.defineProperty(globalThis, 'localStorage', oldStorage)
    else delete globalThis.localStorage
  }
}

