import assert from 'node:assert/strict'
import { test } from 'node:test'
import initSqlJs from 'sql.js'
import { PortalNotesStore } from '../store.ts'
import { notePayload } from '../contract.ts'
import { applyPage } from '../apply.ts'
import { syncNotesOnce } from '../engine.ts'
import { PortalResetStore } from '../reset-store.ts'

const SQL = await initSqlJs()
function fixture(owner = { userId: 'A', workspaceId: 'ws' }, raw = new SQL.Database()) {
  raw.run(`CREATE TABLE IF NOT EXISTS local_portal_sync_epochs(owner_user_id TEXT NOT NULL,owner_workspace_id TEXT NOT NULL,module TEXT NOT NULL,epoch INTEGER NOT NULL,PRIMARY KEY(owner_user_id,owner_workspace_id,module))`)
  if(owner) raw.run("INSERT OR IGNORE INTO local_portal_sync_epochs VALUES(?,?,'notes',0)",[owner.userId,owner.workspaceId])
  let current = true
  const query = (sql, values = []) => {
    const statement = raw.prepare(sql)
    try { statement.bind(values); const rows = []; while (statement.step()) rows.push(statement.getAsObject()); return rows } finally { statement.free() }
  }
  const db = { owner, assertCurrent() { if (!current) throw new Error('expired') },
    async run(sql, values = []) { this.assertCurrent(); raw.run(sql, values) },
    async query(sql, values) { this.assertCurrent(); return query(sql, values) },
    async queryOne(sql, values) { return (await this.query(sql, values))[0] ?? null },
    async runInTransaction(statements) { this.assertCurrent(); raw.run('BEGIN'); try { for (const s of statements) raw.run(s.statement, s.values); raw.run('COMMIT') } catch(e) {raw.run('ROLLBACK'); throw e} },
  }
  return { raw, db, store: new PortalNotesStore(db), expire() {current = false} }
}
const change = (id, content, time, op = 'remote') => ({ module: 'notes', kind: 'note', ext_id: id, title: id,
  payload: notePayload({ content, format: 'text' }), client_updated_at: new Date(time).toISOString(), op_id: op, deleted: false, change_seq: 1 })

for (const source of ['note','asset']) test(`${source} retains its external identity through create edit and delete`, async () => {
  const a = fixture(), input = {source,id:'same',title:'Original',payload:notePayload({content:'keep me',format:'text'}),modifiedAt:1000,epoch:0}
  await a.store.commit([],input,true)
  await a.store.commit([],{...input,title:'Edited',modifiedAt:2000},false)
  await a.store.commit([],{...input,payload:{},deleted:true,modifiedAt:3000},false)
  assert.deepEqual((await a.store.pending()).map(item=>item.ext_id),[`${source}:same`,`${source}:same`,`${source}:same`])
})

test('immutable outbox survives reopen, does not claim old data, and isolates account/workspace', async () => {
  const a = fixture()
  await a.store.commit([], { source: 'note', id: 'fresh', title: 'N', payload: notePayload({content:'one',format:'text'}), epoch:0, modifiedAt: 1000 }, true)
  await a.store.commit([], { source: 'note', id: 'old', title: 'Old', payload: notePayload({content:'unknown',format:'text'}), epoch:0, modifiedAt: 1000 }, false)
  const first = await a.store.pending()
  assert.equal(first.length, 1)
  const reopened = fixture(a.db.owner, new SQL.Database(a.raw.export()))
  assert.deepEqual(await reopened.store.pending(), first)
  assert.equal((await fixture({userId:'B',workspaceId:'ws'}, a.raw).store.pending()).length, 0)
  assert.equal((await fixture({userId:'A',workspaceId:'other'}, a.raw).store.pending()).length, 0)
  assert.equal((await fixture(null, a.raw).store.pending()).length, 0)
})

test('partial acknowledgement removes only the matching immutable operation', async () => {
  const a = fixture()
  const snapshot = {source:'note', id:'n',title:'N',payload:notePayload({content:'old',format:'text'}),epoch:0, modifiedAt:1000}
  await a.store.commit([], snapshot, true)
  const [old] = await a.store.pending()
  await a.store.commit([], {...snapshot, payload:notePayload({content:'new',format:'text'}),epoch:0, modifiedAt:2000}, false)
  await a.store.acknowledge(old, {module:'notes',ext_id:old.ext_id,op_id:old.op_id,current_epoch:0,outcome:'applied'})
  const remaining = await a.store.pending()
  assert.equal(remaining.length, 1)
  assert.equal(remaining[0].payload.content, 'new')
  await a.store.acknowledge(remaining[0], {module:'notes',ext_id:remaining[0].ext_id,op_id:'wrong',current_epoch:0,outcome:'applied'})
  assert.equal((await a.store.pending()).length, 1)
})

