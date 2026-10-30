import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture } from '../../../test-support/notes-sqlite-fixture.mjs'

const identities = [
  ['/src/features/meetings/meeting-sync-adapter.ts','meetingSyncAdapter','record','same','meeting:same',{held_at:'',attendees:[],minutes:''}],
  ['/src/features/email/portal-mail-adapter.ts','portalMailAdapter','summary','summary:same','summary:same',{from:'a@example.com',subject:'Subject',snippet:'Summary',received_at:''}],
  ['/src/features/email/portal-mail-adapter.ts','portalMailAdapter','account','account:same','account:same',{address:'a@example.com',display_name:'A',last_sync_at:''}],
  ['/src/features/rss/rss-sync-adapter.ts','rssSyncAdapter','source','same','rss-source:same',{url:'https://example.com/feed',category:''}],
  ['/src/features/rss/rss-sync-adapter.ts','rssSyncAdapter','item','same','rss-item:same',{source:'Feed',summary:'Summary',url:'https://example.com/item'}],
  ['/src/features/rss/rss-sync-adapter.ts','rssSyncAdapter','item-state','state:pocketd-item:same','pocketd-item:same',{item_ext_id:'pocketd-item:same',status:'read'}],
]
for (const [path,name,kind,id,extId,payload] of identities) test(`${name}/${kind} uses one external identity for create, edit and delete`, () => fixture(async f => {
  const { PortalRecordStore } = await f.loadModule('/src/native/portal-sync/record-store.ts')
  const store = new PortalRecordStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})),(await f.loadModule(path))[name])
  await store.observeEpoch(0)
  const input = {id,kind,title:'Original',payload,modifiedAt:1000,epoch:0}
  await store.commit([],input,true)
  await store.commit([],{...input,title:'Edited',modifiedAt:2000},false)
  await store.commit([],{...input,payload:{},deleted:true,modifiedAt:3000},false)
  const pending = await store.pending()
  assert.equal(pending.length,3)
  assert.deepEqual(pending.map(item=>item.ext_id),[extId,extId,extId])
}))

for (const [path,name,kind,id,,payload] of identities) test(`${name}/${kind} preserves a managed legacy identity without claiming history`, () => fixture(async f => {
  const { PortalRecordStore } = await f.loadModule('/src/native/portal-sync/record-store.ts')
  const store = new PortalRecordStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})),(await f.loadModule(path))[name])
  await store.observeEpoch(0)
  const extId = kind === 'item-state' ? payload.item_ext_id : `legacy:${id}`
  const input = {id,kind,extId,title:'Original',payload,modifiedAt:1000,epoch:0}
  await store.commit([],input,true)
  await store.commit([],{...input,extId:'new-prefix',title:'Edited',modifiedAt:2000},false)
  await store.commit([],{...input,extId:'another-prefix',payload:{},deleted:true,modifiedAt:3000},false)
  await store.commit([],{...input,id:'unknown-history',modifiedAt:4000},false)
  assert.deepEqual((await store.pending()).map(item=>item.ext_id),[extId,extId,extId])
  assert.equal(await store.find('unknown-history'),null)
}))
