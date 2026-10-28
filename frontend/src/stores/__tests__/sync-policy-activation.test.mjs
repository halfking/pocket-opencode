import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createServer as createHttpServer } from 'node:http'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import initSqlJs from 'sql.js'
import { createPinia, setActivePinia } from 'pinia'

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
async function fixture(run) {
  const stored=new Map(),values=new Map()
  const storage={keys:()=>values.keys(),getItem:k=>values.get(k)??null,setItem:(k,v)=>values.set(k,v),removeItem:k=>values.delete(k)}
  const savedGlobals=new Map(['localStorage','window'].map(k=>[k,Object.getOwnPropertyDescriptor(globalThis,k)]))
  Object.defineProperty(globalThis,'localStorage',{configurable:true,value:storage})
  const browser=new EventTarget();browser.location={origin:'https://policy-fixture.test'}
  Object.defineProperty(globalThis,'window',{configurable:true,value:browser})
  setActivePinia(createPinia())
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
    const driverStore = { getItem: async k => stored.get(k), setItem: async (k, v) => stored.set(k, v), removeItem: async k => stored.delete(k) }
    raw = new SQL.Database(stored.get('lobster'))
    const { Database } = await vite.ssrLoadModule('/node_modules/jeep-sqlite/dist/collection/utils/database.js')
    const { UtilsSQLite } = await vite.ssrLoadModule('/node_modules/jeep-sqlite/dist/collection/utils/utils-sqlite.js')
    const definitions = await vite.ssrLoadModule('/node_modules/@capacitor-community/sqlite/dist/esm/definitions.js')
    const { registerPlugin } = await vite.ssrLoadModule('/node_modules/@capacitor/core/dist/index.js')
    const driver = new Database('lobster', 1, {}, driverStore, false, '')
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
    if (true) {
      await db.init('')
      await db.execute('CREATE TABLE executor_records (id INTEGER PRIMARY KEY, title TEXT); INSERT INTO executor_records VALUES(1,\'original\');')
    }
    const authModule=await vite.ssrLoadModule('/src/stores/auth.ts')
    const auth=authModule.useAuthStore()
    await auth.setAuthWithWorkspace('token-A','User A','user-A','work-A')
    const policy=await vite.ssrLoadModule('/src/stores/syncPolicyStore.ts')
    policy.installSyncPolicyStorage(storage)
    const scope=()=>JSON.stringify([auth.user,auth.workspaceId])
    policy.installSyncPolicyScopeResolver(scope)
    policy.installSyncPolicyActivationHost?.({
      captureContext(){
        if(auth.activationStatus!=='ready')return null
        const session=authModule.captureAuthSession(auth),version=auth.activationVersion,captured=scope()
        return {scope:captured,intentScope:auth.userId?JSON.stringify([auth.userId,auth.workspaceId]):'',workspace:auth.workspaceId,isCurrent:()=>auth.activationVersion===version&&scope()===captured&&authModule.isCurrentAuthSession(auth,session)}
      },activate:(m,p)=>db.requestPolicySnapshotActivation(m,p),
    })
    const store=policy.useSyncPolicyStore()
    await run({db,driver,raw,platform,UtilsSQLite,auth,policy,store,storage,values,browser,vite})
  } finally {
    if (db) await db.close().catch(() => {})
    try { raw?.close() } catch { /* already closed by the real driver */ }
    delete globalThis[key]
    await vite.close()
    for(const[k,d]of savedGlobals){if(d)Object.defineProperty(globalThis,k,d);else delete globalThis[k]}
  }
}


