import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture, gate } from '../../../test-support/notes-sqlite-fixture.mjs'

test('new mail accounts atomically queue only portable metadata and preserve credentials', () => fixture(async ({ loadModule, db, raw }) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const { portalMailStore } = await loadModule('/src/features/email/portal-mail-store.ts')
  const operation = db.bindOperation(db.captureOperation({ kind: 'local-edit' }))
  const store = portalMailStore(operation)
  await store.observeEpoch(0)
  const id = await emails.saveAccount({ displayName: 'Private mailbox', emailAddress: 'a@example.com', imapHost: 'secret-imap.local', password: 'SECRET_PASSWORD' })
  const [item] = await store.pending()
  assert.equal(item.kind, 'account')
  assert.equal(item.ext_id, `account:${id}`)
  assert.deepEqual(item.payload, { address: 'a@example.com', display_name: 'Private mailbox', last_sync_at: '' })
  assert.equal(JSON.stringify(item).includes('SECRET_PASSWORD'), false)
  assert.equal(JSON.stringify(item).includes('secret-imap.local'), false)
  assert.ok(raw.exec('SELECT credential_encrypted FROM local_email_accounts')[0].values[0][0])
}))

test('unclaimed local mail history is not adopted by an ordinary edit', () => fixture(async ({ loadModule, db }) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const { portalMailStore } = await loadModule('/src/features/email/portal-mail-store.ts')
  const operation = db.bindOperation(db.captureOperation({ kind: 'local-edit' }))
  const store = portalMailStore(operation)
  await store.observeEpoch(0)
  await operation.run(`INSERT INTO local_email_accounts(id,display_name,email_address,imap_host,credential_encrypted,created_at,owner_user_id,owner_workspace_id)
    VALUES('old','Old','old@example.com','imap.local','ENCRYPTED',1,'user-A','workspace')`)
  await emails.updateAccount('old', { displayName: 'Edited local history' })
  assert.equal((await store.pending()).length, 0)
  assert.equal(await store.find('account:old'), null)
}))

for (const sourceId of ['managed','summary:managed']) test(`mail edits and deletion reuse legacy email identity at source ${sourceId}`, () => fixture(async ({loadModule,db}) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const {portalMailStore} = await loadModule('/src/features/email/portal-mail-store.ts')
  const store = portalMailStore(db.bindOperation(db.captureOperation({kind:'local-edit'})))
  await store.observeEpoch(0)
  const accountId = await emails.saveAccount({displayName:'A',emailAddress:'a@example.com',imapHost:'imap.local',password:'secret'})
  await emails.upsertEmail({id:'managed',accountId,fromAddress:'a@example.com',subject:'Original',snippet:'Summary',date:1,updatedAt:1000})
  const canonical = await store.byExternalId('summary:managed','summary'), old = await store.decode(canonical.item_json)
  await db.run("DELETE FROM local_portal_record_outbox WHERE module='email' AND kind='summary'")
  await db.run("DELETE FROM local_portal_records WHERE module='email' AND kind='summary'")
  const extId = `email:${sourceId}`
  await store.commit([], {id:sourceId,extId,kind:'summary',title:old.title,payload:old.payload,modifiedAt:1000,epoch:0},true)
  await emails.setAiSummary('managed','Explicit update')
  assert.equal((await store.decode((await store.find(sourceId)).item_json)).payload.snippet,'Explicit update')
  await emails.deleteAccount(accountId)
  const pending = (await store.pending()).filter(item=>item.kind==='summary')
  assert.equal(pending.length,3)
  assert.ok(pending.every(item=>item.ext_id===extId))
  assert.equal(pending.at(-1).deleted,true)
  assert.equal(await store.byExternalId('summary:managed','summary'),null)
}))

test('conflicting canonical and legacy mail projections require explicit resolution before source edits', () => fixture(async ({loadModule,db}) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const {portalMailStore} = await loadModule('/src/features/email/portal-mail-store.ts')
  const store = portalMailStore(db.bindOperation(db.captureOperation({kind:'local-edit'})))
  await store.observeEpoch(0)
  const accountId = await emails.saveAccount({displayName:'A',emailAddress:'a@example.com',imapHost:'imap.local',password:'secret'})
  await emails.upsertEmail({id:'managed',accountId,fromAddress:'a@example.com',subject:'Original',snippet:'Summary',date:1,updatedAt:1000})
  await store.commit([], {id:'managed',extId:'email:managed',kind:'summary',title:'Legacy',payload:{from:'a@example.com',snippet:'Legacy'},modifiedAt:1000,epoch:0},true)
  await assert.rejects(emails.setAiSummary('managed','Must not split'),/identity.*resolution/i)
  assert.equal((await emails.getEmail('managed')).aiSummary,null)
  assert.equal((await store.pending()).filter(item=>item.kind==='summary').length,2)
}))

