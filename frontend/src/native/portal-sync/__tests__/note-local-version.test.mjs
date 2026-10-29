import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture, gate } from '../../../test-support/notes-sqlite-fixture.mjs'

for(const imported of [false,true])test(`same-millisecond ${imported?'Markdown':'ordinary'} edits keep the later local intention without changing time`,async()=>fixture(async f=>{
  await f.setMode('local')
  const note=imported?await f.assetStore.upsert({kind:'note',title:'Original',bodyText:'# original',source:'enex_import',workspaceId:'workspace',metaJson:JSON.stringify({originalUpdated:1000})}):await f.notes.createNote({title:'Original',content:'original',workspaceId:'workspace'})
  const store=f.portalNotesStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})))
  const originalNow=Date.now,originalRandom=Object.getOwnPropertyDescriptor(crypto,'randomUUID'),at=Date.now()+1000,ids=['z-first','a-second']
  Date.now=()=>at;Object.defineProperty(crypto,'randomUUID',{configurable:true,value:()=>ids.shift()??'fallback'})
  try{await f.notes.updateNote(note.id,{title:'First'},'workspace');await f.notes.updateNote(note.id,{title:'Second'},'workspace')}
  finally{Date.now=originalNow;if(originalRandom)Object.defineProperty(crypto,'randomUUID',originalRandom);else delete crypto.randomUUID}
  const items=(await store.pending()).filter(i=>i.client_updated_at===new Date(at).toISOString())
  const {compareVersions}=await f.loadModule('/src/native/portal-sync/contract.ts')
  assert.equal(items.length,2)
  const winner=items.reduce((a,b)=>compareVersions(a,b)>0?a:b)
  assert.equal(winner.title,'Second')
  assert.equal((await f.notes.getNote(note.id,false,'workspace')).title,'Second')
}))

test('a competing remote version during encryption rolls back all source statements and the local outbox',async()=>fixture(async f=>{
  await f.setMode('local')
  const asset=await f.assetStore.upsert({kind:'note',title:'Original',bodyText:'# original',source:'enex_import',workspaceId:'workspace',metaJson:JSON.stringify({originalUpdated:1000})})
  const db=f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})),normal=f.portalNotesStore(db)
  const {PortalNotesStore}=await f.loadModule('/src/native/portal-sync/store.ts'),{notePayload}=await f.loadModule('/src/native/portal-sync/contract.ts'),{applyPage}=await f.loadModule('/src/native/portal-sync/apply.ts')
  const paused=gate(),local=new PortalNotesStore(db,{encode:async item=>{await paused.pause();return JSON.stringify(item)},decode:async json=>JSON.parse(json)})
  await db.run('CREATE TABLE note_batch_marker(value TEXT)')
  const before=f.count('local_portal_outbox')
  const saving=local.commit([{statement:'UPDATE local_assets SET title=? WHERE id=?',values:['Stale local',asset.id]},{statement:"INSERT INTO note_batch_marker VALUES('should rollback')",values:[]}],{source:'asset',id:asset.id,title:'Stale local',payload:notePayload({format:'markdown',content:'# stale'}),modifiedAt:2000,epoch:0},false);saving.catch(()=>{})
  await paused.started
  const row=await normal.find('asset',asset.id)
  await applyPage(normal,{changes:[{module:'notes',kind:'note',ext_id:row.ext_id,title:'Remote winner',payload:notePayload({format:'markdown',content:'# remote'}),client_updated_at:new Date(3000).toISOString(),op_id:'remote',epoch:0,deleted:false,change_seq:1}],epochs:{notes:0},next_cursor:'remote'})
  paused.release();await assert.rejects(saving,/Note changed/)
  assert.equal((await f.assetStore.getForWorkspace(asset.id,'workspace')).title,'Remote winner')
  assert.equal(f.count('note_batch_marker'),0)
  assert.equal(f.count('local_portal_outbox'),before)
}))

test('an operation that cannot exceed the observed 200-byte id is rejected before changing its source',async()=>fixture(async f=>{
  await f.setMode('local')
  const asset=await f.assetStore.upsert({kind:'note',title:'Original',bodyText:'# original',source:'enex_import',workspaceId:'workspace',metaJson:JSON.stringify({originalUpdated:1000})})
  const db=f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})),store=f.portalNotesStore(db),row=await store.find('asset',asset.id)
  const id='\u{10ffff}'.repeat(50),item=await store.decode(row.item_json)
  await db.run('UPDATE local_portal_notes SET op_id=?,item_json=? WHERE ext_id=?',[id,await store.encode({...item,op_id:id}),row.ext_id])
  const originalNow=Date.now;Date.now=()=>1000
  try{await assert.rejects(f.notes.updateNote(asset.id,{title:'Must stay in editor'},'workspace'),/Note changed/)}finally{Date.now=originalNow}
  assert.equal((await f.assetStore.getForWorkspace(asset.id,'workspace')).title,'Original')
}))

test('ordinary notes retain the initiating modification time across encryption waits',async()=>fixture(async f=>{
  await f.setMode('local')
  const note=await f.notes.createNote({title:'Original',content:'original',workspaceId:'workspace'})
  f.cryptoConfig.setFieldEncryption('enabled');const paused=gate();f.onEncrypt(paused.pause)
  const originalNow=Date.now,at=Date.now()+1000;Date.now=()=>at
  try{
    const saving=f.notes.updateNote(note.id,{content:'new body'},'workspace')
    await paused.started;Date.now=()=>at+60000;paused.release();await saving
    const store=f.portalNotesStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'}))),row=await store.find('note',note.id),item=await store.decode(row.item_json)
    assert.equal(item.client_updated_at,new Date(at).toISOString())
  }finally{Date.now=originalNow}
}))

for(const imported of [false,true])test(`source decryption cannot adopt a later ${imported?'Markdown':'ordinary'} version as its edit base`,async()=>fixture(async f=>{
  await f.setMode('local');f.cryptoConfig.setFieldEncryption('enabled')
  const base=Date.now()-10000,realNow=Date.now;Date.now=()=>base
  let note
  try{note=imported?await f.assetStore.upsert({kind:'note',title:'Original',bodyText:'old private body',encryptBody:true,source:'enex_import',workspaceId:'workspace',metaJson:JSON.stringify({originalUpdated:base})}):await f.notes.createNote({title:'Original',content:'old private body',workspaceId:'workspace'})}finally{Date.now=realNow}
  const db=f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})),store=f.portalNotesStore(db),row=await store.find(imported?'asset':'note',note.id),old=await store.decode(row.item_json)
  const paused=gate();f.onDecrypt(paused.pause)
  const saving=f.notes.updateNote(note.id,{title:'Local title'},'workspace');saving.catch(()=>{})
  await paused.started
  const {applyPage}=await f.loadModule('/src/native/portal-sync/apply.ts')
  await applyPage(store,{changes:[{...old,title:'Remote title',payload:{...old.payload,content:'new remote body'},client_updated_at:new Date(base+1000).toISOString(),op_id:'remote-version',change_seq:1}],epochs:{notes:0},next_cursor:'remote'})
  paused.release();await assert.rejects(saving,/Note changed/);f.onDecrypt(async()=>{})
  assert.equal((await f.notes.getNote(note.id,false,'workspace')).content,'new remote body')
}))