test('local request closes admission immediately but publishes state and cache only after real rollback',()=>fixture(async f=>{
  const cloud={system_default:'cloud',allow_override:true,modules:{email:'cloud',notes:'cloud',rss:'cloud',meetings:'cloud','ai-chat':'cloud',tasks:'cloud'}}
  await f.store.applyPolicy(cloud,1)
  await f.db.requestPolicySnapshotActivation(f.store.modules,()=>{})
  const bound=f.db.bindOperation(f.db.captureOperation({kind:'cloud-sync',module:'notes'}))
  const entered=deferred(),release=deferred(),run=f.platform.run
  f.platform.run=async o=>{const r=await run(o);entered.resolve();await release.promise;return r}
  const write=bound.run('UPDATE executor_records SET title=? WHERE id=1',['old cloud']).then(()=>null,e=>e)
  await entered.promise
  let pending,observed
  try{
    pending=f.store.applyPolicy({...cloud,modules:{...cloud.modules,notes:'local'}},2)
    observed={mode:f.store.modules.notes,cached:f.policy.readCachedSyncPolicy()?.policy.modules.notes,admission:f.policy.readCachedModuleMode('notes')}
  }finally{release.resolve();f.platform.run=run}
  const error=await write;await pending
  assert.deepEqual(observed,{mode:'cloud',cached:'cloud',admission:'local'})
  assert.equal(error?.name,'AbortError')
  assert.deepEqual(await f.db.queryOne('SELECT title FROM executor_records'),{title:'original'})
  assert.equal(f.store.modules.notes,'local')
  assert.equal(f.policy.readCachedModuleMode('notes'),'local')
}))

const cloudPolicy = () => ({ system_default: 'cloud', allow_override: true, modules: { email: 'cloud', notes: 'cloud', rss: 'cloud', meetings: 'cloud', 'ai-chat': 'cloud', tasks: 'cloud' } })
const localNotes = () => ({ ...cloudPolicy(), modules: { ...cloudPolicy().modules, notes: 'local' } })
const tick = () => new Promise(resolve => setImmediate(resolve))
async function activateCloud(f) { await f.store.applyPolicy(cloudPolicy(), 1, false) }

test('a dispatched COMMIT and web persistence finish before the effective policy is published', () => fixture(async f => {
  await activateCloud(f)
  const bound = f.db.bindOperation(f.db.captureOperation({ kind: 'cloud-sync', module: 'notes' }))
  const commitEntered = deferred(), releaseCommit = deferred(), saveEntered = deferred(), releaseSave = deferred()
  const commit = f.platform.commitTransaction, save = f.driver.saveToStore
  f.driver.autoSave = true
  f.platform.commitTransaction = async () => { const result = await commit(); commitEntered.resolve(); await releaseCommit.promise; return result }
  f.driver.saveToStore = async () => { saveEntered.resolve(); await releaseSave.promise; return save.call(f.driver) }
  const write = bound.run('UPDATE executor_records SET title=? WHERE id=1', ['committed']).then(() => null, error => error)
  await commitEntered.promise
  const activation = f.store.applyPolicy(localNotes(), 2)
  assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
  assert.equal(f.store.modules.notes, 'cloud')
  releaseCommit.resolve()
  await saveEntered.promise
  assert.equal(f.store.modules.notes, 'cloud')
  assert.equal(f.policy.readCachedSyncPolicy().lastFetchedAt, 1)
  releaseSave.resolve()
  const error = await write
  assert.equal(error.name, 'AbortError')
  assert.equal(error.outcome, 'committed')
  assert.equal(await activation, true)
  assert.equal(f.store.modules.notes, 'local')
  assert.deepEqual(await f.db.queryOne('SELECT title FROM executor_records'), { title: 'committed' })
}))

test('cache is a candidate: raw cloud disk cannot authorize before activation or while identity is pending', () => fixture(async f => {
  f.storage.setItem(f.policy.SYNC_POLICY_CACHE_KEY, JSON.stringify({ scope: JSON.stringify([f.auth.user, f.auth.workspaceId]), policy: cloudPolicy(), lastFetchedAt: 99 }))
  assert.equal(f.policy.readCachedModuleMode('email'), 'local')
  assert.equal(await f.store.restoreFromCache(), true)
  assert.equal(f.policy.readCachedModuleMode('email'), 'cloud')
  f.auth.activationStatus = 'pending'
  assert.equal(f.policy.readCachedModuleMode('email'), 'local')
  assert.equal(await f.store.restoreFromCache(), false)
}))

test('same-value snapshots preserve unchanged cloud leases while changed modules revoke synchronously', () => fixture(async f => {
  await activateCloud(f)
  const unchanged = f.db.captureOperation({ kind: 'cloud-sync', module: 'email' })
  const changed = f.db.captureOperation({ kind: 'cloud-sync', module: 'notes' })
  const pending = f.store.applyPolicy(localNotes(), 2)
  assert.equal(f.policy.readCachedModuleMode('email'), 'cloud')
  f.db.assertOperationCurrent(unchanged)
  assert.throws(() => f.db.assertOperationCurrent(changed), { name: 'AbortError' })
  await pending
  await f.store.applyPolicy(localNotes(), 3)
  f.db.assertOperationCurrent(unchanged)
}))

