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
    await run({ db, driver, raw, platform, lifecycle, UtilsSQLite, SQLiteDBConnection: definitions.SQLiteDBConnection, saved: () => stored.get('lobster'), SQL })
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

test('an owner activation requested inside a real write rolls back before COMMIT and then publishes', () => withDatabase(async ({ db, platform }) => {
  let active = 'A', activation
  await db.requestOwnerActivation({ userId: 'A', workspaceId: 'work' }, () => { active = 'A' })
  const leased = db.bindOperation(db.captureOperation({ kind: 'local-edit' }))
  const run = platform.run
  platform.run = async options => {
    const result = await run(options)
    activation = db.requestOwnerActivation({ userId: 'B', workspaceId: 'work' }, () => { active = 'B' })
    assert.equal(active, 'A')
    return result
  }
  try {
    await assert.rejects(leased.run('UPDATE executor_records SET title=? WHERE id=1', ['old A']), { name: 'AbortError' })
    await activation
    assert.equal(active, 'B')
    assert.deepEqual(await db.queryOne('SELECT title FROM executor_records'), { title: 'original' })
  } finally { platform.run = run }
}))

test('activation after COMMIT dispatch waits for the real proxy commit and Web persistence', () => withDatabase(async ({ db, platform, driver, raw, SQLiteDBConnection, saved, SQL }) => {
  let active = 'A', activation, published = false
  const events = []
  await db.requestOwnerActivation({ userId: 'A', workspaceId: 'work' }, () => { active = 'A' })
  const leased = db.bindOperation(db.captureOperation({ kind: 'local-edit' }))
  const commitStarted = deferred(), commitRelease = deferred(), saveStarted = deferred(), saveRelease = deferred()
  const dispatch = SQLiteDBConnection.prototype.commitTransaction
  const commit = platform.commitTransaction, save = driver.saveToStore, exec = raw.exec
  SQLiteDBConnection.prototype.commitTransaction = function (...args) {
    queueMicrotask(() => {
      activation = db.requestOwnerActivation({ userId: 'B', workspaceId: 'work' }, () => {
        active = 'B'; published = true; events.push('publish:B')
      })
    })
    return dispatch.apply(this, args)
  }
  platform.commitTransaction = async () => { commitStarted.resolve(); await commitRelease.promise; return commit() }
  raw.exec = function (sql, ...args) {
    if (sql === 'COMMIT TRANSACTION') events.push(`commit:${active}`)
    return exec.call(this, sql, ...args)
  }
  driver.autoSave = true
  driver.saveToStore = async (...args) => { saveStarted.resolve(); await saveRelease.promise; events.push(`save:${active}`); return save.apply(driver, args) }
  const write = leased.run('UPDATE executor_records SET title=? WHERE id=1', ['committed A']).then(() => null, error => error)
  try {
    await commitStarted.promise
    assert.equal(active, 'A')
    assert.equal(published, false)
    assert.throws(() => db.captureOperation({ kind: 'local-edit' }), { name: 'AbortError' })
    commitRelease.resolve()
    await saveStarted.promise
    assert.equal(active, 'A')
    assert.equal(published, false)
    saveRelease.resolve()
    const error = await write
    await activation
    assert.equal(error?.name, 'AbortError')
    assert.equal(error?.outcome, 'committed', 'a committed operation must not be reported as rolled back')
    assert.deepEqual(events, ['commit:A', 'save:A', 'publish:B'])
    const persisted = new SQL.Database(saved())
    try { assert.equal(persisted.exec('SELECT title FROM executor_records')[0].values[0][0], 'committed A') }
    finally { persisted.close() }
  } finally {
    commitRelease.resolve(); saveRelease.resolve(); await write
    SQLiteDBConnection.prototype.commitTransaction = dispatch
    platform.commitTransaction = commit; driver.saveToStore = save; raw.exec = exec
  }
}))

