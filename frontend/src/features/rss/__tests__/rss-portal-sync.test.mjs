import test from 'node:test'
import assert from 'node:assert/strict'
import { fixture } from '../../../test-support/notes-sqlite-fixture.mjs'

test('RSS portable records preserve original versions, isolate owners and never enroll fetched history', async () => {
  await fixture(async f => {
    const rss = await f.loadModule('/src/features/rss/rss-sync-store.ts')
    const store = rss.rssPortalStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})))
    await store.observeEpoch(0)
    const source = await rss.createRssPortalRecord({kind:'source',title:'Private feed',payload:{url:'https://example.test/feed',category:'News'}})
    const article = await rss.createRssPortalRecord({kind:'item',title:'Article',payload:{source:'Private feed',url:'https://example.test/a',summary:'Full summary'}})
    const pending = await store.pending()
    assert.equal(pending.length,2)
    assert.equal(pending[0].client_updated_at,source.item.client_updated_at)
    assert.notEqual(source.item.ext_id,article.item.ext_id)
    assert.deepEqual(Object.keys(source.item.payload).sort(),['category','url'])
    assert.equal((await rss.listRssPortalRecords()).length,2)
    const old = rss.captureRssPortalOperation()
    await f.login('B')
    assert.deepEqual(await rss.listRssPortalRecords(),[])
    await assert.rejects(rss.saveRssPortalRecord(source,{title:'must not write',payload:source.item.payload},old), {name:'AbortError'})
    await f.login('A')
    await rss.deleteRssPortalRecord(source)
    const after=await rss.rssPortalStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'}))).pending()
    assert.equal(after.at(-1).deleted,true)
    assert.equal((await rss.listRssPortalRecords()).length,1)
    assert.equal(f.requests.some(r=>r.url.includes('/api/rss/')),false)
  })
})

test('RSS source/item remote projection and reset do not fabricate fetched subscriptions or upgrade old work', async () => {
  await fixture(async f => {
    const rss=await f.loadModule('/src/features/rss/rss-sync-store.ts')
    const {applyRecordPage}=await f.loadModule('/src/native/portal-sync/record-engine.ts')
    const store=rss.rssPortalStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})))
    await store.observeEpoch(0)
    const local=await rss.createRssPortalRecord({kind:'item',title:'Offline original',payload:{source:'A',url:'',summary:'original'}})
    const item=(kind,title)=>({module:'rss',kind,ext_id:'same',title,payload:kind==='source'?{url:'https://example.test/rss',category:'C'}:{source:'A',url:'',summary:'remote'},client_updated_at:'2026-01-01T00:00:00.123456Z',op_id:'remote-'+kind,deleted:false,epoch:0,change_seq:kind==='source'?1:2})
    await applyRecordPage(store,{changes:[item('source','Source'),item('item','Remote article')],next_cursor:'2',epoch:0})
    const rows=await rss.listRssPortalRecords()
    assert.equal(rows.length,3)
    assert.equal(rows.filter(row=>row.item.ext_id==='same').length,2)
    await store.observeEpoch(1)
    assert.equal((await store.pending()).length,0)
    const retained=await rss.listRssPortalRecords()
    assert.equal(retained.some(row=>row.item.ext_id===local.item.ext_id&&row.item.payload.summary==='original'),true)
    assert.equal(f.requests.length,0)
  })
})

test('RSS rejects credentials, nested unknown fields and keeps a captured unknown epoch blocked', async () => {
  await fixture(async f=>{
    const rss=await f.loadModule('/src/features/rss/rss-sync-store.ts')
    await assert.rejects(rss.createRssPortalRecord({kind:'source',title:'Credential URL',payload:{url:'https://user:secret@example.test/rss',category:''}}),/credentials/)
    await assert.rejects(rss.createRssPortalRecord({kind:'item',title:'Nested',payload:{summary:'s',source:'x',url:'',credentials:{password:'secret'}}}),/Unknown/)
    const operation=rss.captureRssPortalOperation()
    assert.equal(operation.epoch,null)
    const store=rss.rssPortalStore(operation.db)
    await store.observeEpoch(0)
    await rss.createRssPortalRecord({kind:'item',title:'Captured before discovery',payload:{summary:'keep local',source:'x',url:''}},operation)
    assert.equal((await store.pending()).length,0)
    assert.equal((await rss.listRssPortalRecords()).length,1)
  })
})

