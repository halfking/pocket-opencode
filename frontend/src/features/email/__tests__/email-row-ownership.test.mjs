import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { createServer } from 'vite'
import initSqlJs from 'sql.js'

async function fixture(run) {
  const SQL = await initSqlJs(), raw = new SQL.Database()
  const originalStorage = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
  const stored = new Map()
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
    getItem: key => stored.get(key) ?? null, setItem: (key, value) => stored.set(key, String(value)), removeItem: key => stored.delete(key),
  } })
  let owner = { userId: 'alice', workspaceId: 'one' }, epoch = 0
  const query = async (sql, values = []) => {
    const stmt = raw.prepare(sql)
    try { stmt.bind(values); const rows = []; while (stmt.step()) rows.push(stmt.getAsObject()); return rows } finally { stmt.free() }
  }
  const database = {
    captureOperation() { return { owner: { principal: owner }, epoch } },
    bindOperation(lease) {
      const assertCurrent = () => { if (lease.epoch !== epoch) throw new DOMException('expired', 'AbortError') }
      return { owner: lease.owner.principal, assertCurrent,
        query: async (...args) => { assertCurrent(); return query(...args) },
        queryOne: async (...args) => { assertCurrent(); return (await query(...args))[0] ?? null },
        run: async (sql, values = []) => { assertCurrent(); raw.run(sql, values); return raw.getRowsModified() } }
    },
  }
  Object.assign(database, {
    query, queryOne: async (...args) => (await query(...args))[0] ?? null,
    run: async (sql, values = []) => { raw.run(sql, values); return raw.getRowsModified() },
  })
  const key = `__emailOwnership${Math.random().toString(36).slice(2)}`
  globalThis[key] = database
  const vite = await createServer({ root: fileURLToPath(new URL('../../../../', import.meta.url)), configFile: false,
    server: { middlewareMode: true, hmr: false }, optimizeDeps: { noDiscovery: true }, plugins: [{ name: 'email-ownership-db', enforce: 'pre',
      resolveId(source) {
        if (source.endsWith('/native/local-db')) return '\0ownership-db'
        if (source.endsWith('/native/crypto')) return '\0ownership-crypto'
      }, load(id) {
        if (id === '\0ownership-db') return `export const localDB = globalThis[${JSON.stringify(key)}]`
        if (id === '\0ownership-crypto') return 'export async function encryptString(s) { return s }'
      } }] })
  try {
    const { SCHEMA_SQL } = await vite.ssrLoadModule('/src/native/schema.ts')
    raw.run(SCHEMA_SQL.replace(/CREATE VIRTUAL TABLE[^;]+;/g, '').replace(/CREATE TRIGGER[^]*?END;/g, ''))
    const emails = await vite.ssrLoadModule('/src/features/email/emails-store.ts')
    const folders = await vite.ssrLoadModule('/src/features/email/email-folders-store.ts')
    const invoices = await vite.ssrLoadModule('/src/features/email/invoices-store.ts')
    const cache = await vite.ssrLoadModule('/src/features/email/email-body-cache.ts')
    await run({ raw, emails, folders, invoices, cache, stored, vite, switchOwner(userId, workspaceId) { owner = userId ? { userId, workspaceId } : null; epoch++ } })
  } finally {
    if (originalStorage) Object.defineProperty(globalThis, 'localStorage', originalStorage)
    else delete globalThis.localStorage
    delete globalThis[key]; await vite.close(); raw.close()
  }
}

test('mail accounts, messages and operation queues belong to the captured account and workspace', () => fixture(async ({ emails, folders, switchOwner }) => {
  const id = await emails.saveAccount({ displayName: 'Alice private', emailAddress: 'alice@example.com', imapHost: 'imap.example.com', password: 'secret' })
  await emails.upsertEmail({ id: 'same-email', accountId: id, fromAddress: 'sender@example.com', date: 1, subject: 'Alice private' })
  await folders.recordOpsEntry({ accountId: id, emailId: 'same-email', uid: 1, action: 'delete', targetFolder: '', subject: 'Alice private' })
  switchOwner('bob', 'one')
  assert.deepEqual(await emails.listAccounts(), [])
  assert.equal(await emails.getEmail('same-email'), null)
  assert.equal(await folders.countPendingOps(), 0)
  await emails.markRead('same-email', true)
  await emails.deleteAccount(id)
  switchOwner('alice', 'two')
  assert.deepEqual(await emails.listAccounts(), [])
  switchOwner('alice', 'one')
  assert.equal((await emails.listAccounts())[0].displayName, 'Alice private')
  assert.equal((await emails.getEmail('same-email')).isRead, false)
  assert.equal(await folders.countPendingOps(), 1)
}))