test('web persistence failure keeps revoked epochs closed and cannot be undone by rereading a cloud cache', () => fixture(async f => {
  await activateCloud(f)
  const bound = f.db.bindOperation(f.db.captureOperation({ kind: 'cloud-sync', module: 'notes' }))
  const entered = deferred(), release = deferred(), commit = f.platform.commitTransaction
  f.platform.commitTransaction = async () => { const result = await commit(); entered.resolve(); await release.promise; return result }
  f.driver.autoSave = true
  f.driver.saveToStore = async () => { throw new Error('persistence unavailable') }
  const write = bound.run('UPDATE executor_records SET title=? WHERE id=1', ['committed']).then(() => null, error => error)
  await entered.promise
  const activation = f.store.applyPolicy(localNotes(), 2).then(() => null, error => error)
  release.resolve()
  assert.ok(await write)
  assert.match((await activation).message, /persist|blocked|quarantin|状态不确定/i)
  assert.equal(f.store.activationReady, false)
  assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
  await assert.rejects(() => f.store.restoreFromCache())
  assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
}))

test('quota failures retain the confirmed snapshot in memory without reopening pending permissions', () => fixture(async f => {
  await activateCloud(f)
  f.storage.setItem = () => { throw new Error('quota') }
  assert.equal(f.policy.readCachedModuleMode('email'), 'cloud')
  await f.store.applyPolicy(localNotes(), 2)
  assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
  assert.equal(f.policy.readCachedSyncPolicy().lastFetchedAt, 2)
}))

test('latest GET wins; a PUT revision invalidates earlier GET and reset invalidates late PUT across A→B→A', () => fixture(async f => {
  await activateCloud(f)
  const first = deferred(), second = deferred()
  let gets = 0
  f.policy.syncPolicyTransport.fetchPolicy = () => ++gets === 1 ? first.promise : second.promise
  const a = f.store.load(), b = f.store.load()
  second.resolve(localNotes()); await b
  first.resolve(cloudPolicy()); await a
  assert.equal(f.store.modules.notes, 'local')
  await activateCloud(f)
  const response = deferred()
  f.policy.syncPolicyTransport.putModules = () => response.promise
  const old = f.store.setModule('notes', 'local')
  await f.auth.setAuthWithWorkspace('token-B', 'User B', 'user-B', 'work-B')
  await f.store.$reset()
  await f.auth.setAuthWithWorkspace('token-A2', 'User A', 'user-A', 'work-A')
  await f.store.$reset()
  await f.store.applyPolicy(localNotes(), 7, false)
  response.resolve(cloudPolicy())
  assert.equal(await old, false)
  assert.equal(f.store.modules.notes, 'local')
  assert.equal(f.store.lastFetchedAt, 7)
}))

test('failed PUT keeps local intent and cannot resurrect a revoked lease', () => fixture(async f => {
  await activateCloud(f)
  const old = f.db.captureOperation({ kind: 'cloud-sync', module: 'notes' })
  f.policy.syncPolicyTransport.putModules = async () => { throw new Error('PUT offline') }
  await assert.rejects(() => f.store.setModule('notes', 'local'), /PUT offline/)
  assert.equal(f.store.modules.notes, 'local')
  assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
  assert.deepEqual(f.store.pendingLocal, ['notes'])
  assert.match(f.store.saveError, /PUT offline/)
  assert.throws(() => f.db.assertOperationCurrent(old), { name: 'AbortError' })
  assert.throws(() => f.db.captureOperation({ kind: 'cloud-sync', module: 'notes' }), { name: 'AbortError' })
}))

test('server override returns false so a refused local switch cannot authorize a purge', () => fixture(async f => {
  await activateCloud(f)
  f.policy.syncPolicyTransport.putModules = async () => cloudPolicy()
  assert.equal(await f.store.setModule('notes', 'local'), false)
  assert.equal(f.store.modules.notes, 'local')
}))