test('legacy account mappings retain independently edited metadata through native updates and deletion', () => fixture(async ({loadModule,db}) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const {portalMailStore} = await loadModule('/src/features/email/portal-mail-store.ts')
  const store = portalMailStore(db.bindOperation(db.captureOperation({kind:'local-edit'})))
  await store.observeEpoch(0)
  const id = await emails.saveAccount({displayName:'Native',emailAddress:'native@example.com',imapHost:'imap.local',password:'secret'})
  await db.run("DELETE FROM local_portal_record_outbox WHERE module='email'")
  await db.run("DELETE FROM local_portal_records WHERE module='email'")
  await store.commit([], {id,extId:`email:${id}`,kind:'account',title:'Portal title',payload:{address:'portal@example.com',display_name:'Portal',last_sync_at:''},modifiedAt:1000,epoch:0},true)
  await emails.updateAccount(id,{displayName:'Explicit name'})
  const item = await store.decode((await store.find(id)).item_json)
  assert.equal(item.ext_id,`email:${id}`)
  assert.equal(item.payload.address,'portal@example.com')
  assert.equal(item.payload.display_name,'Explicit name')
  await emails.deleteAccount(id)
  assert.ok((await store.pending()).every(item=>item.ext_id===`email:${id}`))
}))

test('private mailbox settings retain independently edited portal metadata without another operation', () => fixture(async ({ loadModule, db }) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const { portalMailStore, editPortalMailMetadata } = await loadModule('/src/features/email/portal-mail-store.ts')
  const store = portalMailStore(db.bindOperation(db.captureOperation({ kind: 'local-edit' })))
  await store.observeEpoch(0)
  const id = await emails.saveAccount({ displayName: 'Original', emailAddress: 'a@example.com', imapHost: 'imap.local', password: 'secret' })
  await new Promise(resolve => setTimeout(resolve, 2))
  const row = await store.byExternalId(`account:${id}`, 'account')
  await editPortalMailMetadata(row.source_id, { title: 'Portal title', payload: { address: 'portal@example.com', display_name: 'Portal display', last_sync_at: '2026-10-28T00:00:00.000Z' } })
  const before = await store.byExternalId(`account:${id}`, 'account'), pending = await store.pending()
  await emails.updateAccount(id, { enabled: false, imapHost: 'private-changed.local' })
  assert.deepEqual(await store.byExternalId(`account:${id}`, 'account'), before)
  assert.deepEqual(await store.pending(), pending)
  const account = await emails.getAccount(id)
  assert.equal(account.enabled, false)
  assert.equal(account.imapHost, 'private-changed.local')
}))

test('a source account edit merges only explicit portable fields into the current portal metadata', () => fixture(async ({ loadModule, db }) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const { portalMailStore, editPortalMailMetadata } = await loadModule('/src/features/email/portal-mail-store.ts')
  const store = portalMailStore(db.bindOperation(db.captureOperation({ kind: 'local-edit' })))
  await store.observeEpoch(0)
  const id = await emails.saveAccount({ displayName: 'Original', emailAddress: 'a@example.com', imapHost: 'imap.local', password: 'secret' })
  await new Promise(resolve => setTimeout(resolve, 2))
  const row = await store.byExternalId(`account:${id}`, 'account')
  await editPortalMailMetadata(row.source_id, { title: 'Portal title', payload: { address: 'portal@example.com', display_name: 'Portal display', last_sync_at: '2026-10-28T00:00:00.000Z' } })
  await new Promise(resolve => setTimeout(resolve, 2))
  await emails.updateAccount(id, { displayName: 'Explicit name' })
  let item = await store.decode((await store.byExternalId(`account:${id}`, 'account')).item_json)
  assert.equal(item.title, 'Explicit name')
  assert.deepEqual(item.payload, { address: 'portal@example.com', display_name: 'Explicit name', last_sync_at: '2026-10-28T00:00:00.000Z' })
  await new Promise(resolve => setTimeout(resolve, 2))
  await emails.updateAccount(id, { emailAddress: 'explicit@example.com' })
  item = await store.decode((await store.byExternalId(`account:${id}`, 'account')).item_json)
  assert.equal(item.title, 'Explicit name')
  assert.deepEqual(item.payload, { address: 'explicit@example.com', display_name: 'Explicit name', last_sync_at: '2026-10-28T00:00:00.000Z' })
}))