test('RSS sync cycle sends source and item versions, retries immutable operations and applies remote tombstones', async () => {
  await fixture(async f=>{
    const rss=await f.loadModule('/src/features/rss/rss-sync-store.ts')
    const {syncRecordsOnce}=await f.loadModule('/src/native/portal-sync/record-engine.ts')
    const store=rss.rssPortalStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})))
    await store.observeEpoch(0)
    const local=await rss.createRssPortalRecord({kind:'item',title:'Original',payload:{source:'Source',summary:' Complete summary \n',url:'https://example.test/a'}})
    const writes=[],cloud=[],transport={
      changes:async cursor=>({changes:cloud.filter(row=>row.change_seq>Number(cursor||0)),next_cursor:String(cloud.length),epochs:{rss:0}}),
      push:async items=>{writes.push(structuredClone(items));throw new Error('lost response')},
    }
    await assert.rejects(syncRecordsOnce(store,transport,()=>store.db.assertCurrent()),/lost response/)
    assert.equal((await store.pending()).length,1)
    transport.push=async items=>{writes.push(structuredClone(items));for(const item of items)cloud.push({...item,last_op_id:item.op_id,change_seq:cloud.length+1});return{results:items.map(item=>({module:'rss',ext_id:item.ext_id,op_id:item.op_id,outcome:'applied',current_epoch:0}))}}
    assert.equal(await syncRecordsOnce(store,transport,()=>store.db.assertCurrent()),1)
    assert.deepEqual(writes[0],writes[1])
    assert.equal((await store.pending()).length,0)
    const deleted={...cloud[0],deleted:true,payload:{},client_updated_at:new Date(Date.now()+1000).toISOString(),op_id:'remote-deletion',last_op_id:'remote-deletion',change_seq:2}
    cloud.push(deleted)
    await syncRecordsOnce(store,transport,()=>store.db.assertCurrent())
    assert.deepEqual(await rss.listRssPortalRecords(),[])
    assert.equal(f.requests.some(r=>r.url.includes('/api/rss/')),false)
    assert.equal(local.item.payload.summary,' Complete summary \n')
  })
})

test('explicit fetched-item copies keep a stable identity without uploading fetched body or unrelated fields', async()=>{
  await fixture(async f=>{
    const rss=await f.loadModule('/src/features/rss/rss-sync-store.ts'),store=rss.rssPortalStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})))
    await store.observeEpoch(0)
    const input={id:'fetched-1',title:'Chosen article',sourceId:'source-1',summary:'User-chosen summary',url:'https://example.test/a',content:'PRIVATE FULL BODY',author:'raw author'}
    await rss.copyRssItemToPortal(input)
    const first=(await store.pending())[0]
    assert.deepEqual(first.payload,{source:'source-1',summary:'User-chosen summary',url:'https://example.test/a'})
    await rss.copyRssItemToPortal({...input,summary:'Edited summary'})
    const queued=await store.pending()
    assert.equal(queued.length,2)
    assert.equal(queued[0].ext_id,queued[1].ext_id)
    assert.equal((await rss.listRssPortalRecords()).length,1)
    assert.equal(JSON.stringify(queued).includes('PRIVATE FULL BODY'),false)
    assert.equal(f.requests.length,0)
    const before=rss.captureRssPortalOperation()
    await f.login('B')
    await assert.rejects(rss.copyRssItemToPortal(input,before),{name:'AbortError'})
    assert.deepEqual(await rss.listRssPortalRecords(),[])
  })
})
