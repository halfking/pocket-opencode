import test from 'node:test'
import assert from 'node:assert/strict'
import {fixture,gate} from '../../../test-support/notes-sqlite-fixture.mjs'

const article=(id='article')=>({id,sourceId:'shared-source',title:'Author title',content:'PRIVATE FULL BODY',url:'https://example.test/article',categories:[],fetchedAt:'2026-01-01T00:00:00Z',relevance:0,matchReasons:[],status:'unread',portalManaged:true})
const remote=(status='starred',patch={})=>({module:'rss',kind:'item-state',ext_id:'pocketd-item:article',title:'',payload:{item_ext_id:'pocketd-item:article',status},client_updated_at:'2026-01-01T00:00:00.123456Z',op_id:'portal-state-op',deleted:false,epoch:0,change_seq:1,...patch})
async function setup(f){
 const rss=await f.loadModule('/src/features/rss/rss-sync-store.ts'),state=await f.loadModule('/src/features/rss/rss-item-state.ts'),engine=await f.loadModule('/src/native/portal-sync/record-engine.ts')
 const db=f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})),store=rss.rssPortalStore(db);await store.observeEpoch(0)
 return {rss,state,engine,db,store}
}
function acknowledge(f,outcome='applied'){
 f.respond(async(_url,options)=>({user_id:f.auth.userId,workspace_id:f.auth.workspaceId,acks:JSON.parse(options.body).items.map(item=>({ext_id:item.ext_id,op_id:item.op_id,epoch:item.epoch,outcome}))}))
}

test('managed read/star actions save locally while offline with private body excluded and stable retry versions',async()=>{
 await fixture(async f=>{
  const {state,store,db}=await setup(f),api=await f.loadModule('/src/api/rss.ts');f.cryptoConfig.setFieldEncryption('enabled')
  const before=Date.now(),saved=await api.rssApi.setStarred(article(),true)
  assert.equal(saved.status,'starred');assert.equal(saved.content,'PRIVATE FULL BODY');assert.equal(f.requests.length,0)
  const [pending]=await store.pending();assert.equal(pending.kind,'item-state');assert.equal(pending.ext_id,'pocketd-item:article');assert.equal(pending.title,'');assert.deepEqual(pending.payload,{item_ext_id:pending.ext_id,status:'starred'});assert.ok(Date.parse(pending.client_updated_at)>=before)
  const persisted=await db.queryOne("SELECT item_json FROM local_portal_records WHERE kind='item-state'");assert.match(persisted.item_json,/^e1:/);assert.doesNotMatch(JSON.stringify(pending),/PRIVATE FULL BODY|Author title/)
  f.respond(async()=>{throw new Error('offline')});await assert.rejects(state.reconcileRssItemStates(),/offline/)
  const first=f.requests.at(-1).options.body;acknowledge(f);assert.equal(await state.reconcileRssItemStates(),1);assert.equal(f.requests.at(-1).options.body,first)
  assert.equal(await state.reconcileRssItemStates(),0)
  const unstarred=await api.rssApi.setStarred(saved,false);assert.equal(unstarred.status,'read');assert.equal(unstarred.content,'PRIVATE FULL BODY')
  const versions=await store.pending();assert.equal(versions.length,2);assert.notEqual(versions[0].op_id,versions[1].op_id)
  assert.equal((await state.overlayRssItemStates([article()]))[0].status,'read')
 })
})

test('remote states share article identity without overwriting content and malformed targets never advance cursor',async()=>{
 await fixture(async f=>{
  const {state,store,engine}=await setup(f)
  await engine.applyRecordPage(store,{changes:[remote()],epoch:0,next_cursor:'state-1'})
  assert.equal((await state.overlayRssItemStates([article()]))[0].status,'starred')
  const result=(await state.overlayRssItemStates([article()]))[0];assert.equal(result.title,'Author title');assert.equal(result.content,'PRIVATE FULL BODY')
  await assert.rejects(engine.applyRecordPage(store,{changes:[remote('read',{payload:{item_ext_id:'other',status:'read'},op_id:'bad'})],epoch:0,next_cursor:'bad'}),/target/)
  assert.equal(await store.cursor(),'state-1')
  for(const status of [['read'],{status:'read'},null,1]){
    await assert.rejects(engine.applyRecordPage(store,{changes:[remote(status,{op_id:'invalid-type'})],epoch:0,next_cursor:'bad-type'}),/Invalid RSS item state/)
    assert.equal(await store.cursor(),'state-1')
  }
  acknowledge(f);await state.reconcileRssItemStates();assert.equal(f.requests.length,1);assert.ok(f.requests[0].url.endsWith('/api/rss/portable/item-states'))
  const sent=JSON.parse(f.requests[0].options.body).items[0];assert.equal(sent.client_updated_at,remote().client_updated_at);assert.equal(sent.op_id,remote().op_id)
  await engine.applyRecordPage(store,{changes:[remote('',{deleted:true,payload:{},op_id:'state-deleted',client_updated_at:'2026-01-02T00:00:00Z'})],epoch:0,next_cursor:'state-2'})
  assert.equal((await state.overlayRssItemStates([article()]))[0].status,'unread')
 })
})