test('receiving sync advances only last_sync_at in independently edited account metadata', () => fixture(async ({ loadModule, db }) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const { portalMailStore, editPortalMailMetadata } = await loadModule('/src/features/email/portal-mail-store.ts')
  const store = portalMailStore(db.bindOperation(db.captureOperation({ kind: 'local-edit' })))
  await store.observeEpoch(0)
  const id = await emails.saveAccount({ displayName: 'Original', emailAddress: 'a@example.com', imapHost: 'imap.local', password: 'secret' })
  await new Promise(resolve => setTimeout(resolve, 2))
  const row = await store.byExternalId(`account:${id}`, 'account')
  await editPortalMailMetadata(row.source_id, { title: 'Portal title', payload: { address: 'portal@example.com', display_name: 'Portal display', last_sync_at: '' } })
  await new Promise(resolve => setTimeout(resolve, 2))
  await emails.updateSyncState(id, 7)
  const item = await store.decode((await store.byExternalId(`account:${id}`, 'account')).item_json)
  assert.equal(item.title, 'Portal title')
  assert.equal(item.payload.address, 'portal@example.com')
  assert.equal(item.payload.display_name, 'Portal display')
  assert.equal(item.payload.last_sync_at, new Date((await emails.getAccount(id)).lastSyncedAt).toISOString())
}))

test('a private-only account mirror does not overwrite separately edited portal fields', () => fixture(async ({ loadModule, db, setModules }) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const { portalMailStore, editPortalMailMetadata } = await loadModule('/src/features/email/portal-mail-store.ts')
  await setModules({ email: 'cloud' })
  const store = portalMailStore(db.bindOperation(db.captureOperation({ kind: 'local-edit' })))
  await store.observeEpoch(0)
  const id = await emails.saveAccount({ displayName: 'Original', emailAddress: 'a@example.com', imapHost: 'imap.local', password: 'secret' })
  await new Promise(resolve => setTimeout(resolve, 2))
  const row = await store.byExternalId(`account:${id}`, 'account')
  await editPortalMailMetadata(row.source_id, { title: 'Portal title', payload: { address: 'portal@example.com', display_name: 'Portal display', last_sync_at: '2026-10-28T00:00:00.000Z' } })
  const before = await store.byExternalId(`account:${id}`, 'account'), pending = await store.pending()
  const updatedAt = Math.floor(Date.now() / 1000) + 1
  assert.equal(await emails.writeAccountIfNewer({ id, displayName: 'Original', emailAddress: 'a@example.com', imapHost: 'new.private.local', imapPort: 993, authType: 'password', syncIntervalMin: 15, enabled: false, updatedAt }), true)
  assert.equal((await emails.getAccount(id)).imapHost, 'new.private.local')
  assert.deepEqual(await store.byExternalId(`account:${id}`, 'account'), before)
  assert.deepEqual(await store.pending(), pending)
}))

test('source writes and their outbox are one transaction, and edits preserve the original immutable operation', () => fixture(async ({ loadModule, db, raw }) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const { portalMailStore } = await loadModule('/src/features/email/portal-mail-store.ts')
  const store = portalMailStore(db.bindOperation(db.captureOperation({ kind: 'local-edit' })))
  await store.observeEpoch(0)
  raw.run(`CREATE TRIGGER reject_mail_outbox BEFORE INSERT ON local_portal_record_outbox BEGIN SELECT RAISE(ABORT,'disk full'); END`)
  await assert.rejects(emails.saveAccount({ displayName: 'A', emailAddress: 'a@example.com', imapHost: 'imap.local', password: 'secret' }), /disk full/)
  assert.equal((await emails.listAccounts()).length, 0)
  raw.run('DROP TRIGGER reject_mail_outbox')
  const id = await emails.saveAccount({ displayName: 'A', emailAddress: 'a@example.com', imapHost: 'imap.local', password: 'secret' })
  const first = (await store.pending())[0]
  await new Promise(resolve => setTimeout(resolve, 2))
  await emails.updateAccount(id, { displayName: 'New display' })
  const pending = await store.pending()
  assert.equal(pending.length, 2)
  assert.deepEqual(pending.find(item => item.op_id === first.op_id), first)
  assert.equal(pending.find(item => item.op_id !== first.op_id).payload.display_name, 'New display')
}))

