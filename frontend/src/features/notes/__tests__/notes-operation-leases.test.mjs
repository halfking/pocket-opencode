import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture, gate, until } from '../../../test-support/notes-sqlite-fixture.mjs'

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

test('real notes CRUD queues full content and tombstones, with exact owner isolation', async () => fixture(async f => {
  await f.setMode('local')
  const note = await f.notes.createNote({content:'private complete body',workspaceId:'workspace'})
  const bind = () => f.db.bindOperation(f.db.captureOperation({kind:'local-edit'}))
  const store = () => f.portalNotesStore(bind())
  assert.equal((await store().pending())[0].payload.content,'private complete body')
  await f.notes.updateNote(note.id,{content:'updated full body'},'workspace')
  const writes = await store().pending()
  assert.equal(writes.length,2)
  assert.equal(writes[1].payload.content,'updated full body')
  await f.login('B')
  assert.equal(await f.notes.getNote(note.id,false,'workspace'),null)
  assert.equal((await store().pending()).length,0)
  await f.login('A')
  await f.setMode('local')
  await f.notes.deleteNote(note.id,'workspace')
  assert.equal((await store().pending()).at(-1).deleted,true)
  assert.equal(f.requests.length,0)
}))

test('real PKM assets queue HTML and preserve unclaimed history and encrypted outbox', async () => fixture(async f => {
  await f.setMode('local')
  f.cryptoConfig.setFieldEncryption('enabled')
  const a = await f.assetStore.upsert({kind:'note',title:'PKM',bodyText:'<p>完整正文</p>',metaJson:JSON.stringify({links:['link'],tags:['tag'],password:'never send'}),workspaceId:'workspace'})
  const store = f.portalNotesStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})))
  const [item] = await store.pending()
  assert.equal(item.payload.content,'<p>完整正文</p>')
  assert.equal(item.payload.format,'html')
  assert.equal('password' in item.payload,false)
  assert.match((await f.db.queryOne('SELECT item_json FROM local_portal_outbox')).item_json,/^e1:/)
  await f.assetStore.softDeleteForWorkspace(a.id,'workspace')
  assert.equal((await store.pending()).at(-1).deleted,true)
  await f.db.run("INSERT INTO local_assets(id,workspace_id,kind,title,body_text,sync_mode,created_at,updated_at) VALUES ('historic','workspace','note','Old','unknown','e2ee_local_first',1,1)")
  await f.assetStore.upsert({id:'historic',kind:'note',title:'edited old',bodyText:'edited',workspaceId:'workspace'})
  assert.equal((await store.pending()).length,2,'editing historical data does not assign it to the current account')
}))

test('notes and PKM reject an explicit workspace that differs from the captured verified owner', async () => fixture(async f => {
  await assert.rejects(f.notes.createNote({content:'wrong scope',workspaceId:'other'}),/归属不一致/)
  await assert.rejects(f.assetStore.upsert({kind:'note',bodyText:'wrong scope',workspaceId:'other'}),/verified owner/)
  assert.equal(f.count('local_notes'),0)
  assert.equal(f.count('local_assets'),0)
  const note = await f.notes.createNote({content:'trusted workspace default'})
  assert.equal(note.workspaceId,'workspace')
  await assert.rejects(f.notes.updateNote(note.id,{content:'wrong edit'},'other'),/归属不一致/)
  await assert.rejects(f.notes.deleteNote(note.id,'other'),/归属不一致/)
}))


test('SQL ownership precedes pagination, same-title and daily-note lookup', async () => fixture(async f => {
  await f.setMode('local')
  const a = await f.notes.createNote({title:'A own',content:'A',workspaceId:'workspace'})
  const assetA = await f.assetStore.upsert({kind:'note',title:'Same',bodyText:'A',workspaceId:'workspace',metaJson:JSON.stringify({dailyDate:'2026-10-30',same:'key'})})
  await f.login('B'); await f.setMode('local')
  for(let i=0;i<3;i++) {
    await f.notes.createNote({title:'B newer '+i,content:'B',workspaceId:'workspace'})
    await f.assetStore.upsert({kind:'note',title:'Same',bodyText:'B',workspaceId:'workspace',metaJson:JSON.stringify({dailyDate:'2026-10-30',same:'key'})})
  }
  await f.login('A'); await f.setMode('local')
  assert.equal((await f.notes.listNotes({workspaceId:'workspace',limit:1}))[0].id,a.id)
  assert.equal((await f.assetStore.search({kind:'note',workspaceId:'workspace',limit:1}))[0].id,assetA.id)
  assert.equal((await f.pkm.findByTitle('Same','workspace')).id,assetA.id)
  assert.equal((await f.pkm.getDailyNote('2026-10-30','workspace')).id,assetA.id)
  assert.equal((await f.assetStore.findByMeta({workspaceId:'workspace',kind:'note',key:'same',value:'key'})).id,assetA.id)
}))

test('ENEX without a source modification time stays local instead of inventing one', async () => fixture(async f => {
  await f.setMode('local')
  const asset = await f.assetStore.upsert({kind:'note',title:'Imported',bodyText:'# Markdown\nComplete **original**',source:'enex_import',workspaceId:'workspace'})
  const store=f.portalNotesStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})))
  assert.equal((await store.pending()).length,0)
  const failed=await f.db.queryOne('SELECT item_json,last_error FROM local_portal_outbox')
  assert.match(failed.last_error,/modification time is unknown/)
  assert.equal(JSON.parse(failed.item_json).payload.format,'markdown')
  assert.equal((await f.assetStore.getForWorkspace(asset.id,'workspace')).bodyText,'# Markdown\nComplete **original**')
}))

test('business save captures reset epoch before encryption and does not adopt a later reset',async()=>fixture(async f=>{
  await f.setMode('local');f.cryptoConfig.setFieldEncryption('enabled')
  const paused=gate();f.onEncrypt(paused.pause)
  const saving=f.notes.createNote({content:'private original during reset',workspaceId:'workspace'})
  await paused.started
  const store=f.portalNotesStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})))
  await store.observeEpoch(1);paused.release();const note=await saving
  const row=await f.db.queryOne('SELECT item_json,last_error FROM local_portal_outbox')
  const item=await store.decode(row.item_json)
  assert.equal(item.epoch,0);assert.equal(row.last_error,'reset_epoch_mismatch')
  assert.equal((await store.pending()).length,0)
  assert.equal((await f.notes.getNote(note.id,false,'workspace')).content,'private original during reset')
}))

test('fresh verified device saves locally until epoch is known, with no automatic queue upgrade',async()=>fixture(async f=>{
  await f.auth.setAuthWithWorkspace('fresh-token','Fresh','fresh-user','fresh-workspace');await f.setMode('local')
  const note=await f.notes.createNote({content:'first offline original',workspaceId:'fresh-workspace'})
  const store=f.portalNotesStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})))
  const before=await f.db.queryOne('SELECT item_json FROM local_portal_outbox')
  assert.equal((await store.decode(before.item_json)).epoch,null)
  await store.observeEpoch(0)
  assert.equal((await store.pending()).length,0)
  assert.equal((await f.db.queryOne('SELECT item_json FROM local_portal_outbox')).item_json,before.item_json)
  assert.equal((await f.notes.getNote(note.id,false,'fresh-workspace')).content,'first offline original')
}))