test('large bodies and ambiguous legacy duplicate outcomes remain visible as failed operations', async () => {
  const a = fixture()
  await a.store.commit([], {source:'note',id:'n',title:'N',payload:notePayload({content:'中'.repeat(90000),format:'text'}),epoch:0, modifiedAt:1000}, true)
  assert.equal((await a.store.pending()).length,0)
  assert.match((await a.db.queryOne('SELECT last_error FROM local_portal_outbox')).last_error,/256 KiB/)
})

test('clock skew and ambiguous duplicate keep the local data and failed immutable operation', async () => {
  for (const outcome of ['duplicate','rejected']) {
    const a = fixture()
    await a.store.commit([], {source:'note',id:'n',title:'N',payload:notePayload({content:'keep me',format:'text'}),epoch:0, modifiedAt:Date.now()+3600000},true)
    const [item] = await a.store.pending()
    await a.store.acknowledge(item,{module:'notes',ext_id:item.ext_id,op_id:item.op_id,current_epoch:0,outcome,clock_skew:outcome==='rejected'})
    const row = await a.db.queryOne('SELECT item_json,last_error FROM local_portal_outbox')
    assert.equal(JSON.parse(row.item_json).client_updated_at,item.client_updated_at)
    assert.equal(JSON.parse(row.item_json).payload.content,'keep me')
    assert.notEqual(row.last_error,'')
  }
})

test('clock skew replaces an immutable operation once using the authoritative acknowledgement time', async () => {
  const a = fixture(); noteTables(a.raw)
  await a.store.commit([], {source:'note',id:'skew',title:'N',payload:notePayload({content:'keep me',format:'text'}),epoch:0,modifiedAt:3600000},true)
  const [original] = await a.store.pending()
  const ack = {...original,current_epoch:0,outcome:'rejected',reason:'clock_skew',clock_skew:true,client_updated_at:'1970-01-01T00:00:01.000000001Z'}
  await a.store.acknowledge(original, ack)
  const [retry] = await a.store.pending()
  assert.ok(retry)
  assert.notEqual(retry.op_id, original.op_id)
  assert.equal(retry.client_updated_at, '1970-01-01T00:00:01.001Z')
  assert.deepEqual(retry.payload, original.payload)
  await a.store.acknowledge(original, ack)
  assert.deepEqual(await a.store.pending(), [retry])
  const transport = {changes:async()=>({changes:[],epochs:{notes:0},next_cursor:''}),push:async items=>({results:items.map(item=>({...item,current_epoch:0,outcome:'applied'}))})}
  assert.equal(await syncNotesOnce(a.store,transport,()=>a.db.assertCurrent()), 1)
  assert.deepEqual(await a.store.pending(), [])
  assert.equal((await a.store.find('note','skew')).op_id, retry.op_id)
})

test('a second clock rejection stays explicit and does not create an endless retry', async () => {
  const a = fixture()
  await a.store.commit([], {source:'note',id:'skew',title:'N',payload:notePayload({content:'keep me',format:'text'}),epoch:0,modifiedAt:3600000},true)
  const [original] = await a.store.pending()
  await a.store.acknowledge(original, {...original,current_epoch:0,outcome:'rejected',clock_skew:true,client_updated_at:new Date(1000).toISOString()})
  const [retry] = await a.store.pending()
  assert.ok(retry)
  await a.store.acknowledge(retry, {...retry,current_epoch:0,outcome:'rejected',clock_skew:true,client_updated_at:new Date(500).toISOString()})
  assert.deepEqual(await a.store.pending(), [])
  const row = await a.db.queryOne('SELECT item_json,last_error FROM local_portal_outbox')
  assert.equal(row.last_error, 'clock_skew_failed')
  assert.equal(JSON.parse(row.item_json).op_id, retry.op_id)
  assert.deepEqual(JSON.parse(row.item_json).payload, original.payload)
})

