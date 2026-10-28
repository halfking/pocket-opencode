import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createServer as createHttpServer } from 'node:http'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import initSqlJs from 'sql.js'

function deferred() {
  let resolve
  const promise = new Promise(r => { resolve = r })
  return { promise, resolve }
}

function strictNativeClose({ platform, driver }) {
  const close = platform.close
  // Android CapacitorSQLite.close rejects unopened handles, unlike jeep close.
  platform.close = async () => {
    if (!driver.isDBOpen()) throw new Error('database lobster not opened')
    if (driver.isTransActive()) throw new Error('database lobster still in transaction')
    return close()
  }
}

// Only platform/lifecycle plumbing is replaced. SQL and transactions use the
// installed SQLiteDBConnection, Capacitor proxy, jeep Database and SQL.js.
async function withDatabase(run, initialize = true, stored = new Map()) {
  const key = `__localDbExecutor_${Math.random().toString(36).slice(2)}`
  const vite = await createServer({
    root: fileURLToPath(new URL('../../../', import.meta.url)),
    configFile: false, server: { middlewareMode: true, hmr: { server: createHttpServer() } },
    ssr: { noExternal: ['@capacitor-community/sqlite'] },
    optimizeDeps: { noDiscovery: true },
    plugins: [{
      name: 'local-db-platform-fixture', enforce: 'pre',
      resolveId(source, importer) {
        if (source === '@capacitor-community/sqlite' && importer?.endsWith('/local-db.ts')) return '\0executor-sqlite'
        if (importer?.endsWith('/local-db.ts') && source === './sqlite-web-init') return '\0executor-web'
        if (importer?.endsWith('/local-db.ts') && source === './runtime-platform') return '\0executor-platform'
      },
      load(id) {
        if (id === '\0executor-sqlite') return `export const { SQLiteConnection, SQLiteDBConnection, CapacitorSQLite } = globalThis[${JSON.stringify(key)}]`
        if (id === '\0executor-web') return 'export async function initSqliteWeb() {}'
        if (id === '\0executor-platform') return 'export const isWebFallbackRuntime = () => true'
      },
    }],
  })
  let db, raw, closed = false
  try {
    const SQL = await initSqlJs()
    const store = { getItem: async k => stored.get(k), setItem: async (k, v) => stored.set(k, v), removeItem: async k => stored.delete(k) }
    raw = new SQL.Database(stored.get('lobster'))
    const { Database } = await vite.ssrLoadModule('/node_modules/jeep-sqlite/dist/collection/utils/database.js')
    const { UtilsSQLite } = await vite.ssrLoadModule('/node_modules/jeep-sqlite/dist/collection/utils/utils-sqlite.js')
    const definitions = await vite.ssrLoadModule('/node_modules/@capacitor-community/sqlite/dist/esm/definitions.js')
    const { registerPlugin } = await vite.ssrLoadModule('/node_modules/@capacitor/core/dist/index.js')
    const driver = new Database('lobster', 1, {}, store, false, '')
    driver.mDb = raw
    driver._isDBOpen = false
    const lifecycle = []
    const platform = {
      initWebStore: async () => {}, checkConnectionsConsistency: async () => ({ result: true }),
      createConnection: async () => { lifecycle.push('create') },
      open: async () => {
        lifecycle.push('open')
        if (closed) { raw = new SQL.Database(stored.get('lobster')); driver.mDb = raw; closed = false }
        driver._isDBOpen = true
      },
      close: async () => { lifecycle.push('close'); await driver.close(); closed = true },
      closeConnection: async () => {
        lifecycle.push('remove')
        if (driver.isDBOpen()) await driver.close()
        closed = true
      },
      saveToStore: async () => driver.saveToStore(),
      execute: async o => ({ changes: { changes: await driver.executeSQL(o.statements, o.transaction) } }),
      executeSet: async o => ({ changes: await driver.execSet(o.set, o.transaction, o.returnMode) }),
      run: async o => ({ changes: await driver.runSQL(o.statement, o.values, o.transaction, o.returnMode) }),
      query: async o => ({ values: await driver.selectSQL(o.statement, o.values) }),
      beginTransaction: async () => ({ changes: { changes: await driver.beginTransaction() } }),
      commitTransaction: async () => ({ changes: { changes: await driver.commitTransaction() } }),
      rollbackTransaction: async () => ({ changes: { changes: await driver.rollbackTransaction() } }),
      isTransactionActive: async () => ({ result: driver.isTransActive() }),
      loadExtension: async () => { lifecycle.push('extension') },
    }
    globalThis[key] = { ...definitions, CapacitorSQLite: registerPlugin(key, { web: async () => platform }) }
    ;({ localDB: db } = await vite.ssrLoadModule('/src/native/local-db.ts'))
    if (initialize) {
      await db.init('')
      await db.execute('CREATE TABLE executor_records (id INTEGER PRIMARY KEY, title TEXT); INSERT INTO executor_records VALUES(1,\'original\');')
    }
    await run({ db, driver, raw, platform, lifecycle, UtilsSQLite, saved: () => stored.get('lobster'), SQL })
  } finally {
    if (db) await db.close().catch(() => {})
    try { raw?.close() } catch { /* already closed by the real driver */ }
    delete globalThis[key]
    await vite.close()
  }
}

