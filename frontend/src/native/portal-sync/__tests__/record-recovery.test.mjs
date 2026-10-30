import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture, gate } from '../../../test-support/notes-sqlite-fixture.mjs'

const specs = [
  ['rss', '/src/features/rss/rss-sync-adapter.ts', 'rssSyncAdapter', {url:'https://example.com/feed',category:''}],
  ['email', '/src/features/email/portal-mail-adapter.ts', 'portalMailAdapter', {from:'a@example.com',subject:'Keep me',snippet:'Original',received_at:''}],
  ['meetings', '/src/features/meetings/meeting-sync-adapter.ts', 'meetingSyncAdapter', {held_at:'',attendees:[],minutes:'Keep original'}],
]
for (const [module, path, name, payload] of specs) {
  test(`${module} clock recovery persists across interruption and stops after another rejection`, () => fixture(async f => {
    const { PortalRecordStore } = await f.loadModule('/src/native/portal-sync/record-store.ts')
    const adapter = (await f.loadModule(path))[name]
    const db = f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})), store = new PortalRecordStore(db,adapter)
    await store.observeEpoch(0)
    await store.commit([], {id:'clock',title:'Keep me',payload,modifiedAt:3600000,epoch:0},true)
    const [old] = await store.pending()
    const ack = {...old,current_epoch:0,outcome:'rejected',reason:'clock_skew',client_updated_at:new Date(1000).toISOString()}
    const interrupted = new PortalRecordStore(db,adapter,async()=>{throw Error('interrupted')})
    await assert.rejects(interrupted.acknowledge(old,ack), /interrupted/)
    const [retry] = await store.pending()
    assert.notEqual(retry.op_id,old.op_id)
    assert.equal(retry.client_updated_at,new Date(1001).toISOString())
    assert.deepEqual(retry.payload,old.payload)
    await store.acknowledge(old,ack)
    assert.deepEqual(await store.pending(),[retry])
    await store.acknowledge(retry,{...retry,current_epoch:0,outcome:'rejected',clock_skew:true,client_updated_at:new Date(500).toISOString()})
    assert.deepEqual(await store.pending(),[])
    assert.equal((await db.queryOne('SELECT last_error FROM local_portal_record_outbox WHERE module=?',[module])).last_error,'clock_skew_failed')
    await store.commit([], {id:'successful',title:'Retry succeeds',payload,modifiedAt:3600000,epoch:0},true)
    const [next] = await store.pending()
    await store.acknowledge(next,{...ack,ext_id:next.ext_id,op_id:next.op_id})
    const [ready] = await store.pending()
    assert.equal(await store.acknowledge(ready,{...ready,current_epoch:0,outcome:'applied'}),true)
    assert.deepEqual(await store.pending(),[])
  }))

  test(`${module} clock acknowledgement only replaces its current projection`, () => fixture(async f => {
    const { PortalRecordStore } = await f.loadModule('/src/native/portal-sync/record-store.ts')
    const adapter = (await f.loadModule(path))[name]
    const db = f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})), store = new PortalRecordStore(db,adapter)
    await store.observeEpoch(0)
    await store.commit([], {id:'clock',title:'Original',payload,modifiedAt:3600000,epoch:0},true)
    const [old] = await store.pending(), wait = gate()
    const correcting = new PortalRecordStore(db,adapter,async item=>{await wait.pause();return JSON.stringify(item)})
    const response = correcting.acknowledge(old,{...old,current_epoch:0,outcome:'rejected',clock_skew:true,client_updated_at:new Date(1000).toISOString()})
    await wait.started
    await store.commit([], {id:'clock',title:'Concurrent intent',payload,modifiedAt:3600001,epoch:0},false)
    wait.release(); await response
    const [latest] = await store.pending()
    assert.equal(latest.title,'Concurrent intent')
    assert.equal((await store.decode((await store.find('clock')).item_json)).op_id,latest.op_id)
    assert.equal((await store.pending()).length,1)
  }))

  for (const transition of ['reset', 'owner']) test(`${module} clock correction is fenced by a concurrent ${transition}`, () => fixture(async f => {
    const { PortalRecordStore } = await f.loadModule('/src/native/portal-sync/record-store.ts')
    const adapter = (await f.loadModule(path))[name]
    const db = f.db.bindOperation(f.db.captureOperation({kind:'local-edit'})), store = new PortalRecordStore(db,adapter)
    await store.observeEpoch(0)
    await store.commit([], {id:'clock',title:'Original',payload,modifiedAt:3600000,epoch:0},true)
    const [old] = await store.pending(), wait = gate()
    const correcting = new PortalRecordStore(db,adapter,async item=>{await wait.pause();return JSON.stringify(item)})
    const response = correcting.acknowledge(old,{...old,current_epoch:0,outcome:'rejected',clock_skew:true,client_updated_at:new Date(1000).toISOString()})
    await wait.started
    if (transition === 'reset') {
      const { PortalResetStore } = await f.loadModule('/src/native/portal-sync/reset-store.ts')
      const { PortalNotesStore } = await f.loadModule('/src/native/portal-sync/store.ts')
      const resets = new PortalResetStore(new PortalNotesStore(db)), intent = await resets.begin([module])
      await resets.complete(intent,{reset_id:intent.reset_id,purged:{[module]:0},epochs:{[module]:1}})
    } else await f.login('B')
    wait.release()
    if (transition === 'owner') await assert.rejects(response,{name:'AbortError'})
    else { await response; assert.deepEqual(await store.pending(),[]) }
    assert.equal(f.raw.exec(`SELECT count(*) FROM local_portal_record_outbox WHERE module='${module}'`)[0].values[0][0],1)
  }))
}