for (const transition of ['edit', 'reset', 'owner']) test(`a notes clock correction is fenced by a concurrent ${transition}`, async () => {
  const a = fixture()
  const input = {source:'note',id:'skew',title:'Original',payload:notePayload({content:'keep me',format:'text'}),epoch:0,modifiedAt:3600000}
  await a.store.commit([],input,true)
  const [old] = await a.store.pending()
  let release, started
  const wait = new Promise(resolve=>{release=resolve}), entered = new Promise(resolve=>{started=resolve})
  const correcting = new PortalNotesStore(a.db,{encode:async item=>{started();await wait;return JSON.stringify(item)},decode:async json=>JSON.parse(json)})
  const response = correcting.acknowledge(old,{...old,current_epoch:0,outcome:'rejected',clock_skew:true,client_updated_at:new Date(1000).toISOString()})
  await entered
  if (transition === 'edit') await a.store.commit([],{...input,title:'Concurrent intent',modifiedAt:3600001},false)
  else if (transition === 'reset') {
    const resets = new PortalResetStore(a.store), intent = await resets.begin(['notes'])
    await resets.complete(intent,{reset_id:intent.reset_id,purged:{notes:0},epochs:{notes:1}})
  } else a.expire()
  release()
  if (transition === 'owner') await assert.rejects(response,/expired/)
  else await response
  const reopened = fixture(a.db.owner,new SQL.Database(a.raw.export()))
  if (transition === 'edit') assert.equal((await reopened.store.pending())[0].title,'Concurrent intent')
  else if (transition === 'reset') assert.deepEqual(await reopened.store.pending(),[])
  else assert.equal((await a.raw.exec('SELECT COUNT(*) FROM local_portal_outbox'))[0].values[0][0],1)
})

test('an explicit cloud re-enable creates a fresh upload intent for skipped-local operations', async () => {
  const a = fixture()
  await a.store.commit([], {source:'note',id:'n',title:'N',payload:notePayload({content:'saved offline',format:'text'}),epoch:0, modifiedAt:1000},true)
  const [item] = await a.store.pending()
  await a.store.acknowledge(item,{module:'notes',ext_id:item.ext_id,op_id:item.op_id,current_epoch:0,outcome:'skipped_local'})
  assert.equal((await a.store.pending()).length,0)
  await a.store.resumeSkipped()
  const [next] = await a.store.pending()
  assert.notEqual(next.op_id,item.op_id)
  assert.equal(next.client_updated_at,item.client_updated_at)
  assert.deepEqual(next.payload,item.payload)
})

test('concurrent remote materialization sees the newer local version at SQL dispatch', async () => {
  const a = fixture(); noteTables(a.raw)
  const first = change('remote-note','first',1000)
  await applyPage(a.store,{changes:[first],epochs:{notes:0},next_cursor:'one'})
  const record = await a.store.byExternalId(first.ext_id)
  let resume
  const paused = new Promise(resolve=>{resume=resolve})
  let started
  const reached = new Promise(resolve=>{started=resolve})
  const applying = applyPage(a.store,{changes:[change(first.ext_id,'remote second',2000)],epochs:{notes:0},next_cursor:'two'},async content=>{started();await paused;return{content,encrypted:0}})
  await reached
  await a.store.commit([{statement:'UPDATE local_notes SET content=? WHERE id=?',values:['new local',record.source_id]}],
    {source:'note',id:record.source_id,title:'N',payload:notePayload({content:'new local',format:'text'}),epoch:0, modifiedAt:3000},false)
  resume(); await applying
  assert.equal((await a.db.queryOne('SELECT content FROM local_notes')).content,'new local')
})

function noteTables(raw) {
  raw.run(`CREATE TABLE local_notes (id TEXT PRIMARY KEY,workspace_id TEXT,title TEXT,content TEXT,encrypted_content INTEGER,content_type TEXT,domain TEXT,tags TEXT,created_at INTEGER,updated_at INTEGER,status TEXT,summary TEXT,search_text TEXT,storage_tier TEXT,created_by_voice INTEGER,body_path TEXT,deleted_at INTEGER,audio_path TEXT,media_json TEXT)`)
  raw.run(`CREATE TABLE local_assets (id TEXT PRIMARY KEY,workspace_id TEXT,kind TEXT,title TEXT,body_text TEXT,body_encrypted INTEGER,meta_json TEXT,source TEXT,sync_mode TEXT,created_at INTEGER,updated_at INTEGER,dirty INTEGER,deleted_at INTEGER)`)
}