test('a normal write cannot join or commit a failing batch transaction', () => withDatabase(async ({ db, UtilsSQLite }) => {
  const entered = deferred(), release = deferred()
  const original = UtilsSQLite.run
  UtilsSQLite.run = async (...args) => {
    const result = await original.apply(UtilsSQLite, args)
    if (args[1] === 'UPDATE executor_records SET title=? WHERE id=1') {
      entered.resolve()
      await release.promise
    }
    return result
  }
  const batch = db.runInTransaction([
    { statement: 'UPDATE executor_records SET title=? WHERE id=1', values: ['batch'] },
    { statement: 'INSERT INTO executor_records VALUES(1,?)', values: ['duplicate'] },
  ])
  const batchResult = batch.then(() => null, error => error)
  await entered.promise
  let outsideFinished = false
  const outside = db.run('INSERT INTO executor_records VALUES(2,?)', ['outside']).then(() => { outsideFinished = true })
  await new Promise(resolve => setImmediate(resolve))
  const finishedBeforeRelease = outsideFinished
  release.resolve()
  try {
    assert.match(String(await batchResult), /UNIQUE/)
    await outside
    assert.equal(finishedBeforeRelease, false, 'ordinary writes must wait until batch rollback completes')
    assert.deepEqual(await db.query('SELECT * FROM executor_records ORDER BY id'), [
      { id: 1, title: 'original' }, { id: 2, title: 'outside' },
    ])
  } finally { UtilsSQLite.run = original }
}))

test('failed close keeps the connection unavailable until cleanup succeeds', () => withDatabase(async fixture => {
  const { db, platform, lifecycle } = fixture
  strictNativeClose(fixture)
  const remove = platform.closeConnection
  platform.closeConnection = async () => { throw new Error('injected close failure') }
  try {
    await assert.rejects(db.close(), /injected close failure/)
    assert.equal(db.isReady(), false)
    await assert.rejects(db.run('INSERT INTO executor_records VALUES(2,?)', ['blocked']), /未初始化/)
    await assert.rejects(db.init(''), /injected close failure/)
    assert.equal(lifecycle.filter(x => x === 'create').length, 1)
  } finally { platform.closeConnection = remove }
  await db.init('')
  assert.equal(db.isReady(), true)
  assert.deepEqual(await db.query('SELECT * FROM executor_records'), [{ id: 1, title: 'original' }])
}))

test('a failed open rejects queued reads and can be initialized again', () => withDatabase(async fixture => {
  const { db, platform } = fixture
  strictNativeClose(fixture)
  const open = platform.open
  platform.open = async () => { throw new Error('injected open failure') }
  try {
    const init = db.init('')
    const query = db.queryOne('SELECT 1')
    await assert.rejects(init, /injected open failure/)
    await assert.rejects(query, /未初始化/)
    assert.equal(db.isReady(), false)
  } finally { platform.open = open }
  await db.init('')
  assert.deepEqual(await db.queryOne('SELECT COUNT(*) AS count FROM local_notes'), { count: 0 })
}, false))

