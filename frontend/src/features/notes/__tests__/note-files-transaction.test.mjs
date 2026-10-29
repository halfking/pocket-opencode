import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture, gate } from '../../../test-support/notes-sqlite-fixture.mjs'

const original='Original full body\n'.repeat(100),replacement='Replacement full body\n'.repeat(100)

test('interrupted new media notes retain a complete owned inline original and never publish incomplete attachments',async()=>fixture(async f=>{
  await f.setMode('local');f.onRead(async()=>{throw new Error('media conversion failed')})
  await assert.rejects(f.notes.createNote({title:'Interrupted',content:original,workspaceId:'workspace',media:[{kind:'image',blob:new Blob(['private image'],{type:'image/png'})}]}),/media conversion failed/)
  const row=await f.db.queryOne('SELECT id FROM local_notes'),note=await f.notes.getNote(row.id,false,'workspace')
  assert.equal(note.bodyPath,null);assert.equal(await f.notes.loadFullContent(note),original)
  assert.equal(f.count('local_note_files'),0)
  const store=f.portalNotesStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})))
  assert.equal((await store.pending()).length,0)
  assert.match((await f.db.queryOne('SELECT last_error FROM local_portal_outbox')).last_error,/attachments.*not yet committed/)
  await f.login('B');assert.equal(await f.notes.getNote(row.id,false,'workspace'),null)
}))

test('a later edit of a partially saved new note is not overwritten when its original media finishes',async()=>fixture(async f=>{
  await f.setMode('local');const paused=gate();f.onRead(paused.pause)
  const creating=f.notes.createNote({title:'Staging',content:original,workspaceId:'workspace',media:[{kind:'image',blob:new Blob(['private image'],{type:'image/png'})}]});creating.catch(()=>{})
  await paused.started
  const row=await f.db.queryOne('SELECT id FROM local_notes')
  await f.notes.updateNote(row.id,{title:'Later user edit',content:'User replacement'},'workspace')
  paused.release();await assert.rejects(creating,/Note changed/)
  const saved=await f.notes.getNote(row.id,false,'workspace')
  assert.equal(saved.title,'Later user edit');assert.equal(await f.notes.loadFullContent(saved),'User replacement')
  assert.equal(f.count('local_note_files'),0)
}))

for(const next of [replacement,'short replacement'])test(`failed ${next===replacement?'file':'inline'} edit leaves the previous body and attachment bytes reachable`,async()=>fixture(async f=>{
  await f.setMode('local')
  const note=await f.notes.createNote({title:'Original',content:original,workspaceId:'workspace',media:[{kind:'image',blob:new Blob(['original image'],{type:'image/png'})}]})
  const files=f.raw.exec('SELECT id,rel_path,data_base64 FROM local_note_files ORDER BY id')[0].values
  f.cryptoConfig.setFieldEncryption('enabled');f.onEncrypt(async()=>{throw new Error('encryption failed')})
  await assert.rejects(f.notes.updateNote(note.id,{title:'Rejected',content:next,media:[{kind:'image',blob:new Blob(['replacement image'],{type:'image/png'})}]},'workspace'),/encryption failed/)
  const saved=await f.notes.getNote(note.id,false,'workspace')
  assert.equal(saved.title,'Original');assert.equal(saved.bodyPath,note.bodyPath)
  assert.equal(await f.notes.loadFullContent(saved),original)
  assert.deepEqual(f.raw.exec('SELECT id,rel_path,data_base64 FROM local_note_files ORDER BY id')[0].values,files)
}))

test('source transaction failure rolls back staged file rows and retry publishes a complete immutable revision',async()=>fixture(async f=>{
  await f.setMode('local')
  const note=await f.notes.createNote({title:'Original',content:original,workspaceId:'workspace'})
  const before=f.count('local_note_files')
  await f.db.run("CREATE TRIGGER reject_note_update BEFORE UPDATE ON local_notes WHEN NEW.title='Rejected' BEGIN SELECT RAISE(ABORT,'source transaction failed'); END")
  await assert.rejects(f.notes.updateNote(note.id,{title:'Rejected',content:replacement},'workspace'),/source transaction failed/)
  assert.equal(f.count('local_note_files'),before)
  assert.equal(await f.notes.loadFullContent(await f.notes.getNote(note.id,false,'workspace')),original)
  await f.db.run('DROP TRIGGER reject_note_update')
  await f.notes.updateNote(note.id,{title:'Retry',content:replacement},'workspace')
  const saved=await f.notes.getNote(note.id,false,'workspace')
  assert.notEqual(saved.bodyPath,note.bodyPath)
  assert.equal(await f.notes.loadFullContent(saved),replacement)
}))

test('a remote winner during file editing retains the former file bytes and rejects the stale pointer swap',async()=>fixture(async f=>{
  await f.setMode('local')
  const note=await f.notes.createNote({title:'Original',content:original,workspaceId:'workspace'})
  const {readNoteBody}=await f.loadModule('/src/features/notes/note-files.ts')
  f.cryptoConfig.setFieldEncryption('enabled');const paused=gate();f.onEncrypt(paused.pause)
  const saving=f.notes.updateNote(note.id,{title:'Stale',content:replacement},'workspace');saving.catch(()=>{})
  await paused.started
  const store=f.portalNotesStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'}))),row=await store.find('note',note.id),old=await store.decode(row.item_json)
  const {applyPage}=await f.loadModule('/src/native/portal-sync/apply.ts')
  await applyPage(store,{changes:[{...old,title:'Remote',payload:{...old.payload,content:'Remote winner'},client_updated_at:new Date(Date.now()+1000).toISOString(),op_id:'remote',change_seq:1}],epochs:{notes:0},next_cursor:'remote'})
  paused.release();await assert.rejects(saving,/Note changed/)
  const saved=await f.notes.getNote(note.id,false,'workspace')
  assert.equal(saved.title,'Remote');assert.equal(await f.notes.loadFullContent(saved),'Remote winner')
  assert.equal(await readNoteBody(note.bodyPath),original)
}))