test('remote LWW, equal-time tie and tombstones retain local audio and files', async () => {
  const a = fixture(); noteTables(a.raw)
  await a.store.commit([{statement:`INSERT INTO local_notes (id,workspace_id,content,audio_path,media_json,body_path) VALUES ('n','ws','local','audio.webm','attachments','body.md')`,values:[]}],
    {source:'note',id:'n',title:'N',payload:notePayload({content:'local',format:'text'}),epoch:0, modifiedAt:2000},true)
  const [local] = await a.store.pending()
  await applyPage(a.store,{changes:[change(local.ext_id,'older',1000)],epochs:{notes:0},next_cursor:'one'})
  assert.equal((await a.db.queryOne('SELECT content FROM local_notes')).content,'local')
  const remote = change(local.ext_id,'remote full body',2000,'z')
  await applyPage(a.store,{changes:[remote],epochs:{notes:0},next_cursor:'two'})
  const row = await a.db.queryOne('SELECT * FROM local_notes')
  assert.equal(row.content,'remote full body'); assert.equal(row.audio_path,'audio.webm'); assert.equal(row.media_json,'attachments'); assert.equal(row.body_path,null)
  await applyPage(a.store,{changes:[{...remote,deleted:true,payload:{},client_updated_at:new Date(3000).toISOString()}],epochs:{notes:0},next_cursor:'three'})
  assert.equal((await a.db.queryOne('SELECT * FROM local_notes')).deleted_at,3000)
  assert.equal((await a.db.queryOne('SELECT * FROM local_notes')).audio_path,'audio.webm')
  assert.equal(await a.store.cursor(),'three')
})

test('materialization failure and expired owner cannot advance cursor', async () => {
  const a = fixture()
  await assert.rejects(applyPage(a.store,{changes:[change('missing-table','remote',1000)],epochs:{notes:0},next_cursor:'bad'}),/no such table/)
  assert.equal(await a.store.cursor(),'')
  assert.equal(await a.store.byExternalId('missing-table'),null)
  a.expire()
  await assert.rejects(a.store.pending(),/expired/)
})

test('network retry preserves operation content/time/id and account switching aborts before ack', async () => {
  const a = fixture(); noteTables(a.raw)
  await a.store.commit([], {source:'note',id:'n',title:'N',payload:notePayload({content:'one',format:'text'}),epoch:0, modifiedAt:1000},true)
  const sent = []
  const transport = {changes:async cursor => ({changes:[],epochs:{notes:0},next_cursor:cursor}), push: async items => {sent.push(structuredClone(items)); throw new Error('offline')} }
  await assert.rejects(syncNotesOnce(a.store,transport,()=>a.db.assertCurrent()),/offline/)
  await assert.rejects(syncNotesOnce(a.store,transport,()=>a.db.assertCurrent()),/offline/)
  assert.deepEqual(sent[0],sent[1])
  transport.push = async items => { a.expire(); return {results:items.map(item => ({...item,current_epoch:0,outcome:'applied'}))} }
  await assert.rejects(syncNotesOnce(a.store,transport,()=>a.db.assertCurrent()),/expired/)
  const reopened = fixture(a.db.owner,new SQL.Database(a.raw.export()))
  assert.equal((await reopened.store.pending()).length,1)
})

export { fixture, change }

const snapshot = (epoch = 0, content = 'original private content') => ({source:'note',id:'n',title:'N',payload:notePayload({content,format:'text'}),epoch,modifiedAt:1000})
const resetResult = (intent, epoch = 1) => ({reset_id:intent.reset_id,purged:{notes:1},epochs:{notes:epoch}})

test('cloud reset retains original notes, media and immutable old offline operations', async () => {
  const a=fixture(); noteTables(a.raw)
  await a.store.commit([{statement:"INSERT INTO local_notes(id,workspace_id,content,audio_path,media_json) VALUES('n','ws','original private content','audio.webm','attachments')",values:[]}],snapshot(),true)
  const original=await a.db.queryOne('SELECT item_json FROM local_portal_outbox')
  await a.db.runInTransaction([a.store.cursorStatement('old-cursor',0)])
  const resets=new PortalResetStore(a.store), intent=await resets.begin(['notes'])
  assert.equal((await a.store.pending()).length,0)
  await resets.complete(intent,resetResult(intent))
  assert.equal(await a.store.currentEpoch(),1)
  assert.equal(await a.store.cursor(),'')
  assert.equal((await a.store.pending()).length,0)
  assert.equal((await a.db.queryOne('SELECT item_json FROM local_portal_outbox')).item_json,original.item_json)
  const row=await a.db.queryOne('SELECT * FROM local_notes')
  assert.equal(row.content,'original private content');assert.equal(row.audio_path,'audio.webm');assert.equal(row.media_json,'attachments')
})