test('an unopened native connection can be removed before a fresh initialization', () => withDatabase(async fixture => {
  const { db, platform } = fixture
  strictNativeClose(fixture)
  const open = platform.open
  platform.open = async () => { throw new Error('native open failed') }
  try { await assert.rejects(db.init(''), /native open failed/) }
  finally { platform.open = open }
  await db.close()
  await db.init('')
  assert.deepEqual(await db.queryOne('SELECT COUNT(*) AS count FROM local_notes'), { count: 0 })
}, false))

test('native cleanup retries after physical close succeeds but connection removal fails', () => withDatabase(async fixture => {
  const { db, platform, driver } = fixture
  strictNativeClose(fixture)
  const remove = platform.closeConnection
  let fail = true
  platform.closeConnection = async () => {
    if (driver.isDBOpen()) await driver.close()
    if (fail) throw new Error('native removal failed after physical close')
    return remove()
  }
  await assert.rejects(db.close(), /native removal failed/)
  assert.equal(driver.isDBOpen(), false)
  assert.equal(db.isReady(), false)
  await assert.rejects(db.query('SELECT 1'), /未初始化/)
  fail = false
  await db.close()
  await db.init('')
  assert.deepEqual(await db.queryOne('SELECT title FROM executor_records'), { title: 'original' })
}))

test('an extension load cannot be interrupted by close', () => withDatabase(async ({ db, platform, lifecycle }) => {
  const entered = deferred(), release = deferred()
  platform.loadExtension = async () => { entered.resolve(); await release.promise }
  const extension = db.tryLoadVecExtension('test.so')
  await entered.promise
  const closing = db.close()
  await new Promise(resolve => setImmediate(resolve))
  const beforeRelease = [...lifecycle]
  release.resolve()
  assert.equal(await extension, true)
  await closing
  assert.deepEqual(beforeRelease, ['create', 'open'])
  assert.equal(await db.tryLoadVecExtension('test.so'), false)
}))

test('a commit failure before native commit rolls back and releases the queue', () => withDatabase(async ({ db, platform }) => {
  const commit = platform.commitTransaction
  platform.commitTransaction = async () => { throw new Error('injected commit failure') }
  try { await assert.rejects(db.run('UPDATE executor_records SET title=? WHERE id=1', ['bad']), /injected commit failure/) }
  finally { platform.commitTransaction = commit }
  assert.equal(db.isReady(), true)
  await db.run('INSERT INTO executor_records VALUES(2,?)', ['later'])
  assert.deepEqual(await db.query('SELECT * FROM executor_records ORDER BY id'), [
    { id: 1, title: 'original' }, { id: 2, title: 'later' },
  ])
}))

test('a rejected commit reply after native commit is not silently retried', () => withDatabase(async ({ db, platform }) => {
  const commit = platform.commitTransaction
  platform.commitTransaction = async () => { await commit(); throw new Error('lost commit reply') }
  try { await assert.rejects(db.run('UPDATE executor_records SET title=? WHERE id=1', ['committed']), /回滚失败/) }
  finally { platform.commitTransaction = commit }
  assert.equal(db.isReady(), false)
  await assert.rejects(db.query('SELECT 1'), /关闭.*重新初始化/)
  await db.close()
  await db.init('')
  assert.deepEqual(await db.queryOne('SELECT title FROM executor_records'), { title: 'committed' })
}))

test('a rejected begin reply cannot roll back a transaction it may not own', () => withDatabase(async ({ db, platform, driver }) => {
  await driver.beginTransaction()
  const begin = platform.beginTransaction
  let rollbacks = 0
  const rollback = platform.rollbackTransaction
  platform.rollbackTransaction = async () => { rollbacks++; return rollback() }
  try {
    await assert.rejects(db.run('INSERT INTO executor_records VALUES(2,?)', ['outside']), /transaction/i)
    assert.equal(rollbacks, 0)
    assert.equal(driver.isTransActive(), true)
    assert.equal(db.isReady(), false)
  } finally {
    platform.beginTransaction = begin
    platform.rollbackTransaction = rollback
    await driver.rollbackTransaction().catch(() => {})
  }
}))

