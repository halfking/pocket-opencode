import test from 'node:test'
import assert from 'node:assert/strict'
import {fixture} from '../../../test-support/notes-sqlite-fixture.mjs'

const event=(sequence,patch={})=>({sequence,kind:'source',ext_id:'pocketd-source:source-1',title:'Feed',payload:{url:'https://example.test/feed',category:'C'},client_updated_at:'2026-01-01T00:00:00.123456Z',op_id:`pocketd-rss:00000000-0000-4000-8000-${String(sequence).padStart(12,'0')}`,epoch:0,deleted:false,intent:'insert',fields:['title','url','category'],sync_error:'',...patch})
const page=(events)=>({user_id:'user-A',workspace_id:'workspace',changes:events,next_cursor:events.at(-1)?.sequence??0})

test('native RSS events retain source time and immutable identity through durable owner-scoped checkpoints',async()=>{
 await fixture(async f=>{
  const rss=await f.loadModule('/src/features/rss/rss-sync-store.ts'),native=await f.loadModule('/src/features/rss/rss-native-ingest.ts')
  const db=f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})),store=rss.rssPortalStore(db);await store.observeEpoch(0)
  await native.applyNativeRssPage(db,page([event(1)]),0)
  const queued=await store.pending();assert.equal(queued.length,1);assert.equal(queued[0].client_updated_at,'2026-01-01T00:00:00.123456Z');assert.equal(queued[0].op_id,event(1).op_id)
  assert.equal(await native.nativeRssCursor(db),1)
  await assert.rejects(native.applyNativeRssPage(db,page([event(1)]),0))
  assert.equal((await store.pending()).length,1)
  await f.login('B');const other=f.db.bindOperation(f.db.captureOperation({kind:'local-edit'}))
  assert.equal(await native.nativeRssCursor(other),0)
  await assert.rejects(native.applyNativeRssPage(other,page([event(2)]),0),/scope/)
 })
})

test('unknown feed timestamps and stale epochs are retained blocked, never replaced with download time or fresh epoch',async()=>{
 await fixture(async f=>{
  const rss=await f.loadModule('/src/features/rss/rss-sync-store.ts'),native=await f.loadModule('/src/features/rss/rss-native-ingest.ts')
  const db=f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})),store=rss.rssPortalStore(db);await store.observeEpoch(1)
  const dated=event(1),unknown=event(2,{kind:'item',ext_id:'pocketd-item:i',client_updated_at:null,payload:{source:'s',url:'https://example.test/i',summary:'saved'},intent:'feed',sync_error:'source_time_unknown'})
  await native.applyNativeRssPage(db,page([dated,unknown]),0)
  assert.equal((await store.pending()).length,0);assert.deepEqual(await rss.listRssPortalRecords(),[])
  assert.equal(await native.nativeRssBlockedCount(db),2)
  const rows=await db.query('SELECT item_json FROM local_rss_native_events ORDER BY sequence')
  const one=await store.decode(rows[0].item_json),two=await store.decode(rows[1].item_json)
  assert.equal(one.epoch,0);assert.equal(two.client_updated_at,'');assert.equal(two.payload.summary,'saved')
 })
})

test('native feed refresh does not overwrite a user portal edit and title-only source edits preserve independent metadata',async()=>{
 await fixture(async f=>{
  const rss=await f.loadModule('/src/features/rss/rss-sync-store.ts'),native=await f.loadModule('/src/features/rss/rss-native-ingest.ts')
  const db=f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})),store=rss.rssPortalStore(db);await store.observeEpoch(0)
  const article=event(1,{kind:'item',ext_id:'pocketd-item:i',payload:{source:'s',url:'https://example.test/i',summary:'feed'},intent:'feed',fields:['title','url','source','summary']})
  await native.applyNativeRssPage(db,page([article]),0)
  const row=(await rss.listRssPortalRecords())[0]
  await rss.saveRssPortalRecord(row,{title:'User version',payload:{...row.item.payload,summary:'user note'}})
  await native.applyNativeRssPage(db,page([event(2,{...article,sequence:2,op_id:event(2).op_id,client_updated_at:'2090-01-01T00:00:00Z',payload:{...article.payload,summary:'new feed'}})]),1)
  assert.equal((await rss.listRssPortalRecords())[0].item.title,'User version')
  assert.equal(await native.nativeRssBlockedCount(db),1)
  await native.applyNativeRssPage(db,page([event(3,{client_updated_at:new Date().toISOString()})]),2)
  const source=(await rss.listRssPortalRecords()).find(row=>row.item.kind==='source')
  await rss.saveRssPortalRecord(source,{title:source.item.title,payload:{url:'https://independent.test/new',category:'Edited category'}})
  await native.applyNativeRssPage(db,page([event(4,{intent:'update',title:'Native renamed',fields:['title'],client_updated_at:new Date(Date.now()+1000).toISOString()})]),3)
  const saved=(await rss.listRssPortalRecords()).find(row=>row.item.kind==='source')
  assert.equal(saved.item.title,'Native renamed');assert.equal(saved.item.payload.url,'https://independent.test/new');assert.equal(saved.item.payload.category,'Edited category')
 })
})