test('first-device operations without epoch never acquire one from a later pull', async () => {
  const a=fixture();noteTables(a.raw)
  await a.db.run('DELETE FROM local_portal_sync_epochs')
  await a.store.commit([],snapshot(null),true)
  const old=await a.db.queryOne('SELECT item_json FROM local_portal_outbox')
  let pushes=0
  await syncNotesOnce(a.store,{changes:async()=>({changes:[],next_cursor:'',epochs:{notes:0}}),push:async()=>{pushes++;return{results:[]}}},()=>a.db.assertCurrent())
  assert.equal(pushes,0)
  assert.equal((await a.db.queryOne('SELECT item_json FROM local_portal_outbox')).item_json,old.item_json)
  assert.equal(JSON.parse(old.item_json).epoch,null)
  await a.store.commit([],snapshot(0,'explicit user save after initialization'),false)
  assert.equal((await a.store.pending())[0].payload.content,'explicit user save after initialization')
})

test('observing another device reset blocks old offline queue rather than rebasing it', async () => {
  const a=fixture();noteTables(a.raw)
  await a.store.commit([],snapshot(),true)
  let pushes=0
  await syncNotesOnce(a.store,{changes:async()=>({changes:[],next_cursor:'',epochs:{notes:1}}),push:async()=>{pushes++;return{results:[]}}},()=>a.db.assertCurrent())
  assert.equal(pushes,0)
  const row=await a.db.queryOne('SELECT item_json,last_error FROM local_portal_outbox')
  assert.equal(JSON.parse(row.item_json).epoch,0)
  assert.equal(row.last_error,'reset_epoch_mismatch')
})

test('uncertain purge retries reuse a persistent reset id and stay scoped to their owner', async () => {
  const a=fixture(), resets=new PortalResetStore(a.store)
  const first=await resets.begin(['notes'])
  const reopened=fixture(a.db.owner,new SQL.Database(a.raw.export()))
  assert.equal((await new PortalResetStore(reopened.store).begin(['notes'])).reset_id,first.reset_id)
  const b=fixture({userId:'B',workspaceId:'ws'},a.raw)
  assert.notEqual((await new PortalResetStore(b.store).begin(['notes'])).reset_id,first.reset_id)
  await assert.rejects(resets.complete(first,{...resetResult(first),reset_id:'wrong'}),/Invalid trusted/)
  assert.equal((await a.db.queryOne('SELECT completed FROM local_portal_resets WHERE reset_id=?',[first.reset_id])).completed,0)
})

test('saving across an encryption await and cloud reset keeps its original epoch blocked', async () => {
  const a=fixture()
  let resume,enter
  const paused=new Promise(resolve=>{resume=resolve}),started=new Promise(resolve=>{enter=resolve})
  const writer=new PortalNotesStore(a.db,{encode:async item=>{enter();await paused;return JSON.stringify(item)},decode:async value=>JSON.parse(value)})
  const saving=writer.commit([],snapshot(),true)
  await started
  const resets=new PortalResetStore(a.store),intent=await resets.begin(['notes'])
  await resets.complete(intent,resetResult(intent));resume();await saving
  assert.equal((await a.store.pending()).length,0)
  const row=await a.db.queryOne('SELECT item_json,last_error FROM local_portal_outbox')
  assert.equal(JSON.parse(row.item_json).epoch,0);assert.equal(row.last_error,'reset_epoch_mismatch')
})