test('queued and late old-owner operations stay revoked across A→B→A', () => withDatabase(async ({ db, platform }) => {
  const identity = userId => ({ userId, workspaceId: 'work' })
  await db.requestOwnerActivation(identity('A'), () => {})
  const lease = db.captureOperation({ kind: 'local-edit' }), old = db.bindOperation(lease)
  let driverRuns = 0
  const run = platform.run
  platform.run = async options => { driverRuns++; return run(options) }
  const queued = old.run('UPDATE executor_records SET title=? WHERE id=1', ['queued A']).then(() => null, error => error)
  const b = db.requestOwnerActivation(identity('B'), () => { throw new Error('superseded B must not publish') }).catch(error => error)
  assert.throws(() => db.captureOperation({ kind: 'local-edit' }), { name: 'AbortError' })
  const a = db.requestOwnerActivation(identity('A'), () => {})
  await a
  assert.equal((await b).name, 'AbortError')
  assert.equal((await queued).name, 'AbortError')
  await assert.rejects(old.run('INSERT INTO executor_records VALUES(2,?)', ['late network response']), { name: 'AbortError' })
  assert.notEqual(db.captureOperation({ kind: 'local-edit' }).owner.epoch, lease.owner.epoch)
  assert.equal(driverRuns, 0)
  await db.run('INSERT INTO executor_records VALUES(2,?)', ['new A'])
  assert.deepEqual(await db.query('SELECT * FROM executor_records ORDER BY id'), [{ id: 1, title: 'original' }, { id: 2, title: 'new A' }])
}))

test('module epochs revoke old cloud operations without cancelling local edits', () => withDatabase(async ({ db }) => {
  await db.requestPolicyActivation('email', 'cloud', () => {})
  const cloud = db.bindOperation(db.captureOperation({ kind: 'cloud-sync', module: 'email' }))
  const local = db.bindOperation(db.captureOperation({ kind: 'local-edit', module: 'email' }))
  const disable = db.requestPolicyActivation('email', 'local', () => { throw new Error('superseded local must not publish') }).catch(error => error)
  assert.throws(() => db.captureOperation({ kind: 'cloud-sync', module: 'email' }), { name: 'AbortError' })
  await db.requestPolicyActivation('email', 'cloud', () => {})
  assert.equal((await disable).name, 'AbortError')
  await assert.rejects(cloud.run('UPDATE executor_records SET title=? WHERE id=1', ['stale cloud']), { name: 'AbortError' })
  await local.run('UPDATE executor_records SET title=? WHERE id=1', ['local edit'])
  await db.requestPolicyActivation('notes', 'local', () => {})
  const freshCloud = db.bindOperation(db.captureOperation({ kind: 'cloud-sync', module: 'email' }))
  await freshCloud.run('INSERT INTO executor_records VALUES(2,?)', ['fresh cloud'])
  assert.deepEqual(await db.query('SELECT * FROM executor_records ORDER BY id'), [{ id: 1, title: 'local edit' }, { id: 2, title: 'fresh cloud' }])
}))

test('a local-policy request inside a real cloud write rolls it back while local edits remain usable', () => withDatabase(async ({ db, platform }) => {
  await db.requestPolicyActivation('email', 'cloud', () => {})
  const cloud = db.bindOperation(db.captureOperation({ kind: 'cloud-sync', module: 'email' }))
  const local = db.bindOperation(db.captureOperation({ kind: 'local-edit', module: 'email' }))
  let mode = 'cloud', activation
  const run = platform.run
  platform.run = async options => {
    const result = await run(options)
    activation = db.requestPolicyActivation('email', 'local', () => { mode = 'local' })
    assert.equal(mode, 'cloud')
    return result
  }
  try { await assert.rejects(cloud.run('UPDATE executor_records SET title=? WHERE id=1', ['revoked cloud']), { name: 'AbortError' }); await activation }
  finally { platform.run = run }
  assert.equal(mode, 'local')
  assert.deepEqual(await local.queryOne('SELECT title FROM executor_records'), { title: 'original' })
  await local.run('INSERT INTO executor_records VALUES(2,?)', ['local remains writable'])
}))