// Separate tabs each open a private SQL.js snapshot and have independent queues.
// This records the explicit boundary: an instance queue is not a cross-tab lock.
test('separate runtime instances can still overwrite a shared Web store snapshot', async () => {
  const shared = new Map()
  await withDatabase(async ({ db: first, driver, SQL, saved }) => {
    driver.autoSave = true
    await first.run('UPDATE executor_records SET title=? WHERE id=1', ['snapshot'])
    await withDatabase(async ({ db: second, driver: secondDriver }) => {
      secondDriver.autoSave = true
      await second.init('')
      await first.run('UPDATE executor_records SET title=? WHERE id=1', ['new first-tab edit'])
      await second.run('INSERT INTO executor_records VALUES(2,?)', ['second-tab edit'])
      assert.deepEqual(await first.queryOne('SELECT title FROM executor_records WHERE id=1'), { title: 'new first-tab edit' })
      const persisted = new SQL.Database(saved())
      try { assert.equal(persisted.exec('SELECT title FROM executor_records WHERE id=1')[0].values[0][0], 'snapshot') }
      finally { persisted.close() }
    }, false, shared)
  }, true, shared)
})

test('reads and execute wait for the complete batch transaction', () => withDatabase(async ({ db, UtilsSQLite }) => {
  const entered = deferred(), release = deferred()
  const original = UtilsSQLite.run
  UtilsSQLite.run = async (...args) => {
    const result = await original.apply(UtilsSQLite, args)
    if (args[1] === 'UPDATE executor_records SET title=? WHERE id=1') {
      entered.resolve()
      await release.promise
    }
    return result
  }
  const batch = db.runInTransaction([{ statement: 'UPDATE executor_records SET title=? WHERE id=1', values: ['batch'] }])
  const result = batch.then(() => null, error => error)
  await entered.promise
  const finished = []
  const read = db.queryOne('SELECT title FROM executor_records WHERE id=1').then(row => { finished.push('read'); return row })
  const write = db.execute("INSERT INTO executor_records VALUES(2,'execute')", true).then(() => { finished.push('execute') })
  await new Promise(resolve => setImmediate(resolve))
  const beforeRelease = [...finished]
  release.resolve()
  try {
    await write
    assert.equal(await result, null)
    assert.deepEqual(beforeRelease, [])
    assert.deepEqual(await read, { title: 'batch' })
  } finally { UtilsSQLite.run = original }
}))

test('initialization, a queued read and close run in order without reentrancy', () => withDatabase(async ({ db, platform, lifecycle }) => {
  const entered = deferred(), release = deferred()
  const open = platform.open
  platform.open = async () => { entered.resolve(); await release.promise; return open() }
  const first = db.init('').then(() => null, error => error)
  await entered.promise
  const again = db.init('').then(() => null, error => error)
  const read = db.queryOne('SELECT COUNT(*) AS count FROM local_notes')
  const readResult = read.then(value => value, error => error)
  const closing = db.close()
  await new Promise(resolve => setImmediate(resolve))
  const beforeRelease = [...lifecycle]
  release.resolve()
  const initResults = await Promise.all([first, again, closing])
  assert.deepEqual(beforeRelease, ['create'])
  assert.deepEqual(initResults, [null, null, undefined])
  assert.deepEqual(await readResult, { count: 0 })
  assert.deepEqual(lifecycle, ['create', 'open', 'remove'])
  assert.equal(db.isReady(), false)
  await assert.rejects(db.query('SELECT 1'), /未初始化/)
  await db.init('')
  assert.equal(db.isReady(), true)
}, false))