test('synchronous effective-state reentry supersedes the publisher and cannot grant its cloud snapshot', () => fixture(async f => {
  const { watch } = await import('vue')
  let newer, triggered = false
  const stop = watch(() => f.store.modules, () => {
    if (triggered) return
    triggered = true
    newer = f.store.applyPolicy(localNotes(), 3, false)
  }, { flush: 'sync' })
  assert.equal(await f.store.applyPolicy(cloudPolicy(), 2, false), false)
  await newer
  stop()
  assert.equal(f.store.lastFetchedAt, 3)
  assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
  assert.throws(() => f.db.captureOperation({ kind: 'cloud-sync', module: 'notes' }), { name: 'AbortError' })
}))

test('synchronous confirmation reentry keeps the newer request pending and denied', () => fixture(async f => {
  const { watch } = await import('vue')
  let newer, triggered = false
  const stop = watch(() => f.store.activationReady, ready => {
    if (!ready || triggered) return
    triggered = true
    newer = f.store.applyPolicy(localNotes(), 3, false)
    assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
  }, { flush: 'sync' })
  assert.equal(await f.store.applyPolicy(cloudPolicy(), 2, false), false)
  await newer
  stop()
  assert.equal(f.store.activationReady, true)
  assert.equal(f.store.activating, false)
  assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
}))

test('production runtime waits for owner readiness, restores a full candidate, and starts effects only after confirmation', () => fixture(async f => {
  const { startSyncPolicyRuntime } = await f.vite.ssrLoadModule('/src/stores/sync-policy-runtime.ts')
  const effects = []
  f.policy.syncPolicyTransport.fetchPolicy = async () => cloudPolicy()
  const stop = startSyncPolicyRuntime(f.auth, f.store, { startRuntimes: () => effects.push('start'), connect: () => effects.push('connect'), disconnect: () => effects.push('disconnect') })
  try {
    for (let i = 0; i < 10; i++) await tick()
    assert.equal(f.store.activationReady, true)
    assert.equal(effects.filter(e => e === 'start').length, 1)
    await activateCloud(f)
    const entered = deferred(), release = deferred(), run = f.platform.run
    f.platform.run = async o => { const result = await run(o); entered.resolve(); await release.promise; return result }
    const write = f.db.bindOperation(f.db.captureOperation({ kind: 'local-edit' })).run('UPDATE executor_records SET title=? WHERE id=1', ['old']).then(() => null, error => error)
    await entered.promise
    const owner = f.auth.setAuthWithWorkspace('token-B', 'User B', 'user-B', 'work-B')
    assert.equal(f.auth.activationStatus, 'pending')
    assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
    assert.equal(f.store.activationReady, false)
    release.resolve()
    assert.equal((await write).name, 'AbortError')
    await owner
    for (let i = 0; i < 10; i++) await tick()
    assert.equal(f.store.activationReady, true)
    assert.equal(f.policy.readCachedModuleMode('notes'), 'cloud')
    assert.equal(effects.filter(e => e === 'start').length, 1)
    assert.equal(f.db.captureOperation({ kind: 'cloud-sync', module: 'notes' }).owner.principal.userId, 'user-B')
  } finally { stop() }
}))

async function failLocal(f, module = 'notes') {
  f.policy.syncPolicyTransport.putModules = async () => { throw new Error('offline') }
  await assert.rejects(() => f.store.setModule(module, 'local'), /offline/)
}
const intentKeys = f => [...f.values.keys()].filter(key => key.startsWith(f.policy.SYNC_POLICY_LOCAL_INTENTS_KEY + ':'))

test('failed local save survives GET, explicit retry, and a real cold auth restoration without userId', () => fixture(async f => {
  await activateCloud(f)
  await failLocal(f)
  const keys = intentKeys(f)
  assert.equal(keys.length, 1)
  assert.equal(f.policy.readCachedModuleMode('email'), 'cloud', 'other modules keep their effective permission')
  f.policy.syncPolicyTransport.fetchPolicy = async () => cloudPolicy()
  await f.store.load()
  assert.equal(f.store.modules.notes, 'local')
  await f.auth.syncFromStorage(true)
  assert.equal(f.auth.userId, '', 'the actual cold storage contract intentionally omits stable userId')
  await f.store.$reset()
  await f.store.load()
  assert.equal(f.store.modules.notes, 'local')
  assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
  assert.match(f.store.saveError, /waiting/)
  f.policy.syncPolicyTransport.putModules = async modules => ({ ...cloudPolicy(), modules })
  assert.equal(await f.store.retrySave(), false, 'broad refusal metadata cannot authorize clearing a known identity intent')
  await f.auth.setAuthWithWorkspace('token-A-again', 'User A', 'user-A', 'work-A')
  await f.store.$reset()
  assert.equal(await f.store.retrySave(), true)
  assert.deepEqual(f.store.pendingLocal, [])
  assert.equal(intentKeys(f).length, 0)
  assert.equal(f.store.saveError, '')
}))