for (const fault of ['rollback', 'persistence']) {
  test(`${fault} failure blocks owner activation until successful cleanup and an explicit retry`, () => withDatabase(async ({ db, platform, driver, UtilsSQLite }) => {
    await db.requestOwnerActivation({ userId: 'A', workspaceId: 'work' }, () => {})
    const old = db.bindOperation(db.captureOperation({ kind: 'local-edit' }))
    let published = false, activation
    const request = () => {
      activation = db.requestOwnerActivation({ userId: 'B', workspaceId: 'work' }, () => { published = true }).then(() => null, error => error)
    }
    const run = platform.run, commit = platform.commitTransaction, rollback = UtilsSQLite.rollbackTransaction, save = driver.saveToStore
    if (fault === 'rollback') {
      platform.run = async options => { const result = await run(options); request(); return result }
      UtilsSQLite.rollbackTransaction = async () => { throw new Error('injected rollback failure') }
    } else {
      platform.commitTransaction = async () => { const result = await commit(); request(); return result }
      driver.autoSave = true
      driver.saveToStore = async () => { throw new Error('injected persistence failure') }
    }
    try {
      await assert.rejects(old.run('UPDATE executor_records SET title=? WHERE id=1', ['old write']))
      assert.match(String(await activation), /状态不确定/)
      assert.equal(published, false)
      assert.equal(db.isReady(), false)
      assert.throws(() => db.captureOperation({ kind: 'local-edit' }), { name: 'AbortError' })
    } finally {
      platform.run = run; platform.commitTransaction = commit
      UtilsSQLite.rollbackTransaction = rollback; driver.saveToStore = save
    }
    await db.close()
    await db.init('')
    assert.equal(published, false, 'failed activation must not be replayed by init')
    await db.requestOwnerActivation({ userId: 'B', workspaceId: 'work' }, () => { published = true })
    assert.equal(published, true)
    await assert.rejects(old.query('SELECT 1'), { name: 'AbortError' })
    if (fault === 'rollback') assert.deepEqual(await db.queryOne('SELECT title FROM executor_records'), { title: 'original' })
    // A failed persistence attempt does not promise durable committed contents.
    else assert.deepEqual(await db.queryOne('SELECT 1 AS ok'), { ok: 1 })
  }))
}

test('failed connection cleanup cannot publish a new owner', () => withDatabase(async ({ db, platform }) => {
  const remove = platform.closeConnection
  platform.closeConnection = async () => { throw new Error('injected cleanup failure') }
  let published = false
  try {
    await assert.rejects(db.close(), /injected cleanup failure/)
    await assert.rejects(db.requestOwnerActivation(null, () => { published = true }), /状态不确定/)
    assert.equal(published, false)
  } finally { platform.closeConnection = remove }
  await db.close()
  await db.requestOwnerActivation(null, () => { published = true })
  assert.equal(published, true)
}))

test('owner activation works before unlock, clears cloud grants and rejects async publishers', () => withDatabase(async ({ db }) => {
  let published = false
  await db.requestOwnerActivation(null, () => { published = true })
  assert.equal(published, true)
  await db.requestPolicyActivation('email', 'cloud', () => {})
  const old = db.bindOperation(db.captureOperation({ kind: 'cloud-sync', module: 'email' }))
  await db.init('')
  await db.requestOwnerActivation({ userId: '', workspaceId: '' }, () => {})
  assert.throws(() => db.captureOperation({ kind: 'cloud-sync', module: 'email' }), { name: 'AbortError' })
  await assert.rejects(old.query('SELECT 1'), { name: 'AbortError' })
  let entered = false
  await assert.rejects(db.requestOwnerActivation(null, async () => { entered = true; await db.query('SELECT 1') }), /synchronous/)
  assert.equal(entered, false)
  assert.throws(() => db.captureOperation({ kind: 'local-edit' }), { name: 'AbortError' })
  await db.requestOwnerActivation(null, () => {})
  const bound = db.bindOperation(db.captureOperation({ kind: 'local-edit' }))
  await db.close(); await db.init('')
  await assert.rejects(bound.query('SELECT 1'), { name: 'AbortError' })
  assert.deepEqual(await db.queryOne('SELECT 1 AS ok'), { ok: 1 })
}, false))