test('new summaries with unknown source time remain blocked, while explicit local edits carry their own version', () => fixture(async ({ loadModule, db }) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const { portalMailStore } = await loadModule('/src/features/email/portal-mail-store.ts')
  const store = portalMailStore(db.bindOperation(db.captureOperation({ kind: 'local-edit' })))
  await store.observeEpoch(0)
  const accountId = await emails.saveAccount({ displayName: 'A', emailAddress: 'a@example.com', imapHost: 'imap.local', password: 'secret' })
  await emails.upsertEmail({ id: 'unknown', accountId, fromAddress: 'from@example.com', subject: 'Original', snippet: 'snippet', date: 1 })
  assert.equal((await store.pending()).some(item => item.ext_id === 'summary:unknown'), false)
  const blocked = await db.queryOne("SELECT last_error FROM local_portal_record_outbox WHERE ext_id='summary:unknown'")
  assert.equal(blocked.last_error, 'source_timestamp_unknown')
  await emails.setAiSummary('unknown', 'A user-requested summary')
  const item = (await store.pending()).find(item => item.ext_id === 'summary:unknown')
  assert.equal(item.payload.snippet, 'A user-requested summary')
  assert.ok(Date.parse(item.client_updated_at) > 1)
}))

test('remote mail metadata cannot create an enabled mailbox or modify native body, credentials or account links', () => fixture(async ({ loadModule, db, raw, login }) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const { portalMailStore, listPortalMailMetadata } = await loadModule('/src/features/email/portal-mail-store.ts')
  const { applyRecordPage } = await loadModule('/src/native/portal-sync/record-engine.ts')
  const store = portalMailStore(db.bindOperation(db.captureOperation({ kind: 'local-edit' })))
  await store.observeEpoch(0)
  const at = new Date(Date.now()).toISOString()
  const item = { module: 'email', kind: 'account', ext_id: 'account:remote', title: 'Remote account', payload: { address: 'remote@example.com', display_name: 'Remote', last_sync_at: '' }, client_updated_at: at, op_id: 'remote-1', deleted: false, epoch: 0, change_seq: 1 }
  await applyRecordPage(store, { epoch: 0, changes: [item, { ...item, kind: 'summary', ext_id: 'summary:remote', title: 'Remote summary', payload: { from: 'from@example.com', subject: 'Remote', snippet: 'Portable metadata', received_at: at }, op_id: 'remote-2', change_seq: 2 }], next_cursor: '2' })
  assert.equal((await emails.listAccounts()).length, 0)
  assert.equal((await emails.listEmails()).length, 0)
  assert.equal((await listPortalMailMetadata()).length, 2)
  assert.equal(raw.exec('SELECT COUNT(*) FROM local_email_accounts')[0].values[0][0], 0)
  await login('B')
  assert.deepEqual(await listPortalMailMetadata(), [])
}))

test('deleting a source account atomically queues managed child tombstones without deleting portal-only metadata', () => fixture(async ({ loadModule, db }) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const { portalMailStore } = await loadModule('/src/features/email/portal-mail-store.ts')
  const store = portalMailStore(db.bindOperation(db.captureOperation({ kind: 'local-edit' })))
  await store.observeEpoch(0)
  const accountId = await emails.saveAccount({ displayName: 'A', emailAddress: 'a@example.com', imapHost: 'imap.local', password: 'secret' })
  await emails.upsertEmail({ id: 'managed', accountId, fromAddress: 'from@example.com', subject: 'Original', snippet: 'snippet', date: 1, updatedAt: Date.now() - 1000 })
  await new Promise(resolve => setTimeout(resolve, 2))
  await emails.deleteAccount(accountId)
  const pending = await store.pending()
  assert.ok(pending.some(item => item.ext_id === `account:${accountId}` && item.deleted))
  assert.ok(pending.some(item => item.ext_id === 'summary:managed' && item.deleted))
  assert.equal((await emails.listAccounts()).length, 0)
}))