test('owner A-B-A and reset races cannot stamp new credentials or acknowledge original status work',async()=>{
 await fixture(async f=>{
  const {state,store,db}=await setup(f),pause=gate();await state.saveRssItemStatuses([article()],'starred')
  f.respond(async()=>{await pause.pause();return {user_id:'user-A',workspace_id:'workspace',acks:[]}})
  const sending=state.reconcileRssItemStates(),rejected=assert.rejects(sending,{name:'AbortError'});await pause.started;await f.login('B');await f.login('A');pause.release();await rejected
  const fresh=f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})),rss=await f.loadModule('/src/features/rss/rss-sync-store.ts'),current=rss.rssPortalStore(fresh)
  assert.equal((await current.pending()).length,1);assert.equal(f.requests.at(-1).options.headers.Authorization,'Bearer token-A')
  await current.observeEpoch(1);const count=f.requests.length;assert.equal(await state.reconcileRssItemStates(),0);assert.equal(f.requests.length,count)
  assert.equal((await current.byExternalId('pocketd-item:article','item-state')).reset_epoch,0)
  assert.throws(()=>db.assertCurrent(),{name:'AbortError'})
 })
})

test('concurrent local intentions roll back losing batches and historical items cannot be enrolled',async()=>{
 await fixture(async f=>{
  const {state,store}=await setup(f);f.cryptoConfig.setFieldEncryption('enabled')
  await assert.rejects(state.saveRssItemStatuses([{...article(),portalManaged:false}],'read'),/explicitly shared/)
  const pause=gate();let first=true;f.onEncrypt(async()=>{if(first){first=false;await pause.pause()}})
  const slow=state.saveRssItemStatuses([article('one'),article('two')],'read'),rejected=assert.rejects(slow,/Module changed/)
  await pause.started;await state.saveRssItemStatuses([article('one')],'starred');pause.release();await rejected
  const pending=await store.pending();assert.equal(pending.length,1);assert.equal(pending[0].payload.status,'starred');assert.equal(await store.byExternalId('pocketd-item:two','item-state'),null)
 })
})

test('unknown targets remain projections without starving later state batches and replies require exact identity',async()=>{
 await fixture(async f=>{
  const {state,store}=await setup(f)
  await state.saveRssItemStatuses(Array.from({length:100},(_,i)=>article(String(i))),'read')
  await state.saveRssItemStatuses([article('last')],'starred')
  acknowledge(f,'unavailable');assert.equal(await state.reconcileRssItemStates(),0);assert.equal(f.requests.length,2)
  assert.equal(JSON.parse(f.requests.at(-1).options.body).items[0].ext_id,'pocketd-item:last')
  f.respond(async()=>({user_id:'user-A',workspace_id:'workspace',acks:[{ext_id:'wrong',op_id:'wrong',epoch:0,outcome:'applied'}]}))
  await assert.rejects(state.reconcileRssItemStates(),/acknowledgement/)
  assert.equal((await store.pending()).length,50)
 })
})