for (const explicitLease of [false, true]) {
  test(`public execute(false) cannot autocommit a revoked write (explicit lease: ${explicitLease})`, () => withDatabase(async ({ db, UtilsSQLite }) => {
    const lease = db.captureOperation({ kind: 'local-edit' })
    const execute = UtilsSQLite.execute
    let activation
    UtilsSQLite.execute = async (...args) => {
      if (args[1].includes('UPDATE executor_records')) {
        activation = db.requestOwnerActivation({ userId: 'B', workspaceId: 'work' }, () => {})
      }
      return execute.apply(UtilsSQLite, args)
    }
    try {
      await assert.rejects(db.execute("UPDATE executor_records SET title='revoked'", false, explicitLease ? lease : undefined), { name: 'AbortError' })
      await activation
      assert.deepEqual(await db.queryOne('SELECT title FROM executor_records'), { title: 'original' })
    } finally { UtilsSQLite.execute = execute }
  }))
}

test('query and queryOne reject write SQL while WITH, EXPLAIN and read PRAGMAs work', () => withDatabase(async ({ db }) => {
  const bound = db.bindOperation(db.captureOperation({ kind: 'local-edit' }))
  for (const sql of [
    "UPDATE executor_records SET title='bad' RETURNING title",
    "WITH picked AS (SELECT 1) UPDATE executor_records SET title='bad' RETURNING title",
    "SELECT 1; UPDATE executor_records SET title='bad' RETURNING title",
    'PRAGMA user_version=7', 'PRAGMA user_version(7)', 'EXPLAIN PRAGMA foreign_keys=OFF',
    "REPLACE INTO executor_records VALUES(1,'bad') RETURNING title",
    "WITH picked AS (SELECT 1) REPLACE INTO executor_records VALUES(1,'bad') RETURNING title",
  ]) {
    await assert.rejects(bound.query(sql), /只读/)
    await assert.rejects(db.queryOne(sql), /只读/)
  }
  assert.deepEqual(await bound.query('WITH picked AS (SELECT title FROM executor_records) SELECT * FROM picked'), [{ title: 'original' }])
  assert.ok((await bound.query('PRAGMA table_info(executor_records)')).some(row => row.name === 'title'))
  assert.deepEqual(await bound.query('PRAGMA user_version'), [{ user_version: 0 }])
  assert.deepEqual(await bound.query("SELECT REPLACE('a','a','b') AS value"), [{ value: 'b' }])
  assert.ok((await bound.query('EXPLAIN UPDATE executor_records SET title=\'explained only\'' )).length > 0)
  assert.deepEqual(await bound.queryOne('SELECT title FROM executor_records'), { title: 'original' })
}))

test('a synchronously reentrant owner publisher preserves the newest ticket and never mints a mixed owner', () => withDatabase(async ({ db }) => {
  let nested, active
  const outer = db.requestOwnerActivation({ userId: 'A', workspaceId: 'work' }, () => {
    active = 'A'
    nested = db.requestOwnerActivation({ userId: 'B', workspaceId: 'work' }, () => { active = 'B' }).then(() => null, error => error)
    assert.throws(() => db.captureOperation({ kind: 'local-edit' }), { name: 'AbortError' })
  }).then(() => null, error => error)
  assert.equal((await outer)?.name, 'AbortError')
  assert.equal(await nested, null)
  const lease = db.captureOperation({ kind: 'local-edit' })
  assert.equal(active, 'B')
  assert.equal(lease.owner.principal.userId, 'B')
  await db.bindOperation(lease).run('INSERT INTO executor_records VALUES(2,?)', ['B'])
}))

test('a synchronously reentrant policy publisher cannot clear a newer local request', () => withDatabase(async ({ db }) => {
  let nested, mode
  const outer = db.requestPolicyActivation('email', 'cloud', () => {
    mode = 'cloud'
    nested = db.requestPolicyActivation('email', 'local', () => { mode = 'local' }).then(() => null, error => error)
  }).then(() => null, error => error)
  assert.equal((await outer)?.name, 'AbortError')
  assert.equal(await nested, null)
  assert.equal(mode, 'local')
  assert.throws(() => db.captureOperation({ kind: 'cloud-sync', module: 'email' }), { name: 'AbortError' })
  await db.bindOperation(db.captureOperation({ kind: 'local-edit' })).run('INSERT INTO executor_records VALUES(2,?)', ['local'])
}))