test('late applied acknowledgement and old change page cannot undo a completed reset', async () => {
  const a=fixture();noteTables(a.raw)
  await a.store.commit([{statement:"INSERT INTO local_notes(id,workspace_id,content) VALUES('n','ws','original')",values:[]}],snapshot(),true)
  const [old]=await a.store.pending()
  const resets=new PortalResetStore(a.store),intent=await resets.begin(['notes'])
  await resets.complete(intent,resetResult(intent))
  assert.equal(await a.store.acknowledge(old,{module:'notes',ext_id:old.ext_id,op_id:old.op_id,outcome:'applied',current_epoch:0}),false)
  await assert.rejects(applyPage(a.store,{changes:[change(old.ext_id,'old response',2000)],next_cursor:'stale',epochs:{notes:0}}),/Stale portal reset/)
  assert.equal((await a.db.queryOne('SELECT content FROM local_notes')).content,'original')
  assert.equal(await a.store.cursor(),'')
  assert.equal((await a.db.queryOne('SELECT COUNT(*) AS n FROM local_portal_outbox')).n,1)
})

test('an epoch mismatch acknowledgement fences the operation and retains the original payload', async () => {
  const a=fixture();await a.store.commit([],snapshot(),true)
  const [old]=await a.store.pending()
  assert.equal(await a.store.acknowledge(old,{module:'notes',ext_id:old.ext_id,op_id:old.op_id,outcome:'rejected',reason:'reset_epoch_mismatch',current_epoch:2}),false)
  assert.equal(await a.store.currentEpoch(),2)
  const row=await a.db.queryOne('SELECT item_json,last_error FROM local_portal_outbox')
  assert.equal(JSON.parse(row.item_json).epoch,0);assert.equal(JSON.parse(row.item_json).payload.content,'original private content')
  assert.equal(row.last_error,'reset_epoch_mismatch')
})

test('legacy outbox migration preserves unknown epoch operations without assigning current epoch',async()=>{
  const a=fixture()
  a.raw.run(`CREATE TABLE local_portal_outbox(owner_user_id TEXT NOT NULL,owner_workspace_id TEXT NOT NULL,op_id TEXT NOT NULL,ext_id TEXT NOT NULL,item_json TEXT NOT NULL,last_error TEXT NOT NULL DEFAULT '',PRIMARY KEY(owner_user_id,owner_workspace_id,op_id))`)
  const old=JSON.stringify({module:'notes',kind:'note',ext_id:'note:n',op_id:'old-op',client_updated_at:'2026-01-01T00:00:00Z',title:'Old',payload:{content:'retain original'},deleted:false})
  a.raw.run('INSERT INTO local_portal_outbox VALUES(?,?,?,?,?,?)',['A','ws','old-op','note:n',old,''])
  await a.store.observeEpoch(3)
  assert.equal((await a.store.pending()).length,0)
  const row=await a.db.queryOne('SELECT item_json,reset_epoch,last_error FROM local_portal_outbox')
  assert.equal(row.item_json,old);assert.equal(row.reset_epoch,null);assert.equal(row.last_error,'epoch_unknown')
})

test('a prepared old pull cannot write source rows or cursor after concurrent reset',async()=>{
  const a=fixture();noteTables(a.raw)
  await a.store.commit([{statement:"INSERT INTO local_notes(id,workspace_id,content) VALUES('n','ws','original')",values:[]}],snapshot(),true)
  const [item]=await a.store.pending()
  let resume,enter;const paused=new Promise(resolve=>{resume=resolve}),started=new Promise(resolve=>{enter=resolve})
  const applying=applyPage(a.store,{changes:[change(item.ext_id,'late remote',2000)],next_cursor:'late',epochs:{notes:0}},async content=>{enter();await paused;return{content,encrypted:0}})
  await started
  const resets=new PortalResetStore(a.store),intent=await resets.begin(['notes'])
  await resets.complete(intent,resetResult(intent));resume()
  await assert.rejects(applying,/reset is pending or changed/)
  assert.equal((await a.db.queryOne('SELECT content FROM local_notes')).content,'original')
  assert.equal(await a.store.cursor(),'')
})

test('late replay of an older successful reset never decreases epoch or current cursor',async()=>{
  const a=fixture(),resets=new PortalResetStore(a.store)
  const first=await resets.begin(['notes']);await resets.complete(first,resetResult(first,1))
  const second=await resets.begin(['notes']);await resets.complete(second,resetResult(second,2))
  await a.db.runInTransaction([a.store.cursorStatement('current-epoch-two',2)])
  await resets.complete(first,resetResult(first,1))
  assert.equal(await a.store.currentEpoch(),2);assert.equal(await a.store.cursor(),'current-epoch-two')
})