test('A and B pending local choices use independent keys and A→B→A cannot erase either', () => fixture(async f => {
  await activateCloud(f)
  await failLocal(f)
  const original = intentKeys(f)[0]
  await f.auth.setAuthWithWorkspace('token-B', 'User B', 'user-B', 'work-B')
  await f.store.$reset()
  await activateCloud(f)
  await failLocal(f, 'email')
  assert.equal(intentKeys(f).length, 2)
  f.policy.syncPolicyTransport.putModules = async modules => ({ ...cloudPolicy(), modules })
  assert.equal(await f.store.retrySave(), true)
  assert.equal(intentKeys(f).length, 1)
  assert.ok(f.values.has(original))
  await f.auth.setAuthWithWorkspace('token-A2', 'User A', 'user-A', 'work-A')
  await f.store.$reset()
  f.policy.syncPolicyTransport.fetchPolicy = async () => cloudPolicy()
  await f.store.load()
  assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
  assert.equal(f.store.pendingLocal.includes('notes'), true)
}))

test('an older successful response cannot clear a newer immutable local intent from another tab', () => fixture(async f => {
  await activateCloud(f)
  const response = deferred()
  f.policy.syncPolicyTransport.putModules = () => response.promise
  const saving = f.store.setModule('notes', 'local')
  const first = intentKeys(f)[0], newer = first + ':newer-tab'
  f.values.set(newer, f.values.get(first))
  response.resolve(localNotes())
  assert.equal(await saving, false)
  assert.equal(f.values.has(first), false)
  assert.equal(f.values.has(newer), true)
  assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
  assert.deepEqual(f.store.pendingLocal, ['notes'])
  f.policy.syncPolicyTransport.putModules = async () => cloudPolicy()
  assert.equal(await f.store.setModule('notes', 'cloud'), true, 'only explicit cloud selection may clear the remaining captured refusal')
  assert.equal(f.policy.readCachedModuleMode('notes'), 'cloud')
}))

test('a new cross-tab local choice during cloud confirmation prevents the old response from reopening admission', () => fixture(async f => {
  await activateCloud(f)
  await failLocal(f)
  const first = intentKeys(f)[0], original = f.values.get(first), response = deferred()
  f.policy.syncPolicyTransport.putModules = () => response.promise
  const cloud = f.store.setModule('notes', 'cloud')
  const newer = first + ':concurrent-local'
  f.values.set(newer, original)
  response.resolve(cloudPolicy())
  assert.equal(await cloud, false)
  assert.equal(f.values.has(newer), true)
  assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
  assert.equal(f.store.modules.notes, 'local')
}))

for (const fault of ['corrupt', 'unreadable', 'limit']) {
  test(`intent ${fault} is visible and conservative; it is never treated as no pending refusal`, () => fixture(async f => {
    await activateCloud(f)
    const key = f.policy.SYNC_POLICY_LOCAL_INTENTS_KEY + ':invalid'
    if (fault === 'corrupt') f.values.set(key, '{broken')
    if (fault === 'unreadable') {
      const get = f.storage.getItem
      f.storage.getItem = k => { if (k.startsWith(f.policy.SYNC_POLICY_LOCAL_INTENTS_KEY)) throw new Error('storage denied'); return get(k) }
      f.values.set(key, '{}')
    }
    if (fault === 'limit') for (let i = 0; i < 4097; i++) f.values.set('unrelated-' + i, '1')
    await f.store.$reset()
    assert.ok(f.store.saveError)
    assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
    f.policy.syncPolicyTransport.fetchPolicy = async () => cloudPolicy()
    await f.store.load()
    assert.equal(f.policy.readCachedModuleMode('email'), 'local')
    assert.equal(f.store.modules.notes, 'local')
  }))
}