const completePolicy = overrides => ({ 'ai-chat': 'local', email: 'local', rss: 'local', notes: 'local', meetings: 'local', tasks: 'local', ...overrides })

test('readonly operation checks expose no state and reject revoked policy and owner leases', () => withDatabase(async ({ db }) => {
  await db.requestPolicyActivation('email', 'cloud', () => {})
  const lease = db.captureOperation({ kind: 'cloud-sync', module: 'email' }), bound = db.bindOperation(lease)
  assert.equal(db.assertOperationCurrent(lease), undefined)
  assert.equal(bound.assertCurrent(), undefined)
  const local = db.requestPolicyActivation('email', 'local', () => {})
  assert.throws(() => bound.assertCurrent(), { name: 'AbortError' })
  await local
  const device = db.bindOperation(db.captureOperation({ kind: 'local-edit' }))
  await db.requestOwnerActivation(null, () => {})
  assert.throws(() => device.assertCurrent(), { name: 'AbortError' })
  const current = db.bindOperation(db.captureOperation({ kind: 'local-edit' }))
  await db.close()
  assert.throws(() => current.assertCurrent(), { name: 'AbortError' })
}))

test('a complete policy snapshot synchronously revokes all changed modules and publishes once after queued SQL', () => withDatabase(async ({ db, platform }) => {
  for (const module of ['email', 'notes', 'rss']) await db.requestPolicyActivation(module, 'cloud', () => {})
  const email = db.bindOperation(db.captureOperation({ kind: 'cloud-sync', module: 'email' }))
  const notes = db.bindOperation(db.captureOperation({ kind: 'cloud-sync', module: 'notes' }))
  const rss = db.bindOperation(db.captureOperation({ kind: 'cloud-sync', module: 'rss' }))
  const local = db.bindOperation(db.captureOperation({ kind: 'local-edit' }))
  const entered = deferred(), release = deferred()
  const run = platform.run
  platform.run = async options => { const result = await run(options); entered.resolve(); await release.promise; return result }
  const first = local.run('UPDATE executor_records SET title=? WHERE id=1', ['queued local'])
  await entered.promise
  const input = completePolicy({ rss: 'cloud' }), publications = []
  let activation
  try {
    activation = db.requestPolicySnapshotActivation(input, snapshot => {
      assert.equal(Object.isFrozen(snapshot), true)
      publications.push({ ...snapshot })
    })
    input.notes = 'cloud' // The publisher must receive the validated copy, not caller-owned state.
    for (const module of ['email', 'notes']) assert.throws(() => db.captureOperation({ kind: 'cloud-sync', module }), { name: 'AbortError' })
    db.captureOperation({ kind: 'cloud-sync', module: 'rss' })
    assert.deepEqual(publications, [])
  } finally { release.resolve(); platform.run = run }
  await first; await activation
  assert.deepEqual(publications, [completePolicy({ rss: 'cloud' })])
  await assert.rejects(email.run('UPDATE executor_records SET title=? WHERE id=1', ['old email']), { name: 'AbortError' })
  await assert.rejects(notes.run('UPDATE executor_records SET title=? WHERE id=1', ['old notes']), { name: 'AbortError' })
  await rss.run('INSERT INTO executor_records VALUES(2,?)', ['unaffected rss'])
  await local.run('INSERT INTO executor_records VALUES(3,?)', ['local edit'])
  assert.deepEqual(await db.queryOne('SELECT title FROM executor_records WHERE id=1'), { title: 'queued local' })
}))