test('body caches and invoice rows with matching IDs remain separate across owners', () => fixture(async ({ invoices, cache, stored, switchOwner }) => {
  const invoice = { id: 'same-invoice', emailId: 'same-mail', accountId: 'account', kind: 'bill', category: 'other', title: 'Alice invoice', seller: 'seller', amount: 3, currency: 'CNY', subject: 'private', status: 'new', extractedBy: 'rule', createdAt: 1, updatedAt: 1 }
  await invoices.upsertFromServer([invoice])
  await cache.writeEmailBodyLocal('same-mail', 'Alice body')
  const context = cache.captureEmailBodyContext()
  stored.set('pocket:email_body:legacy', 'UNCLAIMED')
  switchOwner('bob', 'one')
  assert.deepEqual((await invoices.listLocalPage({})).rows, [])
  assert.equal(await cache.readEmailBodyLocal('same-mail'), '')
  assert.equal(await cache.readEmailBodyLocal('legacy'), '')
  await assert.rejects(cache.writeEmailBodyLocal('same-mail', 'late Alice response', context), { name: 'AbortError' })
  await cache.writeEmailBodyLocal('same-mail', 'Bob body')
  await invoices.upsertFromServer([{ ...invoice, title: 'Bob invoice' }])
  await invoices.setLocalStatus('same-invoice', 'filed')
  switchOwner('alice', 'one')
  assert.equal(await cache.readEmailBodyLocal('same-mail'), 'Alice body')
  assert.equal((await invoices.listLocalPage({})).rows[0].title, 'Alice invoice')
  assert.equal((await invoices.listLocalPage({})).rows[0].status, 'new')
  switchOwner(null)
  assert.equal(await cache.readEmailBodyLocal('same-mail'), '')
  assert.deepEqual((await invoices.listLocalPage({})).rows, [])
  assert.equal(stored.get('pocket:email_body:legacy'), 'UNCLAIMED')
}))

test('ownership migration preserves unknown legacy rows, foreign keys and rollback on failure', () => fixture(async ({ raw, vite }) => {
  const { migrateEmailOwnership } = await vite.ssrLoadModule('/src/native/email-ownership-migration.ts')
  const tables = ['local_email_accounts', 'local_emails', 'local_email_folders', 'local_email_ops', 'local_email_invoices']
  for (const table of [...tables].reverse()) raw.run(`DROP TABLE ${table}`)
  raw.run(`PRAGMA foreign_keys=ON;
    CREATE TABLE local_email_accounts(id TEXT PRIMARY KEY,display_name TEXT NOT NULL,email_address TEXT NOT NULL,imap_host TEXT NOT NULL,credential_encrypted TEXT NOT NULL,created_at INTEGER NOT NULL);
    CREATE TABLE local_emails(id TEXT PRIMARY KEY,account_id TEXT NOT NULL,from_address TEXT NOT NULL,date INTEGER NOT NULL,created_at INTEGER NOT NULL,FOREIGN KEY(account_id) REFERENCES local_email_accounts(id) ON DELETE CASCADE);
    CREATE TABLE local_email_folders(id TEXT PRIMARY KEY,account_id TEXT NOT NULL,name TEXT NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL);
    CREATE TABLE local_email_ops(id TEXT PRIMARY KEY,account_id TEXT NOT NULL,email_id TEXT NOT NULL,action TEXT NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL);
    CREATE TABLE local_email_invoices(id TEXT PRIMARY KEY,email_id TEXT NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL);
    INSERT INTO local_email_accounts VALUES('legacy-account','private','a@example.com','imap.example.com','encrypted',1);
    INSERT INTO local_emails VALUES('legacy-mail','legacy-account','sender@example.com',1,1);`)
  const conn = {
    query: async sql => { const result = raw.exec(sql)[0]; return { values: result ? result.values.map(row => Object.fromEntries(result.columns.map((key, i) => [key, row[i]]))) : [] } },
    execute: async sql => { raw.run(sql) }, beginTransaction: async () => raw.run('BEGIN'), commitTransaction: async () => raw.run('COMMIT'), rollbackTransaction: async () => raw.run('ROLLBACK'),
  }
  const execute = conn.execute
  conn.execute = async sql => { if (sql.startsWith('INSERT INTO local_emails_owned')) throw new Error('disk failure'); return execute(sql) }
  await assert.rejects(migrateEmailOwnership(conn), /disk failure/)
  assert.equal(raw.exec('SELECT COUNT(*) FROM local_emails')[0].values[0][0], 1)
  conn.execute = execute
  await migrateEmailOwnership(conn)
  assert.deepEqual(raw.exec('SELECT id,owner_user_id,owner_workspace_id FROM local_emails')[0].values, [['legacy-mail', '', '']])
  assert.deepEqual(raw.exec('PRAGMA foreign_key_check'), [])
  await migrateEmailOwnership(conn)
  assert.equal(raw.exec('SELECT COUNT(*) FROM local_emails')[0].values[0][0], 1)
}))