for (const change of ['insert child', 'edit existing child']) test(`account deletion rejects a stale child snapshot after ${change}`, () => fixture(async ({ loadModule, db }) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const cache = await loadModule('/src/features/email/email-body-cache.ts')
  const { portalMailStore } = await loadModule('/src/features/email/portal-mail-store.ts')
  const { PortalRecordStore } = await loadModule('/src/native/portal-sync/record-store.ts')
  const store = portalMailStore(db.bindOperation(db.captureOperation({ kind: 'local-edit' })))
  await store.observeEpoch(0)
  const accountId = await emails.saveAccount({ displayName: 'A', emailAddress: 'a@example.com', imapHost: 'imap.local', password: 'secret' })
  await emails.upsertEmail({ id: 'original', accountId, fromAddress: 'sender@example.com', subject: 'Original', snippet: 'Original summary', date: 1, updatedAt: Date.now() - 1000 })
  await cache.writeEmailBodyLocal('original', 'PRIVATE BODY')
  const wait = gate(), commit = PortalRecordStore.prototype.commitMany
  PortalRecordStore.prototype.commitMany = async function (statements, entries) {
    if (entries.some(entry => entry.snapshot.kind === 'account' && entry.snapshot.deleted)) await wait.pause()
    return commit.call(this, statements, entries)
  }
  try {
    const deletion = emails.deleteAccount(accountId)
    await wait.started
    if (change === 'insert child') await emails.upsertEmail({ id: 'new-child', accountId, fromAddress: 'new@example.com', subject: 'New child', snippet: 'New summary', date: 1, updatedAt: Date.now() })
    else await emails.markRead('original', true)
    wait.release()
    await assert.rejects(deletion, /changed.*retry/i)
    assert.equal((await emails.listAccounts()).length, 1)
    assert.equal((await emails.getEmail('original')).isRead, change === 'edit existing child')
    assert.equal(await cache.readEmailBodyLocal('original'), 'PRIVATE BODY')
    assert.equal((await store.pending()).some(item => item.deleted), false)
    if (change === 'insert child') {
      assert.equal((await emails.getEmail('new-child')).subject, 'New child')
      assert.equal((await store.decode((await store.byExternalId('summary:new-child', 'summary')).item_json)).deleted, false)
    }
  } finally { wait.release(); PortalRecordStore.prototype.commitMany = commit }
}))

test('an account mirror cannot overtake a local edit that began before its first lookup', () => fixture(async ({ loadModule, db, onQuery, setModules }) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const { portalMailStore } = await loadModule('/src/features/email/portal-mail-store.ts')
  await setModules({ email: 'cloud' })
  const store = portalMailStore(db.bindOperation(db.captureOperation({ kind: 'local-edit' })))
  await store.observeEpoch(0)
  const id = await emails.saveAccount({ displayName: 'A', emailAddress: 'a@example.com', imapHost: 'imap.local', password: 'secret' })
  const waiting = gate()
  onQuery(async sql => { if (sql.startsWith('SELECT display_name,email_address,last_synced_at')) await waiting.pause() })
  const edit = emails.updateAccount(id, { displayName: 'Local intent' })
  await waiting.started
  assert.equal(await emails.writeAccountIfNewer({ id, displayName: 'Remote', emailAddress: 'a@example.com', imapHost: 'imap.remote', imapPort: 993, authType: 'password', syncIntervalMin: 15, enabled: true, updatedAt: Math.floor(Date.now() / 1000) + 1 }), false)
  waiting.release()
  await edit
  onQuery(async () => {})
  assert.equal((await emails.getAccount(id)).displayName, 'Local intent')
}))

test('remote metadata edits and tombstones retain original local mail bodies and credentials', () => fixture(async ({ loadModule, db }) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const cache = await loadModule('/src/features/email/email-body-cache.ts')
  const { portalMailStore } = await loadModule('/src/features/email/portal-mail-store.ts')
  const { applyRecordPage } = await loadModule('/src/native/portal-sync/record-engine.ts')
  const store = portalMailStore(db.bindOperation(db.captureOperation({ kind: 'local-edit' })))
  await store.observeEpoch(0)
  const id = await emails.saveAccount({ displayName: 'Local', emailAddress: 'a@example.com', imapHost: 'imap.private', password: 'SECRET' })
  await emails.upsertEmail({ id: 'original', accountId: id, fromAddress: 'sender@example.com', subject: 'Local subject', snippet: 'Local snippet', date: 1, updatedAt: Date.now() })
  await cache.writeEmailBodyLocal('original', 'PRIVATE ORIGINAL BODY')
  const before = await db.queryOne('SELECT credential_encrypted,imap_host FROM local_email_accounts')
  const at = new Date(Date.now() + 1000).toISOString()
  const remote = { module: 'email', kind: 'account', ext_id: `account:${id}`, title: 'Remote display', payload: { address: 'other@example.com', display_name: 'Remote', last_sync_at: '' }, client_updated_at: at, op_id: 'remote-account', deleted: false, epoch: 0, change_seq: 1 }
  await applyRecordPage(store, { epoch: 0, changes: [remote, { ...remote, kind: 'summary', ext_id: 'summary:original', payload: {}, deleted: true, op_id: 'remote-delete', change_seq: 2 }], next_cursor: '2' })
  assert.deepEqual(await db.queryOne('SELECT credential_encrypted,imap_host FROM local_email_accounts'), before)
  assert.equal((await emails.getEmail('original')).subject, 'Local subject')
  assert.equal(await cache.readEmailBodyLocal('original'), 'PRIVATE ORIGINAL BODY')
}))