test('equal snapshots preserve active leases while a pending A→B→A change never revives an old epoch', () => withDatabase(async ({ db }) => {
  const a = completePolicy({ email: 'cloud', notes: 'cloud' })
  await db.requestPolicySnapshotActivation(a, () => {})
  const original = db.bindOperation(db.captureOperation({ kind: 'cloud-sync', module: 'email' }))
  let calls = 0
  const equalFirst = db.requestPolicySnapshotActivation(a, () => { throw new Error('superseded equal snapshot') }).catch(error => error)
  await db.requestPolicySnapshotActivation({ ...a }, () => { calls++ })
  assert.equal((await equalFirst).name, 'AbortError')
  await original.run('UPDATE executor_records SET title=? WHERE id=1', ['equal snapshot remains valid'])
  const b = db.requestPolicySnapshotActivation(completePolicy(), () => { throw new Error('superseded B must not publish') }).catch(error => error)
  const restored = db.requestPolicySnapshotActivation(a, () => { calls++ })
  for (const module of ['email', 'notes']) assert.throws(() => db.captureOperation({ kind: 'cloud-sync', module }), { name: 'AbortError' })
  await restored
  assert.equal((await b).name, 'AbortError')
  assert.equal(calls, 2)
  await assert.rejects(original.query('SELECT 1'), { name: 'AbortError' })
  for (const module of ['email', 'notes']) {
    const fresh = db.bindOperation(db.captureOperation({ kind: 'cloud-sync', module }))
    assert.deepEqual(await fresh.queryOne('SELECT 1 AS ok'), { ok: 1 })
  }
}))

test('a complete snapshot supersedes prior partial tickets and rejects partial takeover until it settles', () => withDatabase(async ({ db }) => {
  await db.requestPolicyActivation('email', 'cloud', () => {})
  const original = db.bindOperation(db.captureOperation({ kind: 'cloud-sync', module: 'email' }))
  const partial = db.requestPolicyActivation('email', 'local', () => { throw new Error('superseded partial must not publish') }).catch(error => error)
  const publications = []
  const whole = db.requestPolicySnapshotActivation(completePolicy({ email: 'cloud', notes: 'cloud' }), snapshot => { publications.push({ ...snapshot }) })
  await assert.rejects(db.requestPolicyActivation('email', 'local', () => { throw new Error('partial takeover') }), { name: 'AbortError' })
  await whole
  assert.equal((await partial).name, 'AbortError')
  assert.equal(publications.length, 1)
  await assert.rejects(original.query('SELECT 1'), { name: 'AbortError' })
  await db.requestPolicyActivation('email', 'local', () => {})
  assert.throws(() => db.captureOperation({ kind: 'cloud-sync', module: 'email' }), { name: 'AbortError' })
  db.captureOperation({ kind: 'cloud-sync', module: 'notes' })
}))

test('snapshot publisher reentry confirms either all newer modules or none of the superseded snapshot', () => withDatabase(async ({ db }) => {
  const start = completePolicy({ email: 'cloud', notes: 'cloud' })
  await db.requestPolicySnapshotActivation(start, () => {})
  const old = db.bindOperation(db.captureOperation({ kind: 'cloud-sync', module: 'email' }))
  const publications = []
  let nested, partial
  const outer = db.requestPolicySnapshotActivation(completePolicy(), snapshot => {
    publications.push({ ...snapshot })
    partial = db.requestPolicyActivation('notes', 'cloud', () => { throw new Error('partial reentry') }).catch(error => error)
    nested = db.requestPolicySnapshotActivation(start, next => { publications.push({ ...next }) }).then(() => null, error => error)
    for (const module of ['email', 'notes']) assert.throws(() => db.captureOperation({ kind: 'cloud-sync', module }), { name: 'AbortError' })
  }).then(() => null, error => error)
  assert.equal((await outer).name, 'AbortError')
  assert.equal((await partial).name, 'AbortError')
  assert.equal(await nested, null)
  assert.deepEqual(publications, [completePolicy(), start])
  for (const module of ['email', 'notes']) db.captureOperation({ kind: 'cloud-sync', module })
  await assert.rejects(old.query('SELECT 1'), { name: 'AbortError' })
}))