test('new subscription API captures owner and original epoch before awaits and automatic URL tokens remain local',async()=>{
 await fixture(async f=>{
  const rss=await f.loadModule('/src/features/rss/rss-sync-store.ts'),native=await f.loadModule('/src/features/rss/rss-native-ingest.ts'),api=await f.loadModule('/src/api/rss.ts')
  const db=f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})),store=rss.rssPortalStore(db);await store.observeEpoch(0)
  f.respond(async()=>({id:'created'}))
  const source=await api.rssApi.addSource({url:'https://example.test/rss',title:'New source'})
  assert.equal(source.id,'created')
  const request=f.requests.find(r=>r.url.endsWith('/api/rss/sources'))
  assert.equal(request.options.headers.Authorization,'Bearer token-A')
  assert.deepEqual(JSON.parse(request.options.body),{url:'https://example.test/rss',title:'New source',portalSync:true,portalEpoch:0})
  await native.applyNativeRssPage(db,page([event(1,{payload:{url:'https://example.test/rss?access_token=SECRET',category:''}})]),0)
  assert.equal((await store.pending()).length,0);assert.equal(await native.nativeRssBlockedCount(db),1)
  assert.deepEqual(await rss.listRssPortalRecords(),[])
  let release,started
  const began=new Promise(resolve=>started=resolve),wait=new Promise(resolve=>release=resolve)
  f.respond(async()=>{started();await wait;return {id:'old-account-source'}})
  const pending=api.rssApi.addSource({url:'https://example.test/two'})
  await began;await f.login('B');release()
  await assert.rejects(pending,{name:'AbortError'})
  assert.equal(f.requests.filter(r=>r.url.endsWith('/api/rss/sources')).length,2)
  assert.equal(f.requests.at(-1).options.headers.Authorization,'Bearer token-A')
 })
})

test('native snapshot uses the original cloud lease and atomic checkpoint through policy and reset races',async()=>{
 await fixture(async f=>{
  const rss=await f.loadModule('/src/features/rss/rss-sync-store.ts'),native=await f.loadModule('/src/features/rss/rss-native-ingest.ts')
  const db=f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})),store=rss.rssPortalStore(db);await store.observeEpoch(0)
  let release,started;const began=new Promise(resolve=>started=resolve),wait=new Promise(resolve=>release=resolve)
  f.respond(async()=>{started();await wait;return page([event(1)])})
  const pending=native.ingestNativeRssChanges();await began;await f.setModules({rss:'local'});release()
  await assert.rejects(pending,{name:'AbortError'});assert.equal(await native.nativeRssCursor(db),0)
  await f.setModules({rss:'cloud'})
  f.cryptoConfig.setFieldEncryption('enabled')
  let decryptRelease,encryptStarted;const encrypted=new Promise(resolve=>encryptStarted=resolve),encryptWait=new Promise(resolve=>decryptRelease=resolve)
  let once=true;f.onEncrypt(async()=>{if(once){once=false;encryptStarted();await encryptWait}})
  const applying=native.applyNativeRssPage(db,page([event(1)]),0)
  await encrypted;await store.observeEpoch(1);decryptRelease()
  await assert.rejects(applying,/portal_record_write_conflict/)
  assert.equal(await native.nativeRssCursor(db),0);assert.equal((await store.pending()).length,0)
  await native.applyNativeRssPage(db,page([event(1)]),0)
  assert.equal(await native.nativeRssCursor(db),1);assert.equal(await native.nativeRssBlockedCount(db),1)
  const row=await db.queryOne('SELECT item_json FROM local_rss_native_events')
  assert.match(row.item_json,/^e1:/);assert.equal((await store.decode(row.item_json)).epoch,0)
 })
})

test('a newer native source intention can restore a portal tombstone with its complete portable fields',async()=>{
 await fixture(async f=>{
  const rss=await f.loadModule('/src/features/rss/rss-sync-store.ts'),native=await f.loadModule('/src/features/rss/rss-native-ingest.ts'),{applyRecordPage}=await f.loadModule('/src/native/portal-sync/record-engine.ts')
  const db=f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})),store=rss.rssPortalStore(db);await store.observeEpoch(0)
  await native.applyNativeRssPage(db,page([event(1)]),0)
  await applyRecordPage(store,{changes:[{module:'rss',kind:'source',ext_id:event(1).ext_id,title:'Deleted',payload:{},client_updated_at:'2026-01-02T00:00:00Z',op_id:'portal-delete',deleted:true,epoch:0,change_seq:1}],epoch:0,next_cursor:'1'})
  await native.applyNativeRssPage(db,page([event(2,{title:'Restored native source',intent:'update',fields:['title'],client_updated_at:'2026-01-03T00:00:00Z'})]),1)
  const restored=(await rss.listRssPortalRecords())[0]
  assert.equal(restored.item.title,'Restored native source');assert.equal(restored.item.payload.url,'https://example.test/feed');assert.equal(restored.item.deleted,false)
  assert.equal((await store.pending()).at(-1).title,'Restored native source')
 })
})