test('unversioned source classification blocks the older queued metadata without inventing a version', () => fixture(async ({ loadModule, db }) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const { portalMailStore } = await loadModule('/src/features/email/portal-mail-store.ts')
  const store = portalMailStore(db.bindOperation(db.captureOperation({ kind: 'local-edit' })))
  await store.observeEpoch(0)
  const accountId = await emails.saveAccount({ displayName: 'A', emailAddress: 'a@example.com', imapHost: 'imap.local', password: 'secret' })
  await emails.upsertEmail({ id: 'source', accountId, fromAddress: 'from@example.com', subject: 'Original', snippet: 'snippet', date: 1, updatedAt: 1000 })
  const original = (await store.pending()).find(item => item.ext_id === 'summary:source')
  await emails.handleClassifiedEvent({ email_id: 'source', category: 'work', summary: 'New source classification without version' })
  assert.equal((await emails.getEmail('source')).aiSummary, 'New source classification without version')
  assert.equal((await store.pending()).some(item => item.ext_id === 'summary:source'), false)
  const queued = await db.queryOne("SELECT item_json,last_error FROM local_portal_record_outbox WHERE ext_id='summary:source'")
  assert.equal(JSON.parse(queued.item_json).client_updated_at, original.client_updated_at)
  assert.equal(queued.last_error, 'source_timestamp_unknown')
}))

test('remote mail mirrors enqueue only metadata with a confirmed source version', () => fixture(async ({ loadModule, db, respond }) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const { syncAccountsFromServer } = await loadModule('/src/features/email/account-sync.ts')
  const { portalMailStore } = await loadModule('/src/features/email/portal-mail-store.ts')
  const store = portalMailStore(db.bindOperation(db.captureOperation({ kind: 'local-edit' })))
  await store.observeEpoch(0)
  const received = 1_750_000_000
  const revision = 1_780_000_000_123
  const account = { id: 'historical-account', displayName: 'Historical mailbox', emailAddress: 'a@example.com',
    imapHost: 'imap.example.com', imapPort: 993, authType: 'password', createdAt: received, enabled: true }
  const message = { id: 'historical-mail', accountId: account.id, fromAddress: 'original@example.com', subject: 'Historical mail',
    snippet: 'Historical snippet', date: received, updatedAt: received * 1000 }
  respond(url => url.includes('/accounts') ? { accounts: [account] } : { emails: [message] })

  assert.equal((await syncAccountsFromServer()).applied, 1)
  assert.equal((await emails.getAccount(account.id)).updatedAt, 0)
  assert.equal((await emails.syncEmailsFromServerDetailed()).inserted, 1)
  assert.equal((await emails.getEmail(message.id)).subject, message.subject)
  assert.deepEqual(await store.pending(), [])
  const blocked = await db.query("SELECT kind,last_error FROM local_portal_record_outbox WHERE module='email' ORDER BY kind")
  assert.deepEqual(blocked, [
    { kind: 'account', last_error: 'source_timestamp_unknown' },
    { kind: 'summary', last_error: 'source_timestamp_unknown' },
  ])

  const updated = { ...message, fromAddress: 'corrected@example.com', date: received + 1, aiSummary: 'Verified summary', sourceUpdatedAt: revision }
  respond(() => ({ emails: [updated] }))
  assert.equal((await emails.syncEmailsFromServerDetailed()).inserted, 1)
  const [pending] = await store.pending()
  assert.equal(pending.ext_id, 'summary:historical-mail')
  assert.equal(pending.client_updated_at, new Date(revision).toISOString())
  assert.deepEqual(pending.payload, { from: updated.fromAddress, subject: message.subject, snippet: 'Verified summary',
    received_at: new Date(updated.date * 1000).toISOString() })
  assert.equal((await emails.getEmail(message.id)).fromAddress, updated.fromAddress)
}))

