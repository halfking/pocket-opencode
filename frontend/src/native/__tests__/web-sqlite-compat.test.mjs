// web-sqlite-compat.test.mjs — Web / SQL.js 回退路径的两个实测缺陷。
//
// 缺陷 1（SQL.js 首次初始化）：`stripFts5ForWeb` 只去掉了 notes 的 FTS 虚表
//   和 notes 的三个触发器，**assets 的三个触发器留在库里**；而 notes 迁移
//   （runNotesCaptureV1Migration）又会在 Web 上重建 notes 的三个 FTS 触发器。
//   ⇒ 触发器存在、虚表不存在 ⇒ 普通 INSERT 报 "no such table: local_notes_fts"。
//
// 缺陷 2（向量写入）：`VectorIndex.add` 把 `blob.buffer`（ArrayBuffer）交给
//   SQLite 驱动；Web 驱动拒绝 ArrayBuffer，只接受 Uint8Array 一类的视图。
//
// 本测试用真实驱动（jeep-sqlite + SQL.js）跑**全新初始化**，不复用任何夹具
// 里的 DROP TRIGGER 补救 —— 因为那正是掩盖缺陷的地方。
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import { createServer } from 'vite'
import initSqlJs from 'sql.js'
import { createPinia, setActivePinia } from 'pinia'

const SQL = await initSqlJs()

/** Real LocalDB over jeep-sqlite + SQL.js; no FTS5 triggers are dropped. */
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
  const key = `__webSQLite_${Math.random().toString(36).slice(2)}`
  console.warn = (...args) => { warnings.push(args); oldWarn(...args) }
  globalThis.fetch = async (url, options) => {
    requests.push({ url: String(url), options })
    return Response.json(await respond(String(url), options))
  }
  const vite = await createServer({
    root: fileURLToPath(new URL('../../../', import.meta.url)), configFile: false,
    server: { middlewareMode: true, hmr: false }, ssr: { noExternal: ['@capacitor-community/sqlite'] },
    optimizeDeps: { noDiscovery: true },
    plugins: [{
      name: 'web-sqlite-real-driver', enforce: 'pre',
      resolveId(source, importer) {
        if (source === '@capacitor-community/sqlite' && importer?.endsWith('/local-db.ts')) return '\0web-sqlite'
        if (importer?.endsWith('/local-db.ts') && source === './sqlite-web-init') return '\0web-web'
        if (importer?.endsWith('/local-db.ts') && source === './runtime-platform') return '\0web-platform'
      },
      load(id) {
        if (id === '\0web-sqlite') return `export const { SQLiteConnection, SQLiteDBConnection, CapacitorSQLite } = globalThis[${JSON.stringify(key)}]`
        if (id === '\0web-web') return 'export async function initSqliteWeb() {}'
        if (id === '\0web-platform') return 'export const isWebFallbackRuntime = () => true'
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
    const vectorModule = await vite.ssrLoadModule('/src/native/vector-index.ts')
    const vectorRuntime = await vite.ssrLoadModule('/src/native/vector.ts')
    await run({ db, raw, warnings, requests, vectorIndex: vectorRuntime.vectorIndex, DIM: vectorModule.DIM,
      respond: fn => { respond = fn } })
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

test('a fresh Web database can insert a note without a missing FTS table', async () => fixture(async f => {
  const triggers = f.raw.exec(
    "SELECT name FROM sqlite_master WHERE type = 'trigger' AND name LIKE 'local_%fts%'",
  )[0]?.values ?? []
  assert.deepEqual(triggers, [], 'no trigger may reference an FTS table that does not exist')
  await f.db.run(
    `INSERT INTO local_notes (id, workspace_id, title, content, content_type, encrypted_content, created_at, updated_at)
     VALUES ('note-1', 'workspace', 'Title', 'Body text', 'text', 0, 1, 1)`,
  )
  const rows = f.raw.exec("SELECT id, title FROM local_notes WHERE id = 'note-1'")[0]?.values ?? []
  assert.deepEqual(rows, [['note-1', 'Title']])
}))

test('a fresh Web database can insert an asset without a missing FTS table', async () => fixture(async f => {
  await f.db.run(
    `INSERT INTO local_assets (id, workspace_id, kind, title, body_text, body_encrypted, created_at, updated_at)
     VALUES ('asset-1', 'workspace', 'note', 'Asset', 'Body', 0, 1, 1)`,
  )
  const rows = f.raw.exec("SELECT id FROM local_assets WHERE id = 'asset-1'")[0]?.values ?? []
  assert.deepEqual(rows, [['asset-1']])
}))

test('a Web database writes and reads back a note vector', async () => fixture(async f => {
  const vector = new Float32Array(f.DIM)
  vector[0] = 1
  await f.vectorIndex.add('note-1', vector, 'fixture-model')
  const rows = f.raw.exec("SELECT note_id, dim, chunks FROM local_note_vectors WHERE note_id = 'note-1'")[0]?.values ?? []
  assert.deepEqual(rows, [['note-1', f.DIM, 1]])
  const stored = f.raw.exec("SELECT embedding FROM local_note_vectors WHERE note_id = 'note-1'")[0]?.values ?? []
  assert.equal(stored.length, 1, 'the vector blob must be readable')
  const blob = stored[0][0]
  assert.equal(blob.length, f.DIM * 4, 'the blob must hold dim float32 values')
}))