test('native status echo retains original op and incompatible submicrosecond time stays blocked',async()=>{
 await fixture(async f=>{
  const {store,state}=await setup(f),native=await f.loadModule('/src/features/rss/rss-native-ingest.ts'),db=f.db.bindOperation(f.db.captureOperation({kind:'local-edit'}))
  const event=(sequence,patch={})=>({sequence,kind:'item-state',ext_id:'pocketd-item:article',title:'',payload:{item_ext_id:'pocketd-item:article',status:'archived'},client_updated_at:'2026-01-01T00:00:00.123456Z',op_id:'original-state-'+sequence,epoch:0,deleted:false,intent:'status',fields:['status'],sync_error:'',...patch})
  await native.applyNativeRssPage(db,{user_id:'user-A',workspace_id:'workspace',changes:[event(1)],next_cursor:1},0)
  assert.equal((await state.overlayRssItemStates([article()]))[0].status,'archived')
  const pending=await store.pending();assert.equal(pending[0].op_id,'original-state-1')
  await native.applyNativeRssPage(db,{user_id:'user-A',workspace_id:'workspace',changes:[event(2,{client_updated_at:'2026-01-02T00:00:00.123456789Z'})],next_cursor:2},1)
  assert.equal((await store.pending()).length,1);assert.equal(await native.nativeRssBlockedCount(db),1)
  const blocked=await db.queryOne('SELECT item_json,reason FROM local_rss_native_events WHERE sequence=2');assert.equal(blocked.reason,'source_precision_unsupported');assert.equal((await store.decode(blocked.item_json)).client_updated_at,'2026-01-02T00:00:00.123456789Z')
 })
})

test('local status filtering merges verified native targets before pagination without reviving deleted articles',async()=>{
 await fixture(async f=>{
  const {state}=await setup(f),api=await f.loadModule('/src/api/rss.ts')
  const feed=Array.from({length:6},(_,i)=>({...article(String(i)),publishedAt:`2026-01-01T00:00:00.00000${6-i}Z`}))
  await state.saveRssItemStatuses([feed[0]],'starred');await state.saveRssItemStatuses([feed[3]],'starred');await state.saveRssItemStatuses([article('deleted')],'starred')
  f.respond(async url=>{
   const q=new URL(url,'https://fixture').searchParams,ids=q.getAll('id')
   const matching=feed.filter(item=>(!q.get('status')||item.status===q.get('status'))&&(!ids.length||ids.includes(item.id)))
   const offset=Number(q.get('offset')??0),limit=Number(q.get('limit')??50)
   return {items:matching.slice(offset,offset+limit)}
  })
  assert.deepEqual((await api.rssApi.listItems({status:'starred',limit:1})).map(item=>item.id),['0'])
  assert.deepEqual((await api.rssApi.listItems({status:'starred',limit:1,offset:1})).map(item=>item.id),['3'])
  assert.deepEqual((await api.rssApi.listItems({status:'unread',limit:2})).map(item=>item.id),['1','2'])
  assert.deepEqual((await api.rssApi.listItems({status:'unread',limit:2,offset:2})).map(item=>item.id),['4','5'])
  await f.login('B');assert.equal((await api.rssApi.listItems({status:'starred'})).length,0)
 })
})

test('clear-cloud and source deletion do not restore old status operations into a new epoch',async()=>{
 await fixture(async f=>{
  const {state,store,rss,db}=await setup(f),native=await f.loadModule('/src/features/rss/rss-native-ingest.ts')
  const captured=rss.captureRssPortalOperation();await store.observeEpoch(1)
  await state.saveRssItemStatuses([article()],'starred',captured)
  assert.equal((await store.byExternalId('pocketd-item:article','item-state')).reset_epoch,0)
  assert.equal((await store.pending()).length,0);assert.equal(await state.reconcileRssItemStates(),0)
  const change={sequence:1,kind:'item-state',ext_id:'pocketd-item:article',title:'',payload:{},client_updated_at:'2026-01-02T00:00:00Z',op_id:'pocketd-rss:00000000-0000-4000-8000-000000000001',epoch:1,deleted:true,intent:'delete',fields:[],sync_error:''}
  await native.applyNativeRssPage(db,{user_id:'user-A',workspace_id:'workspace',changes:[change],next_cursor:1},0)
  const tombstone=await store.decode((await store.byExternalId(change.ext_id,'item-state')).item_json);assert.equal(tombstone.deleted,true);assert.equal(tombstone.epoch,1)
  const originals=await db.query("SELECT item_json FROM local_portal_record_outbox WHERE reset_epoch=0");assert.equal(originals.length,1);assert.equal((await store.decode(originals[0].item_json)).payload.status,'starred')
  acknowledge(f,'unavailable');assert.equal(await state.reconcileRssItemStates(),0)
  const sent=JSON.parse(f.requests.at(-1).options.body).items;assert.equal(sent.length,1);assert.equal(sent[0].deleted,true);assert.equal(sent[0].epoch,1)
 })
})