test('upgrading holds legacy uploads and retries until the same owner reviews the metadata', () => {
  const timestamp = '2025-06-15T15:06:40.000Z'
  const account = { module: 'email', kind: 'account', ext_id: 'account:legacy', title: 'Legacy mailbox',
    payload: { address: 'legacy@example.com', display_name: 'Legacy mailbox', last_sync_at: '' },
    client_updated_at: timestamp, op_id: 'legacy-account', deleted: false, epoch: 0 }
  const summary = { ...account, kind: 'summary', ext_id: 'summary:legacy', title: 'Legacy mail',
    payload: { from: 'sender@example.com', subject: 'Legacy mail', snippet: 'Saved locally', received_at: timestamp } }
  const tombstone = { ...summary, ext_id: 'summary:legacy-deleted', op_id: 'legacy-delete', payload: {}, deleted: true }
  const rows = [
    ['user-A', account, ''],
    ['user-A', { ...summary, op_id: 'legacy-policy' }, 'policy_local'],
    ['user-A', { ...summary, op_id: 'legacy-clock' }, 'clock_skew_pending'],
    ['user-A', { ...summary, op_id: 'legacy-confirmed' }, 'clock_retry_confirmed'],
    ['user-A', tombstone, ''],
    ['user-B', account, ''],
  ]
  return fixture(async ({ loadModule, db, login, setModules }) => {
    const emails = await loadModule('/src/features/email/emails-store.ts')
    const { versionKey } = await loadModule('/src/native/portal-sync/contract.ts')
    const { syncRecordsOnce } = await loadModule('/src/native/portal-sync/record-engine.ts')
    const { portalMailStore, editPortalMailMetadata, deletePortalMailMetadata, listPortalMailMetadata } = await loadModule('/src/features/email/portal-mail-store.ts')
    const scopedStore = () => portalMailStore(db.bindOperation(db.captureOperation({ kind: 'local-edit' })))
    const store = scopedStore()
    await store.observeEpoch(0)
    await db.run(`INSERT INTO local_email_accounts(id,display_name,email_address,imap_host,credential_encrypted,created_at,updated_at,owner_user_id,owner_workspace_id)
      VALUES ('legacy','Legacy mailbox','legacy@example.com','imap.example.com','SAVED',1,1750000000,'user-A','workspace')`)
    for (const item of [account, tombstone]) {
      await db.run(`INSERT INTO local_portal_records(owner_user_id,owner_workspace_id,module,kind,ext_id,source_id,item_json,version_key,op_id,reset_epoch)
        VALUES ('user-A','workspace','email',?,?,?,?,?,?,0)`,
      [item.kind, item.ext_id, item.ext_id, JSON.stringify(item), versionKey(timestamp), item.op_id])
    }

    await store.resumeSkipped()
    await store.reaffirmClock('2026-11-26T04:00:00.000Z')
    await emails.updateSyncState('legacy', 12)
    assert.equal((await emails.getAccount('legacy')).lastSyncedUid, 12)
    for (const [owner, item, error] of rows) {
      assert.deepEqual(await db.queryOne('SELECT item_json,last_error,source_checked FROM local_portal_record_outbox WHERE owner_user_id=? AND op_id=?',
        [owner, item.op_id]), { item_json: JSON.stringify(item), last_error: error, source_checked: 0 })
    }

    let pushed = []
    const transport = { changes: async () => ({ epochs: { email: 0 }, changes: [], next_cursor: '' }),
      push: async items => { pushed = items; return { results: [] } } }
    await syncRecordsOnce(store, transport, () => {})
    assert.deepEqual(pushed, [])
    await setModules({ email: 'local' })
    const { lobsterReady } = await loadModule('/src/native/lobster-init.ts')
    lobsterReady.value = true
    const { portalEmailRuntime } = await loadModule('/src/features/email/portal-mail-runtime.ts')
    await portalEmailRuntime.syncNow()
    assert.equal(portalEmailRuntime.status.sourceUnchecked, 5)
    assert.equal(portalEmailRuntime.status.clockPending, 0)
    await setModules({ email: 'cloud' })

    await editPortalMailMetadata(account.ext_id, { title: 'Reviewed', payload: account.payload })
    await syncRecordsOnce(store, transport, () => {})
    assert.equal(pushed.length, 1)
    assert.equal(pushed[0].ext_id, account.ext_id)
    assert.equal(pushed[0].title, 'Reviewed')
    assert.notEqual(pushed[0].op_id, account.op_id)
    assert.equal(await db.queryOne("SELECT 1 FROM local_portal_record_outbox WHERE owner_user_id='user-A' AND op_id=?", [account.op_id]), null)

    assert.ok((await listPortalMailMetadata()).some(row => row.id === tombstone.ext_id && row.unchecked && row.item.deleted))
    await deletePortalMailMetadata(tombstone.ext_id)
    const reviewedDelete = (await store.pending()).find(item => item.ext_id === tombstone.ext_id)
    assert.ok(reviewedDelete?.deleted)
    assert.notEqual(reviewedDelete.op_id, tombstone.op_id)
    assert.equal((await listPortalMailMetadata()).some(row => row.id === tombstone.ext_id), false)

    await login('B')
    const b = scopedStore()
    await b.observeEpoch(0)
    assert.deepEqual(await b.pending(), [])
    assert.equal(await b.byExternalId(account.ext_id, 'account'), null)
    assert.equal(await emails.getAccount('legacy'), null)
    assert.equal((await db.queryOne("SELECT source_checked FROM local_portal_record_outbox WHERE owner_user_id='user-B' AND op_id=?", [account.op_id])).source_checked, 0)
  }, { prepareDatabase: raw => {
    // The table and queued rows use the released schema without source metadata.
    raw.run(`CREATE TABLE local_portal_record_outbox (
      owner_user_id TEXT NOT NULL,owner_workspace_id TEXT NOT NULL,module TEXT NOT NULL,kind TEXT NOT NULL,op_id TEXT NOT NULL,ext_id TEXT NOT NULL,
      item_json TEXT NOT NULL,reset_epoch INTEGER,last_error TEXT NOT NULL DEFAULT '',
      PRIMARY KEY(owner_user_id,owner_workspace_id,module,op_id))`)
    for (const [owner, item, error] of rows) {
      raw.run("INSERT INTO local_portal_record_outbox VALUES (?, 'workspace', 'email', ?, ?, ?, ?, 0, ?)",
        [owner, item.kind, item.op_id, item.ext_id, JSON.stringify(item), error])
    }
  } })
})