test('unwritable storage at startup restores only local modes and rejects fresh cloud grants', () => fixture(async f => {
  await activateCloud(f)
  const set = f.storage.setItem, remove = f.storage.removeItem
  f.storage.setItem = () => { throw new Error('quota') }
  f.storage.removeItem = () => { throw new Error('denied') }
  await f.store.$reset()
  assert.equal(f.store.modules.notes, 'local')
  assert.ok(f.store.saveError)
  assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
  f.policy.syncPolicyTransport.fetchPolicy = async () => cloudPolicy()
  await f.store.load()
  assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
  f.storage.setItem = set; f.storage.removeItem = remove
  await f.store.$reset()
  await f.store.load()
  assert.equal(f.policy.readCachedModuleMode('notes'), 'cloud')
}))

test('unknown B in A workspace may broaden refusal but can never clear A stable identity intent', () => fixture(async f => {
  await activateCloud(f)
  await failLocal(f)
  const original = intentKeys(f)[0]
  await f.auth.setAuthWithWorkspace('B', 'User B', 'user-B', 'work-A')
  await f.auth.syncFromStorage(true)
  assert.equal(f.auth.userId, '')
  await f.store.$reset()
  f.policy.syncPolicyTransport.putModules = async () => cloudPolicy()
  assert.equal(await f.store.setModule('notes', 'cloud'), false)
  assert.ok(f.values.has(original))
  assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
  await f.auth.setAuthWithWorkspace('A2', 'User A renamed', 'user-A', 'work-A')
  await f.store.$reset()
  f.policy.syncPolicyTransport.fetchPolicy = async () => cloudPolicy()
  await f.store.load()
  assert.equal(f.store.modules.notes, 'local', 'display name must not change the stable refusal scope')
}))

test('storage intent event synchronously revokes this instance and notifies consumers; clear cannot open cloud', () => fixture(async f => {
  const { startSyncPolicyRuntime } = await f.vite.ssrLoadModule('/src/stores/sync-policy-runtime.ts')
  f.policy.syncPolicyTransport.fetchPolicy = async () => cloudPolicy()
  const stop = startSyncPolicyRuntime(f.auth, f.store, { startRuntimes() {}, connect() {}, disconnect() {} })
  try {
    for (let i = 0; i < 10; i++) await tick()
    const old = f.db.captureOperation({ kind: 'cloud-sync', module: 'notes' })
    const key = f.policy.SYNC_POLICY_LOCAL_INTENTS_KEY + ':another-tab:notes:1'
    f.values.set(key, JSON.stringify({ version: 1, scope: JSON.stringify([f.auth.userId, f.auth.workspaceId]), workspace: f.auth.workspaceId, module: 'notes' }))
    let notifications = 0
    const changed = () => { notifications++; assert.equal(f.policy.readCachedModuleMode('notes'), 'local') }
    f.browser.addEventListener('openpocket:sync-policy-changed', changed)
    f.browser.dispatchEvent(Object.assign(new Event('storage'), { key }))
    assert.throws(() => f.db.assertOperationCurrent(old), { name: 'AbortError' })
    assert.ok(notifications > 0)
    for (let i = 0; i < 10; i++) await tick()
    assert.equal(f.store.modules.notes, 'local')
    f.values.delete(key)
    f.browser.dispatchEvent(Object.assign(new Event('storage'), { key }))
    for (let i = 0; i < 10; i++) await tick()
    assert.equal(f.store.modules.notes, 'local')
    assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
    f.browser.removeEventListener('openpocket:sync-policy-changed', changed)
  } finally { stop() }
}))

test('repeated offline retries rotate durable local intent tokens without unbounded growth', () => fixture(async f => {
  await activateCloud(f)
  await failLocal(f)
  let previous = intentKeys(f)[0]
  for (let i = 0; i < 8; i++) {
    await assert.rejects(() => f.store.retrySave(), /offline/)
    assert.equal(intentKeys(f).length, 1)
    assert.notEqual(intentKeys(f)[0], previous)
    previous = intentKeys(f)[0]
    assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
  }
}))