test('failed rollback blocks all access and refuses to persist an unresolved transaction on close', () => withDatabase(async ({ db, UtilsSQLite, lifecycle }) => {
  const original = UtilsSQLite.rollbackTransaction
  UtilsSQLite.rollbackTransaction = async () => { throw new Error('injected rollback failure') }
  let failure, writeFailure, closeFailure, ready, afterClose
  try {
    failure = await db.runInTransaction([
      { statement: 'UPDATE executor_records SET title=? WHERE id=1', values: ['uncommitted'] },
      { statement: 'INSERT INTO executor_records VALUES(1,?)', values: ['duplicate'] },
    ]).then(() => null, error => error)
    ready = db.isReady()
    writeFailure = await db.run('INSERT INTO executor_records VALUES(2,?)', ['blocked']).then(() => null, error => error)
    closeFailure = await db.close().then(() => null, error => error)
    afterClose = [...lifecycle]
  } finally { UtilsSQLite.rollbackTransaction = original }
  assert.match(String(failure), /rollback|回滚/i)
  assert.equal(ready, false)
  assert.match(String(writeFailure), /关闭.*重新初始化/)
  assert.match(String(closeFailure), /rollback/i)
  assert.deepEqual(afterClose, ['create', 'open'], 'close must not export an unresolved web transaction')
  await assert.rejects(db.query('SELECT * FROM executor_records'), /关闭.*重新初始化/)
  assert.equal(await db.tryLoadVecExtension('test.so'), false)
  await db.close()
  await db.init('')
  assert.deepEqual(await db.query('SELECT * FROM executor_records'), [{ id: 1, title: 'original' }])
}))

test('web transactional writes remain persisted when jeep autoSave is enabled', () => withDatabase(async ({ db, driver, saved, SQL }) => {
  driver.autoSave = true
  await db.runInTransaction([{ statement: 'UPDATE executor_records SET title=? WHERE id=1', values: ['durable'] }])
  const reopened = new SQL.Database(saved())
  try { assert.equal(reopened.exec('SELECT title FROM executor_records')[0].values[0][0], 'durable') }
  finally { reopened.close() }
}))

test('transaction control cannot escape a public call, including query and nested batches', () => withDatabase(async ({ db }) => {
  for (const call of [
    () => db.execute('BEGIN;'),
    () => db.execute("SELECT 'BEGIN; COMMIT'; /* before */ COMMIT;"),
    () => db.execute('BEGIN; UPDATE executor_records SET title=\'manual\'; COMMIT;', true),
    () => db.run('-- manual transaction\nSAVEPOINT nested'),
    () => db.query('/* not a read */ ROLLBACK'),
    () => db.runInTransaction([{ statement: 'RELEASE nested', values: [] }]),
    () => db.execute('END TRANSACTION;'),
  ]) await assert.rejects(call(), /事务控制.*runInTransaction/)
  assert.equal(db.isReady(), true)
  assert.deepEqual(await db.query('SELECT * FROM executor_records'), [{ id: 1, title: 'original' }])
}))

test('migration scripts, comments, quoted keywords and trigger BEGIN/END remain valid', () => withDatabase(async ({ db }) => {
  await db.execute(`
    /* COMMIT; */ CREATE TABLE executor_log("end" TEXT);
    CREATE TEMP TRIGGER executor_audit AFTER UPDATE ON executor_records BEGIN
      INSERT INTO executor_log VALUES (CASE WHEN new.id=1 THEN 'BEGIN; COMMIT;' ELSE 'END' END);
      INSERT INTO executor_log VALUES ('it''s a ROLLBACK;');
    END;
    -- BEGIN;
    INSERT INTO executor_log VALUES ('SAVEPOINT');
  `, true)
  await db.run('UPDATE executor_records SET title=? WHERE id=1', ['migrated'])
  assert.deepEqual(await db.query('SELECT "end" FROM executor_log'), [
    { end: 'SAVEPOINT' }, { end: 'BEGIN; COMMIT;' }, { end: "it's a ROLLBACK;" },
  ])
  await assert.rejects(db.execute('CREATE TRIGGER executor_noop AFTER INSERT ON executor_records BEGIN SELECT 1; END; COMMIT;'), /事务控制/)
}))