test('encrypted email metadata and old reset operations stay local until a new explicit intent', () => fixture(async ({ loadModule, db, cryptoConfig }) => {
  const emails = await loadModule('/src/features/email/emails-store.ts')
  const { portalMailStore, editPortalMailMetadata } = await loadModule('/src/features/email/portal-mail-store.ts')
  const { PortalNotesStore } = await loadModule('/src/native/portal-sync/store.ts')
  const { PortalResetStore } = await loadModule('/src/native/portal-sync/reset-store.ts')
  const handle = db.bindOperation(db.captureOperation({ kind: 'local-edit' }))
  const store = portalMailStore(handle)
  await store.observeEpoch(0)
  cryptoConfig.setFieldEncryption('enabled')
  const id = await emails.saveAccount({ displayName: 'PRIVATE DISPLAY', emailAddress: 'private@example.com', imapHost: 'imap.local', password: 'secret' })
  const queued = await db.queryOne("SELECT item_json FROM local_portal_record_outbox WHERE module='email'")
  assert.ok(queued.item_json.startsWith('e1:'))
  assert.equal(queued.item_json.includes('PRIVATE DISPLAY'), false)
  const resets = new PortalResetStore(new PortalNotesStore(handle))
  const intent = await resets.begin(['email'])
  await resets.complete(intent, { reset_id: intent.reset_id, purged: { email: 0 }, epochs: { email: 1 } })
  assert.equal((await store.pending()).length, 0)
  assert.equal((await db.queryOne("SELECT item_json FROM local_portal_record_outbox WHERE module='email'")).item_json, queued.item_json)
  assert.equal((await emails.getAccount(id)).displayName, 'PRIVATE DISPLAY')
  const row = await store.byExternalId(`account:${id}`, 'account')
  await editPortalMailMetadata(row.source_id, { title: 'A new explicit edit', payload: { address: 'private@example.com', display_name: 'A new explicit edit', last_sync_at: '' } })
  const [fresh] = await store.pending()
  assert.equal(fresh.epoch, 1)
  assert.equal(fresh.payload.display_name, 'A new explicit edit')
}))