test('owner A→B→A cancels the whole old snapshot and a publisher owner switch cannot partially confirm it', () => withDatabase(async ({ db }) => {
  const identity = userId => ({ userId, workspaceId: 'work' })
  const cloud = completePolicy({ email: 'cloud', notes: 'cloud' })
  await db.requestOwnerActivation(identity('A'), () => {})
  await db.requestPolicySnapshotActivation(cloud, () => {})
  const old = db.bindOperation(db.captureOperation({ kind: 'cloud-sync', module: 'email' }))
  const stale = db.requestPolicySnapshotActivation(completePolicy(), () => { throw new Error('old A snapshot published') }).catch(error => error)
  const b = db.requestOwnerActivation(identity('B'), () => {}).catch(error => error)
  await db.requestOwnerActivation(identity('A'), () => {})
  assert.equal((await b).name, 'AbortError'); assert.equal((await stale).name, 'AbortError')
  await assert.rejects(old.query('SELECT 1'), { name: 'AbortError' })
  await db.requestPolicySnapshotActivation(cloud, () => {})
  let owner
  const interrupted = db.requestPolicySnapshotActivation(completePolicy(), () => {
    owner = db.requestOwnerActivation(identity('B'), () => {}).then(() => null, error => error)
  }).then(() => null, error => error)
  assert.equal((await interrupted).name, 'AbortError'); assert.equal(await owner, null)
  for (const module of ['email', 'notes']) assert.throws(() => db.captureOperation({ kind: 'cloud-sync', module }), { name: 'AbortError' })
  // Successful owner activation must also release the previous snapshot's partial-API lock.
  await db.requestPolicyActivation('notes', 'cloud', () => {})
  db.captureOperation({ kind: 'cloud-sync', module: 'notes' })
}))

test('invalid snapshots change no admission and throwing publishers keep every changed module closed until explicit retry', () => withDatabase(async ({ db }) => {
  const cloud = completePolicy({ email: 'cloud', notes: 'cloud', rss: 'cloud' })
  await db.requestPolicySnapshotActivation(cloud, () => {})
  const original = db.bindOperation(db.captureOperation({ kind: 'cloud-sync', module: 'email' }))
  const missing = { ...cloud }; delete missing.notes
  for (const invalid of [missing, { ...cloud, email: 'invalid' }, { ...cloud, extra: 'local' }, null]) {
    await assert.rejects(db.requestPolicySnapshotActivation(invalid, () => { throw new Error('invalid publication') }), /complete policy snapshot/)
    assert.deepEqual(await original.queryOne('SELECT 1 AS ok'), { ok: 1 })
  }
  await assert.rejects(db.requestPolicySnapshotActivation(completePolicy({ rss: 'cloud' }), () => { throw new Error('publisher failed') }), /publisher failed/)
  for (const module of ['email', 'notes']) assert.throws(() => db.captureOperation({ kind: 'cloud-sync', module }), { name: 'AbortError' })
  db.captureOperation({ kind: 'cloud-sync', module: 'rss' })
  await assert.rejects(db.requestPolicyActivation('email', 'cloud', () => {}), { name: 'AbortError' })
  await db.requestPolicySnapshotActivation(cloud, () => {})
  await assert.rejects(original.query('SELECT 1'), { name: 'AbortError' })
  for (const module of ['email', 'notes']) db.captureOperation({ kind: 'cloud-sync', module })
}))

test('a snapshot requested inside a real cloud write rolls back before publishing all changed modules', () => withDatabase(async ({ db, platform }) => {
  await db.requestPolicySnapshotActivation(completePolicy({ email: 'cloud', notes: 'cloud' }), () => {})
  const email = db.bindOperation(db.captureOperation({ kind: 'cloud-sync', module: 'email' }))
  const run = platform.run
  let activation, published = false
  platform.run = async options => {
    const result = await run(options)
    activation = db.requestPolicySnapshotActivation(completePolicy(), () => { published = true })
    for (const module of ['email', 'notes']) assert.throws(() => db.captureOperation({ kind: 'cloud-sync', module }), { name: 'AbortError' })
    assert.equal(published, false)
    return result
  }
  try { await assert.rejects(email.run('UPDATE executor_records SET title=? WHERE id=1', ['revoked email']), { name: 'AbortError' }); await activation }
  finally { platform.run = run }
  assert.equal(published, true)
  assert.deepEqual(await db.queryOne('SELECT title FROM executor_records'), { title: 'original' })
}))