test('key-specific intent write failure is visible; losing all memory cannot preserve an unwritten refusal', () => fixture(async f => {
  await activateCloud(f)
  const set = f.storage.setItem
  f.storage.setItem = (key, value) => {
    if (key.startsWith(f.policy.SYNC_POLICY_LOCAL_INTENTS_KEY + ':')) throw new Error('intent writes denied')
    return set(key, value)
  }
  await failLocal(f)
  assert.equal(f.store.persistenceWarning, true)
  assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
  assert.equal(intentKeys(f).length, 0)
  assert.equal(JSON.parse(f.values.get(f.policy.SYNC_POLICY_CACHE_KEY)).policy.modules.notes, 'local', 'cache writes are independently available')
  // Simulate fresh module memory after the storage failure later recovers.
  // There is no durable refusal to recover: this accepted limit must stay visible
  // before closing the app, rather than being claimed as cross-restart safety.
  f.store.suspend()
  f.policy.installSyncPolicyStorage(f.storage)
  f.storage.setItem = set
  const { startSyncPolicyRuntime } = await f.vite.ssrLoadModule('/src/stores/sync-policy-runtime.ts')
  f.policy.syncPolicyTransport.fetchPolicy = async () => cloudPolicy()
  const stop = startSyncPolicyRuntime(f.auth, f.store, { startRuntimes() {}, connect() {}, disconnect() {} })
  try {
    for (let i = 0; i < 10; i++) await tick()
    assert.equal(f.policy.readCachedModuleMode('notes'), 'cloud', 'an unpersisted intention cannot be reconstructed after all memory is lost')
  } finally { stop() }
}))

test('a settings mount racing initial cache restoration cannot suppress runtime startup', () => fixture(async f => {
  const { startSyncPolicyRuntime } = await f.vite.ssrLoadModule('/src/stores/sync-policy-runtime.ts')
  const effects = []
  f.policy.syncPolicyTransport.fetchPolicy = async () => cloudPolicy()
  const stop = startSyncPolicyRuntime(f.auth, f.store, { startRuntimes: () => effects.push('start'), connect: () => effects.push('connect'), disconnect() {} })
  try {
    await f.store.load() // StorageSyncSection.onMounted before the initial queued publisher.
    for (let i = 0; i < 10; i++) await tick()
    assert.equal(f.store.activationReady, true)
    assert.equal(effects.filter(value => value === 'start').length, 1)
    assert.equal(effects.filter(value => value === 'connect').length, 1)
  } finally { stop() }
}))

test('storage intent superseding bootstrap still starts effects from the final confirmed policy exactly once', () => fixture(async f => {
  const { startSyncPolicyRuntime } = await f.vite.ssrLoadModule('/src/stores/sync-policy-runtime.ts')
  const effects = []
  f.policy.syncPolicyTransport.fetchPolicy = async () => cloudPolicy()
  const stop = startSyncPolicyRuntime(f.auth, f.store, { startRuntimes: () => effects.push('start'), connect: () => effects.push('connect'), disconnect() {} })
  try {
    const key = f.policy.SYNC_POLICY_LOCAL_INTENTS_KEY + ':bootstrap-tab-notes'
    f.values.set(key, JSON.stringify({ version: 1, scope: JSON.stringify([f.auth.userId, f.auth.workspaceId]), workspace: f.auth.workspaceId, module: 'notes' }))
    f.browser.dispatchEvent(Object.assign(new Event('storage'), { key }))
    for (let i = 0; i < 15; i++) await tick()
    assert.equal(f.store.activationReady, true)
    assert.equal(f.policy.readCachedModuleMode('notes'), 'local')
    assert.deepEqual(effects, ['start', 'connect'])
  } finally { stop() }
}))

test('startup effects that synchronously request a newer policy connect only after its confirmation', () => fixture(async f => {
  const { startSyncPolicyRuntime } = await f.vite.ssrLoadModule('/src/stores/sync-policy-runtime.ts')
  const effects = [], updates = []
  f.policy.syncPolicyTransport.fetchPolicy = async () => localNotes()
  const stop = startSyncPolicyRuntime(f.auth, f.store, {
    startRuntimes() { effects.push('start'); updates.push(f.store.applyPolicy(localNotes(), 5, false)) },
    connect() { assert.equal(f.store.activating, false); assert.equal(f.store.activationReady, true); assert.equal(f.store.modules.notes, 'local'); effects.push('connect') },
    disconnect() {},
  })
  try {
    for (let i = 0; i < 15; i++) await tick()
    await Promise.all(updates)
    assert.deepEqual(effects, ['start', 'connect'])
  } finally { stop() }
}))
