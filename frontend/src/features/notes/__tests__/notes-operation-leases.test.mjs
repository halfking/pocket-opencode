import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import { createServer } from 'vite'
import initSqlJs from 'sql.js'
import { createPinia, setActivePinia } from 'pinia'

const SQL = await initSqlJs()
function gate() {
  let release, entered
  const waiting = new Promise(resolve => { release = resolve })
  const started = new Promise(resolve => { entered = resolve })
  return { started, release, pause: async () => { entered(); await waiting } }
}
async function until(predicate) {
  for (let i = 0; i < 100; i++) {
    if (predicate()) return
    await new Promise(resolve => setImmediate(resolve))
  }
  assert.fail('operation did not settle')
}

/** Actual LocalDB, plugin Promise bridge and jeep SQL engine; only platform I/O is isolated. */
async function fixture(run) {
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
  const vite = await createServer({ root: fileURLToPath(new URL('../../../../', import.meta.url)), configFile: false,
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
    const { vectorIndex } = await vite.ssrLoadModule('/src/native/vector.ts')
    const crypto = await vite.ssrLoadModule('/src/native/crypto.ts')
    const { useCryptoConfig } = await vite.ssrLoadModule('/src/stores/crypto-config.ts')
    await crypto.initAppCrypto('fixture-password')
    const count = table => Number(raw.exec(`SELECT count(*) FROM ${table}`)[0].values[0][0])
    respond = async url => url.endsWith('/api/embed') ? { embedding: Array(1536).fill(1), model: 'fixture-model' } : {}
    await run({ notes, db, raw, auth, requests, warnings, vectorIndex, setMode, login, count, crypto, cryptoConfig: useCryptoConfig(),
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

for (const roundtrip of [false, true]) test(`create keeps its original owner across encryption${roundtrip ? ' ABA' : ''}`, async () => fixture(async f => {
  f.cryptoConfig.setFieldEncryption('enabled')
  const paused = gate()
  f.onEncrypt(paused.pause)
  const saving = f.notes.createNote({ content: 'private note', workspaceId: 'workspace' })
  saving.catch(() => {})
  await Promise.race([paused.started, saving.then(() => { throw new Error("save completed before pause") })])
  await f.login('B')
  if (roundtrip) await f.login('A')
  paused.release()
  await assert.rejects(saving, { name: 'AbortError' })
  assert.equal(f.count('local_notes'), 0)
  assert.equal(f.requests.length, 0)
}))

test('update cannot write after its encryption crosses an owner change', async () => fixture(async f => {
  await f.db.run("INSERT INTO local_notes (id, workspace_id, content, content_type, encrypted_content, created_at, updated_at) VALUES ('note-1', 'workspace', 'original', 'text', 0, 1, 1)")
  f.cryptoConfig.setFieldEncryption('enabled')
  const paused = gate()
  f.onEncrypt(paused.pause)
  const saving = f.notes.updateNote('note-1', { content: 'private replacement' }, 'workspace')
  saving.catch(() => {})
  await Promise.race([paused.started, saving.then(() => { throw new Error("save completed before pause") })])
  await f.login('B')
  paused.release()
  await assert.rejects(saving, { name: 'AbortError' })
  assert.equal(f.raw.exec("SELECT content FROM local_notes WHERE id = 'note-1'")[0].values[0][0], 'original')
}))

test('an attachment conversion cannot attach old content after owner activation', async () => fixture(async f => {
  const paused = gate()
  f.onRead(paused.pause)
  const saving = f.notes.createNote({ content: 'original parent', workspaceId: 'workspace', media: [{ kind: 'image', blob: new Blob(['private image'], { type: 'image/png' }) }] })
  saving.catch(() => {})
  await Promise.race([paused.started, saving.then(() => { throw new Error("save completed before pause") })])
  await f.login('B')
  paused.release()
  await assert.rejects(saving, { name: 'AbortError' })
  assert.equal(f.count('local_notes'), 1, 'already committed parent is retained')
  assert.equal(f.raw.exec("SELECT count(*) FROM local_note_files WHERE kind = 'image'")[0].values[0][0], 0)
  assert.equal(f.requests.length, 0)
}))

test('a delayed embedding response cannot persist after policy local/cloud ABA', async () => fixture(async f => {
  const paused = gate()
  f.respond(async url => {
    if (url.endsWith('/api/embed')) { await paused.pause(); return { embedding: Array(1536).fill(1), model: 'fixture-model' } }
    return {}
  })
  const note = await f.notes.createNote({ content: 'cloud text', workspaceId: 'workspace' })
  await paused.started
  await f.setMode('local')
  await f.setMode('cloud')
  paused.release()
  await f.notes.awaitNoteMirror(note.id)
  await until(() => f.warnings.some(w => String(w[0]).includes('嵌入失败')) || f.count('local_note_vectors') > 0)
  assert.equal(f.warnings.find(w => String(w[0]).includes('嵌入失败'))?.at(-1)?.name, 'AbortError')
  assert.equal(f.count('local_note_vectors'), 0)
  assert.equal(f.vectorIndex.size(), 0)
}))

test('local policy changes do not cancel local encryption/save or start new cloud work', async () => fixture(async f => {
  f.cryptoConfig.setFieldEncryption('enabled')
  const paused = gate()
  f.onEncrypt(paused.pause)
  const saving = f.notes.createNote({ content: 'keep local edit', workspaceId: 'workspace' })
  await paused.started
  await f.setMode('local')
  paused.release()
  const note = await saving
  await f.notes.awaitNoteMirror(note.id)
  assert.equal(f.count('local_notes'), 1)
  assert.equal(f.requests.length, 0)
}))

test('a caller can retain its earlier owner lease and disable notes background uploads', async () => fixture(async f => {
  const lease = f.db.captureOperation({ kind: 'local-edit' })
  const note = await f.notes.createNote({ content: 'meeting local ingestion', workspaceId: 'workspace' }, { lease, backgroundCloud: false })
  await f.notes.awaitNoteMirror(note.id)
  assert.equal(f.count('local_notes'), 1)
  assert.equal(f.requests.length, 0)
  await f.login('B')
  await assert.rejects(f.notes.createNote({ content: 'old meeting', workspaceId: 'workspace' }, { lease, backgroundCloud: false }), { name: 'AbortError' })
  assert.equal(f.count('local_notes'), 1)
}))

for (const deleting of [false, true]) test(`${deleting ? 'local note deletion' : 'imported note update'} cannot continue after decryption crosses owner`, async () => fixture(async f => {
  const encrypted = await f.crypto.encryptString('original imported body')
  if (deleting) await f.db.run("INSERT INTO local_notes (id,workspace_id,title,content,encrypted_content,created_at,updated_at) VALUES ('asset-1','workspace','Original',?,1,1,1)", [encrypted])
  else await f.db.run("INSERT INTO local_assets (id,workspace_id,kind,title,body_text,body_encrypted,source,sync_mode,created_at,updated_at) VALUES ('asset-1','workspace','note','Original',?,1,'enex_import','e2ee_local_first',1,1)", [encrypted])
  const paused = gate()
  f.onDecrypt(paused.pause)
  const editing = deleting ? f.notes.deleteNote('asset-1', 'workspace') : f.notes.updateNote('asset-1', { title: 'New' }, 'workspace')
  editing.catch(() => {})
  await Promise.race([paused.started, editing.then(() => { throw new Error('operation completed before decryption') })])
  await f.login('B')
  paused.release()
  await assert.rejects(editing, { name: 'AbortError' })
  const table = deleting ? 'local_notes' : 'local_assets'
  assert.equal(f.raw.exec(`SELECT title FROM ${table} WHERE id='asset-1'`)[0].values[0][0], 'Original')
  assert.equal(f.raw.exec(`SELECT deleted_at FROM ${table} WHERE id='asset-1'`)[0].values[0][0], null)
}))

test('mirror classify cannot start after a local/cloud policy ABA while upload was in flight', async () => fixture(async f => {
  const paused = gate()
  f.respond(async url => {
    if (url.endsWith('/api/notes')) { await paused.pause(); return {} }
    if (url.endsWith('/api/embed')) return { embedding: [], model: 'fixture-model' }
    return {}
  })
  const note = await f.notes.createNote({ content: 'cloud note', workspaceId: 'workspace' })
  await paused.started
  await f.setMode('local')
  await f.setMode('cloud')
  paused.release()
  await f.notes.awaitNoteMirror(note.id)
  assert.equal(f.requests.some(r => r.url.endsWith('/classify')), false)
}))

test('policy ABA during proactive refresh cancels background requests before their first send', async () => fixture(async f => {
  const paused = gate()
  f.auth.maybeRefresh = paused.pause
  const note = await f.notes.createNote({ content: 'original cloud operation', workspaceId: 'workspace' })
  await paused.started
  await f.setMode('local')
  await f.setMode('cloud')
  paused.release()
  await f.notes.awaitNoteMirror(note.id)
  await until(() => f.warnings.some(w => String(w[0]).includes('嵌入失败')))
  assert.equal(f.requests.length, 0)
  assert.equal(f.count('local_note_vectors'), 0)
}))