test('a complete snapshot waits through dispatched COMMIT and real Web save before its single publication', () => withDatabase(async ({ db, platform, driver, raw, SQLiteDBConnection }) => {
  let active = completePolicy({ email: 'cloud', notes: 'cloud' }), activation
  await db.requestPolicySnapshotActivation(active, () => {})
  const email = db.bindOperation(db.captureOperation({ kind: 'cloud-sync', module: 'email' }))
  const commitStarted = deferred(), releaseCommit = deferred(), saveStarted = deferred(), releaseSave = deferred(), events = []
  const dispatch = SQLiteDBConnection.prototype.commitTransaction, commit = platform.commitTransaction, save = driver.saveToStore, exec = raw.exec
  SQLiteDBConnection.prototype.commitTransaction = function (...args) {
    queueMicrotask(() => { activation = db.requestPolicySnapshotActivation(completePolicy(), snapshot => { active = snapshot; events.push('publish:local/local') }) })
    return dispatch.apply(this, args)
  }
  platform.commitTransaction = async () => { commitStarted.resolve(); await releaseCommit.promise; return commit() }
  raw.exec = function (sql, ...args) { if (sql === 'COMMIT TRANSACTION') events.push(`commit:${active.email}/${active.notes}`); return exec.call(this, sql, ...args) }
  driver.autoSave = true
  driver.saveToStore = async (...args) => { saveStarted.resolve(); await releaseSave.promise; events.push(`save:${active.email}/${active.notes}`); return save.apply(driver, args) }
  const result = email.run('UPDATE executor_records SET title=? WHERE id=1', ['committed before snapshot']).then(() => null, error => error)
  try {
    await commitStarted.promise
    assert.equal(active.email, 'cloud'); assert.equal(active.notes, 'cloud')
    for (const module of ['email', 'notes']) assert.throws(() => db.captureOperation({ kind: 'cloud-sync', module }), { name: 'AbortError' })
    releaseCommit.resolve(); await saveStarted.promise
    assert.equal(active.email, 'cloud'); assert.equal(active.notes, 'cloud')
    releaseSave.resolve()
    assert.equal((await result).outcome, 'committed'); await activation
    assert.deepEqual(events, ['commit:cloud/cloud', 'save:cloud/cloud', 'publish:local/local'])
  } finally {
    releaseCommit.resolve(); releaseSave.resolve(); await result
    SQLiteDBConnection.prototype.commitTransaction = dispatch; platform.commitTransaction = commit; driver.saveToStore = save; raw.exec = exec
  }
}))

for (const fault of ['rollback', 'persistence']) {
  test(`snapshot activation stays wholly closed after ${fault} failure and never replays on cleanup`, () => withDatabase(async ({ db, platform, driver, UtilsSQLite }) => {
    const cloud = completePolicy({ email: 'cloud', notes: 'cloud' })
    await db.requestPolicySnapshotActivation(cloud, () => {})
    const old = db.bindOperation(db.captureOperation({ kind: 'cloud-sync', module: 'email' }))
    const run = platform.run, commit = platform.commitTransaction, rollback = UtilsSQLite.rollbackTransaction, save = driver.saveToStore
    let activation, publications = 0
    const request = () => { activation = db.requestPolicySnapshotActivation(completePolicy(), () => { publications++ }).then(() => null, error => error) }
    if (fault === 'rollback') {
      platform.run = async options => { const result = await run(options); request(); return result }
      UtilsSQLite.rollbackTransaction = async () => { throw new Error('snapshot rollback failure') }
    } else {
      platform.commitTransaction = async () => { const result = await commit(); request(); return result }
      driver.autoSave = true; driver.saveToStore = async () => { throw new Error('snapshot persistence failure') }
    }
    try {
      await assert.rejects(old.run('UPDATE executor_records SET title=? WHERE id=1', ['uncertain snapshot']))
      assert.match(String(await activation), /状态不确定/)
      assert.equal(publications, 0)
      for (const module of ['email', 'notes']) assert.throws(() => db.captureOperation({ kind: 'cloud-sync', module }), { name: 'AbortError' })
    } finally { platform.run = run; platform.commitTransaction = commit; UtilsSQLite.rollbackTransaction = rollback; driver.saveToStore = save }
    await db.close(); await db.init('')
    assert.equal(publications, 0)
    await db.requestPolicySnapshotActivation(cloud, () => { publications++ })
    assert.equal(publications, 1)
    await assert.rejects(old.query('SELECT 1'), { name: 'AbortError' })
    for (const module of ['email', 'notes']) db.captureOperation({ kind: 'cloud-sync', module })
  }))
}
