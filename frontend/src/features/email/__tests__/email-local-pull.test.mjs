import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { test } from 'node:test'
import { createServer } from 'vite'
import initSqlJs from 'sql.js'
import { createPinia, setActivePinia } from 'pinia'

const SQL = await initSqlJs()
const remote = { id: 'email-1', accountId: 'account-1', fromAddress: 'sender@example.com', subject: 'Remote subject', snippet: 'Remote snippet', date: Date.now(), updatedAt: Date.now() }
const account = { id: 'account-1', displayName: 'Remote account', emailAddress: 'reader@example.com', imapHost: 'imap.example.com', imapPort: 993, authType: 'password', enabled: true, updatedAt: 100 }
const localError = error => error.body?.error === 'module_policy_local'
const cancelledSync = error => error.name === 'AbortError' || localError(error)

async function fixture(run) {
  const previousStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const previousFetch = globalThis.fetch
  const values = new Map(), stored = new Map(), pending = []
  const storage = { keys: () => values.keys(), getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, String(value)), removeItem: key => values.delete(key) }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
  setActivePinia(createPinia())
  let afterQuery = () => {}, afterRun = () => {}, db, raw, driver, UtilsSQLite, originalDriverRun
  const track = promise => { pending.push(promise); promise.catch(() => {}); return promise }
  const settled = async () => {
    while (pending.length) {
      const results = await Promise.allSettled(pending.splice(0))
      for (const result of results) if (result.status === 'rejected' && result.reason?.name !== 'AbortError') throw result.reason
    }
  }
  const key = `__emailSQLite_${Math.random().toString(36).slice(2)}`
  const requests = []
  let respond = url => url.includes('/accounts') ? { accounts: [account] } : { emails: [], deletedIds: ['email-1'], total: 0 }
  globalThis.fetch = async url => {
    requests.push(String(url))
    const response = await respond(String(url))
    return response instanceof Response ? response : Response.json(response)
  }
  const vite = await createServer({ root: fileURLToPath(new URL('../../../../', import.meta.url)), configFile: false,
    server: { middlewareMode: true, hmr: false }, ssr: { noExternal: ['@capacitor-community/sqlite'] }, optimizeDeps: { noDiscovery: true }, plugins: [{
      name: 'email-real-sqlite-platform', enforce: 'pre',
      resolveId(source, importer) {
        if (source === '@capacitor-community/sqlite' && importer?.endsWith('/local-db.ts')) return '\0email-sqlite'
        if (importer?.endsWith('/local-db.ts') && source === './sqlite-web-init') return '\0email-web'
        if (importer?.endsWith('/local-db.ts') && source === './runtime-platform') return '\0email-platform'
        if (source.endsWith('/native/lobster-init')) return '\0email-ready'
      },
      load(id) {
        if (id === '\0email-sqlite') return `export const { SQLiteConnection, SQLiteDBConnection, CapacitorSQLite } = globalThis[${JSON.stringify(key)}]`
        if (id === '\0email-web') return 'export async function initSqliteWeb() {}'
        if (id === '\0email-platform') return 'export const isWebFallbackRuntime = () => true'
        if (id === '\0email-ready') return "import {ref} from 'vue'; export const lobsterReady = ref(true)"
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
      run: async o => { const changes = await driver.runSQL(o.statement, o.values, o.transaction, o.returnMode); await afterRun(o.statement); return { changes } },
      query: async o => { const rows = await driver.selectSQL(o.statement, o.values); await afterQuery(o.statement); return { values: rows } },
      beginTransaction: async () => ({ changes: { changes: await driver.beginTransaction() } }),
      commitTransaction: async () => ({ changes: { changes: await driver.commitTransaction() } }),
      rollbackTransaction: async () => ({ changes: { changes: await driver.rollbackTransaction() } }),
      isTransactionActive: async () => ({ result: driver.isTransActive() }),
    }
    globalThis[key] = { ...definitions, CapacitorSQLite: registerPlugin(key, { web: async () => platform }) }
    ;({ localDB: db } = await vite.ssrLoadModule('/src/native/local-db.ts'))
    await db.init('')
    await db.run("INSERT INTO local_email_accounts (id,display_name,email_address,imap_host,credential_encrypted,created_at,updated_at,owner_user_id,owner_workspace_id) VALUES ('account-1','Local account','reader@example.com','imap.localhost','ENCRYPTED',1,1,'fixture-user','fixture-workspace')")
    await db.run("INSERT INTO local_emails (id,account_id,from_address,subject,snippet,date,created_at,updated_at,owner_user_id,owner_workspace_id) VALUES ('email-1','account-1','sender@example.com','Local subject','PRIVATE_LOCAL_SNIPPET',1,1,1,'fixture-user','fixture-workspace')")
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
    const setMode = mode => track(policyStore.applyPolicy({ system_default: 'cloud', allow_override: true, modules: { email: mode } }, Date.now()))
    await setMode('cloud')
    const emails = await vite.ssrLoadModule('/src/features/email/emails-store.ts')
    const cache = await vite.ssrLoadModule('/src/features/email/email-body-cache.ts')
    await cache.writeEmailBodyLocal('email-1', 'PRIVATE_ORIGINAL_BODY')
    const page = await vite.ssrLoadModule('/src/features/email/email-inbox-page.ts')
    const accounts = await vite.ssrLoadModule('/src/features/email/account-sync.ts')
    const search = await vite.ssrLoadModule('/src/features/email/email-inbox-search.ts')
    const intact = async () => {
      await settled()
      const original = await db.queryOne("SELECT subject,snippet FROM local_emails WHERE owner_user_id='fixture-user'")
      assert.equal(original.subject, 'Local subject')
      assert.equal(original.snippet, 'PRIVATE_LOCAL_SNIPPET')
      const rows = await page.readInboxPage('')
      if (auth.userId === 'fixture-user') {
        assert.equal(rows[0]?.subject, 'Local subject')
        assert.equal(search.matchInboxSearch(rows[0], { q: 'PRIVATE_LOCAL_SNIPPET' }), true)
        assert.equal(await cache.readEmailBodyLocal('email-1'), 'PRIVATE_ORIGINAL_BODY')
        assert.equal((await emails.listAccounts())[0]?.displayName, 'Local account')
      } else {
        assert.deepEqual(rows, [])
        assert.deepEqual(await emails.listAccounts(), [])
        assert.equal(await cache.readEmailBodyLocal('email-1'), '')
      }
    }
    await run({ emails, page, accounts, cache, setMode, requests, intact, db, sqlite: raw, driver, UtilsSQLite, auth, track, settled,
      setResponse: fn => { respond = fn }, onQuery: fn => { afterQuery = fn }, onRun: fn => { afterRun = fn }, vite })
    await settled()
  } finally {
    afterQuery = afterRun = () => {}
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

test('local-only inbox refresh and sync preserve local search/body without requesting cloud data', () => fixture(async f => {
  await f.setMode('local')
  await f.page.pullInboxFromServer()
  await f.page.syncInboxFromServer()
  assert.equal(f.requests.length, 0)
  await f.intact()
}))

test('local mode blocks cloud mirror sync while preserving local ingestion', () => fixture(async f => {
  await f.setMode('local')
  await assert.rejects(f.emails.syncEmailsFromServerDetailed(), localError)
  assert.equal(f.requests.length, 0)
  await f.intact()
  assert.equal(await f.emails.upsertEmail({ ...remote, id: 'local-imap' }), true)
  assert.equal((await f.page.readInboxPage('')).length, 2)
}))

test('switching local while fetching rejects incoming rows and cloud tombstones', () => fixture(async f => {
  f.setResponse(() => { f.setMode('local'); return { emails: [remote], deletedIds: ['email-1'] } })
  await assert.rejects(f.emails.syncEmailsFromServerDetailed(), cancelledSync)
  await f.intact()
}))

for (const [name, sql, payload] of [
  ['email write', 'SELECT deleted_at, body_purged, folder', { emails: [remote] }],
  ['tombstone write', 'SELECT subject, snippet, ai_summary', { emails: [], deletedIds: ['email-1'] }],
]) test(`switching local while awaiting ${name} leaves original data intact`, () => fixture(async f => {
  f.setResponse(() => payload)
  f.onQuery(query => { if (query.startsWith(sql)) f.setMode('local') })
  await assert.rejects(f.emails.syncEmailsFromServerDetailed(), cancelledSync)
  await f.intact()
}))

test('account sync cannot overwrite local account after policy changes during fetch', () => fixture(async f => {
  f.setResponse(() => { f.setMode('local'); return { accounts: [account] } })
  await f.accounts.syncAccountsFromServer()
  await f.intact()
}))

test('account sync rechecks policy after waiting for the local account', () => fixture(async f => {
  f.onQuery(query => { if (query.includes('FROM local_email_accounts WHERE id')) f.setMode('local') })
  await f.accounts.syncAccountsFromServer()
  await f.intact()
}))

test('switching local during auth refresh prevents sync GETs from being sent', () => fixture(async f => {
  const { useAuthStore } = await f.vite.ssrLoadModule('/src/stores/auth.ts')
  useAuthStore().maybeRefresh = async () => { f.setMode('local') }
  await assert.rejects(f.emails.syncEmailsFromServerDetailed(), localError)
  assert.equal(f.requests.length, 0)
  await f.setMode('cloud')
  await f.accounts.syncAccountsFromServer()
  assert.equal(f.requests.length, 0)
  await f.intact()
}))

test('cloud sync still imports rows and applies tombstones', () => fixture(async f => {
  f.setResponse(() => ({ emails: [remote] }))
  assert.equal((await f.emails.syncEmailsFromServerDetailed()).inserted, 1)
  assert.equal((await f.page.readInboxPage(''))[0]?.subject, 'Remote subject')
  f.setResponse(() => ({ emails: [], deletedIds: ['email-1'] }))
  assert.equal((await f.emails.syncEmailsFromServerDetailed()).tombstones, 1)
  assert.deepEqual(await f.page.readInboxPage(''), [])
  assert.equal(await f.cache.readEmailBodyLocal('email-1'), '')
}))

test('local user deletion remains available in local mode', () => fixture(async f => {
  await f.setMode('local')
  const { purgeEmailsLocal } = await f.vite.ssrLoadModule('/src/features/email/email-soft-delete.ts')
  assert.equal(await purgeEmailsLocal(['email-1']), 1)
  assert.deepEqual(await f.page.readInboxPage(''), [])
  assert.equal(await f.cache.readEmailBodyLocal('email-1'), '')
  assert.equal(f.requests.length, 0)
}))

for (const transition of ['A→B', 'A→B→A']) {
  async function switcher(f) {
    const login = id => f.auth.setAuthWithWorkspace(`${id}-token`, id, id === 'A' ? 'fixture-user' : `user-${id}`, id === 'A' ? 'fixture-workspace' : `ws-${id}`)
    await login('A')
    await f.setMode('cloud')
    let switched = false
    return () => {
      if (switched) return
      switched = true
      const b = f.track(login('B'))
      const active = transition === 'A→B→A' ? f.track(login('A')) : b
      f.track(active.then(() => f.setMode('cloud')))
    }
  }
  for (const [name, sql, payload] of [
    ['email', 'SELECT deleted_at, body_purged, folder', { emails: [remote] }],
    ['tombstone', 'SELECT subject, snippet, ai_summary', { emails: [], deletedIds: ['email-1'] }],
  ]) test(`${transition} during ${name} lookup cancels the old sync and preserves original data`, () => fixture(async f => {
    const switchIdentity = await switcher(f)
    f.setResponse(() => payload)
    f.onQuery(query => { if (query.startsWith(sql)) switchIdentity() })
    await assert.rejects(f.emails.syncEmailsFromServerDetailed(), { name: 'AbortError' })
    await f.intact()
  }))

  test(`${transition} during account lookup cannot overwrite the local account`, () => fixture(async f => {
    const switchIdentity = await switcher(f)
    f.onQuery(query => { if (query.includes('FROM local_email_accounts WHERE id')) switchIdentity() })
    const report = await f.accounts.syncAccountsFromServer()
    assert.equal(report.applied, 0)
    assert.match(report.error ?? '', /session changed/)
    await f.intact()
  }))

  test(`${transition} after a tombstone write preserves original body before cache deletion`, () => fixture(async f => {
    const switchIdentity = await switcher(f)
    f.onRun(sql => { if (sql.startsWith('UPDATE local_emails SET snippet')) switchIdentity() })
    await assert.rejects(f.emails.syncEmailsFromServerDetailed(), { name: 'AbortError' })
    assert.equal(await f.cache.readEmailBodyLocal('email-1'), f.auth.userId === 'fixture-user' ? 'PRIVATE_ORIGINAL_BODY' : '')
  }))

  test(`${transition} during account cleanup cannot delete local accounts or continue the old refresh`, () => fixture(async f => {
    const switchIdentity = await switcher(f)
    await f.emails.updateAccount('account-1', { emailAddress: 'reader@test.local' })
    f.setResponse(() => ({ accounts: [] }))
    f.onQuery(query => { if (query.includes('FROM local_email_accounts WHERE') && query.includes('ORDER BY')) switchIdentity() })
    await f.page.pullInboxFromServer()
    assert.equal((await f.emails.listAccounts()).length, f.auth.userId === 'fixture-user' ? 1 : 0)
    assert.equal(f.requests.length, 1)
    await f.intact()
  }))

  for (const [name, payload] of [
    ['email', { emails: [remote] }],
    ['tombstone', { emails: [], deletedIds: ['email-1'] }],
    ['account', { accounts: [account] }],
  ]) test(`driver: ${transition} inside real SQLite ${name} write must preserve original data`, () => fixture(async f => {
    const switchIdentity = await switcher(f)
    const { UtilsSQLite } = await f.vite.ssrLoadModule('/node_modules/jeep-sqlite/dist/collection/utils/utils-sqlite.js')
    f.setResponse(() => payload)
    const originalRun = UtilsSQLite.run
    UtilsSQLite.run = (...args) => {
      const operation = originalRun.apply(UtilsSQLite, args)
      queueMicrotask(switchIdentity)
      return operation
    }
    if (name === 'account') await f.accounts.syncAccountsFromServer()
    else await assert.rejects(f.emails.syncEmailsFromServerDetailed(), { name: 'AbortError' })
    await f.intact()
  }))
}

for (const [name, payload] of [
  ['email', { emails: [remote] }],
  ['tombstone', { emails: [], deletedIds: ['email-1'] }],
  ['account', { accounts: [account] }],
]) test(`policy driver: switching local inside real ${name} SQL rolls back the cloud write`, () => fixture(async f => {
  f.setResponse(() => payload)
  const originalRun = f.UtilsSQLite.run
  let changed = false
  f.UtilsSQLite.run = (...args) => {
    const operation = originalRun.apply(f.UtilsSQLite, args)
    if (!changed) { changed = true; queueMicrotask(() => { f.setMode('local') }) }
    return operation
  }
  if (name === 'account') await f.accounts.syncAccountsFromServer()
  else await assert.rejects(f.emails.syncEmailsFromServerDetailed(), error => error.name === 'AbortError' || localError(error))
  await f.intact()
}))

test('account mirror cannot overwrite an edit queued after its stamp lookup', () => fixture(async f => {
  let edited = false
  f.onQuery(sql => {
    if (!edited && sql.includes('FROM local_email_accounts WHERE id')) {
      edited = true
      f.track(f.emails.updateAccount('account-1', { displayName: 'New local edit', updatedAt: 200 }))
    }
  })
  assert.equal(await f.emails.writeAccountIfNewer({ ...account, syncIntervalMin: 15 }), false)
  await f.settled()
  const row = (await f.emails.listAccounts())[0]
  assert.equal(row.displayName, 'New local edit')
  assert.equal(row.updatedAt, 200)
}))

test('account stamp comparison retains legacy milliseconds and credential bytes', () => fixture(async f => {
  await f.db.run('UPDATE local_email_accounts SET updated_at=? WHERE id=?', [1_700_000_000_000, 'account-1'])
  assert.equal(await f.emails.writeAccountIfNewer({ ...account, syncIntervalMin: 15, updatedAt: 1_700_000_100 }), true)
  const row = await f.db.queryOne('SELECT updated_at,credential_encrypted FROM local_email_accounts WHERE id=?', ['account-1'])
  assert.equal(row.updated_at, 1_700_000_100)
  assert.equal(row.credential_encrypted, 'ENCRYPTED')
}))

test('a local tombstone queued after cloud lookup cannot be resurrected by upsert', () => fixture(async f => {
  let deleted = false
  f.setResponse(() => ({ emails: [remote] }))
  f.onQuery(sql => {
    if (!deleted && sql.startsWith('SELECT deleted_at, body_purged, folder')) {
      deleted = true
      f.track(f.db.run('UPDATE local_emails SET deleted_at=?,body_purged=1,snippet=? WHERE id=?', [200, 'Local tombstone', 'email-1']))
    }
  })
  assert.equal((await f.emails.syncEmailsFromServerDetailed()).inserted, 0)
  await f.settled()
  const row = await f.db.queryOne('SELECT deleted_at,body_purged,snippet FROM local_emails WHERE id=?', ['email-1'])
  assert.deepEqual(row, { deleted_at: 200, body_purged: 1, snippet: 'Local tombstone' })
}))

test('cloud rows without source timestamps do not acquire the download time', () => fixture(async f => {
  f.setResponse(() => ({ emails: [{ ...remote, id: 'unstamped', date: '', updatedAt: 0 }] }))
  assert.equal((await f.emails.syncEmailsFromServerDetailed()).inserted, 1)
  const row = await f.db.queryOne('SELECT date,updated_at FROM local_emails WHERE id=?', ['unstamped'])
  assert.deepEqual(row, { date: 0, updated_at: 0 })
}))

test('a late older cloud response cannot replace a newer email version', () => fixture(async f => {
  f.setResponse(() => ({ emails: [{ ...remote, subject: 'Newer remote', updatedAt: 200 }] }))
  assert.equal((await f.emails.syncEmailsFromServerDetailed()).inserted, 1)
  f.setResponse(() => ({ emails: [{ ...remote, subject: 'Older remote', updatedAt: 100 }] }))
  assert.equal((await f.emails.syncEmailsFromServerDetailed()).inserted, 0)
  assert.equal((await f.page.readInboxPage(''))[0].subject, 'Newer remote')
}))

test('a cloud tombstone preserves source modification time rather than using download time', () => fixture(async f => {
  await f.emails.syncEmailsFromServerDetailed()
  assert.equal((await f.db.queryOne('SELECT updated_at FROM local_emails WHERE id=?', ['email-1'])).updated_at, 1)
}))

for (const action of ['upsert', 'delete']) test(`local ${action} keeps its original owner across an asynchronous lookup`, () => fixture(async f => {
  const { purgeEmailsLocal } = await f.vite.ssrLoadModule('/src/features/email/email-soft-delete.ts')
  await f.setMode('local')
  let switched = false
  f.onQuery(sql => {
    if (!switched && sql.includes('FROM local_emails WHERE id')) {
      switched = true
      f.track(f.auth.setAuthWithWorkspace('B-token', 'B', 'user-B', 'ws-B'))
    }
  })
  await assert.rejects(action === 'upsert' ? f.emails.upsertEmail(remote) : purgeEmailsLocal(['email-1']), { name: 'AbortError' })
  await f.intact()
}))

test('an expired passed operation cannot send an account patch as the next owner', () => fixture(async f => {
  const { captureEmailSyncOperation } = await f.vite.ssrLoadModule('/src/features/email/email-sync-guard.ts')
  const operation = captureEmailSyncOperation()
  await f.auth.setAuthWithWorkspace('B-token', 'B', 'user-B', 'ws-B')
  await f.setMode('cloud')
  await assert.rejects(f.accounts.pushAccountToServer(account, operation), { name: 'AbortError' })
  await f.accounts.syncAccountsFromServer(operation)
  assert.equal(f.requests.length, 0)
  await f.intact()
}))

test('account retry uses the initiating lease for outbox writes', () => fixture(async f => {
  f.setResponse(() => { throw new Error('offline') })
  f.onRun(sql => {
    if (sql.includes('INSERT INTO local_config_outbox')) {
      f.track(f.auth.setAuthWithWorkspace('B-token', 'B', 'user-B', 'ws-B'))
    }
  })
  await assert.rejects(f.accounts.pushAccountToServer(account), { name: 'AbortError' })
  await f.settled()
  assert.equal((await f.db.queryOne('SELECT count(*) AS n FROM local_config_outbox')).n, 0)
}))

test('account retry still queues unchanged-session failures', () => fixture(async f => {
  f.setResponse(() => { throw new Error('offline') })
  assert.equal(await f.accounts.pushAccountToServer(account), false)
  const row = await f.db.queryOne('SELECT entity_id,state FROM local_config_outbox')
  assert.deepEqual(row, { entity_id: 'account-1', state: 'queued' })
}))

test('a restored session retains authorized cloud reads without claiming historical rows', () => fixture(async f => {
  await f.auth.syncFromStorage(true)
  assert.equal(f.auth.userId, '')
  await f.setMode('cloud') // authoritative policy admission, not inferred from disk identity
  const { captureEmailSyncOperation } = await f.vite.ssrLoadModule('/src/features/email/email-sync-guard.ts')
  const operation = captureEmailSyncOperation()
  assert.equal(operation.lease.owner.principal, null)
  f.setResponse(() => ({ emails: [remote] }))
  assert.equal((await f.emails.syncEmailsFromServerDetailed(200, 0, operation)).inserted, 1)
  assert.equal((await f.db.queryOne('SELECT credential_encrypted FROM local_email_accounts')).credential_encrypted, 'ENCRYPTED')
}))