test('same mail IDs do not share an in-flight body across accounts or accept the old response', () => fixture(async ({ cache, vite, switchOwner }) => {
  const { prefetchEmailBody, inflightBodyCount } = await vite.ssrLoadModule('/src/features/email/email-body-prefetch.ts')
  let releaseAlice
  const deps = { captureScope: cache.captureEmailBodyContext, readCache: cache.readEmailBodyLocal, writeCache: cache.writeEmailBodyLocal, extract: raw => raw,
    fetchBody: () => new Promise(resolve => { releaseAlice = resolve }) }
  const alice = prefetchEmailBody('shared', deps)
  await new Promise(resolve => setImmediate(resolve))
  switchOwner('bob', 'one')
  const bob = prefetchEmailBody('shared', { ...deps, fetchBody: async () => ({ body: 'Bob body' }) })
  assert.equal(inflightBodyCount(), 2)
  assert.deepEqual(await bob, { ok: true, body: 'Bob body', purged: false })
  releaseAlice({ body: 'Alice body' })
  assert.equal((await alice).ok, false)
  assert.equal(await cache.readEmailBodyLocal('shared'), 'Bob body')
  switchOwner('alice', 'one')
  assert.equal(await cache.readEmailBodyLocal('shared'), '')
}))

test('translation caches do not claim legacy keys or publish an old owner response', () => fixture(async ({ cache, stored, vite, switchOwner }) => {
  const { restoreEmailTranslations, translateEmailForOwner } = await vite.ssrLoadModule('/src/features/email/email-translation-cache.ts')
  stored.set('email_translations:shared', JSON.stringify({ zh: 'UNCLAIMED LEGACY' }))
  const alice = cache.captureEmailBodyContext()
  assert.deepEqual(restoreEmailTranslations('shared', alice), {})
  await translateEmailForOwner('shared', 'zh', async () => 'Alice translation', alice)
  let release
  const delayed = translateEmailForOwner('shared', 'en', () => new Promise(resolve => { release = resolve }), alice)
  switchOwner('bob', 'one')
  assert.deepEqual(restoreEmailTranslations('shared'), {})
  release('Late Alice translation')
  await assert.rejects(delayed, { name: 'AbortError' })
  await translateEmailForOwner('shared', 'zh', async () => 'Bob translation', cache.captureEmailBodyContext())
  switchOwner('alice', 'one')
  assert.deepEqual(restoreEmailTranslations('shared'), { zh: 'Alice translation' })
  assert.equal(stored.get('email_translations:shared'), JSON.stringify({ zh: 'UNCLAIMED LEGACY' }))
  switchOwner('alice', 'two')
  assert.deepEqual(restoreEmailTranslations('shared'), {})
}))

test('a multi-step mail action never dispatches a second request as the next owner', () => fixture(async ({ cache, vite, switchOwner }) => {
  const { bindEmailRequests } = await vite.ssrLoadModule('/src/features/email/email-request-owner.ts')
  const calls = []
  let release
  const service = bindEmailRequests({ first: () => new Promise(resolve => { release = resolve }), remove: async id => { calls.push(id) } }, cache.captureEmailBodyContext())
  const first = service.first()
  switchOwner('bob', 'one')
  release('Alice response')
  await assert.rejects(first, { name: 'AbortError' })
  await assert.rejects(service.remove('shared'), { name: 'AbortError' })
  assert.deepEqual(calls, [])
}))
