import assert from 'node:assert/strict'
import { test, after } from 'node:test'
import { fixture } from './store.test.mjs'
import { notePayload } from '../contract.ts'
import { createServer } from 'vite'

const vite = await createServer({configFile:false,server:{middlewareMode:true,hmr:false},optimizeDeps:{noDiscovery:true}})
after(()=>vite.close())
const { PortalRecordStore } = await vite.ssrLoadModule('/src/native/portal-sync/record-store.ts')

async function setup(module, failures = 2) {
  const f = fixture()
  const store = module === 'notes' ? f.store : new PortalRecordStore(f.db,{module,kind:'record',payload:v=>v,materialize:()=>[]})
  await store.init(); await store.observeEpoch(0)
  const table = module === 'notes' ? 'local_portal_outbox' : 'local_portal_record_outbox'
  const protectedErrors = ['', 'reset_pending', 'reset_epoch_mismatch', 'epoch_unknown', 'policy_local', 'clock_skew:1970-01-01T00:00:01.001Z', 'clock_retry', 'clock_skew_failed']
  const insert = (id,error,epoch=0,owner='A',mod=module) => ({
    statement:`INSERT INTO ${table}(owner_user_id,owner_workspace_id,${module==='notes'?'':'module,kind,'}op_id,ext_id,item_json,reset_epoch,last_error) VALUES (${module==='notes'?'?,?,?,?,?,?,?':'?,?,?,?,?,?,?,?,?'})`,
    values:[owner,'ws',...(module==='notes'?[]:[mod,'record']),id,id,'{}',epoch,error],
  })
  await f.db.runInTransaction(Array.from({length:500},(_,i)=>insert(`old-${i}`,i<failures?'rejected':protectedErrors[(i-failures)%protectedErrors.length])))
  f.raw.run('CREATE TABLE local_capacity_proof(id TEXT PRIMARY KEY)')
  const commit = id => store.commit([{statement:'INSERT INTO local_capacity_proof VALUES(?)',values:[id]}],
    module==='notes'?{source:'note',id,title:id,payload:notePayload({content:'Keep original',format:'text'}),modifiedAt:1000,epoch:0}
      :{id,title:id,payload:{content:'Keep original'},modifiedAt:1000,epoch:0},true)
  return {...f,store,table,insert,commit}
}

for (const module of ['notes','rss','email','meetings']) test(`${module} bounds the owned queue and evicts only its oldest terminal failures`, async () => {
  const f = await setup(module)
  await f.db.runInTransaction([f.insert('other-owner','rejected',0,'B'),
    ...(module==='notes'?[]:[f.insert('other-module','rejected',0,'A',module==='rss'?'email':'rss')])])
  await f.commit('new-1')
  const scoped = module==='notes' ? "owner_user_id='A'" : `owner_user_id='A' AND module='${module}'`
  assert.equal((await f.db.queryOne(`SELECT COUNT(*) AS count FROM ${f.table} WHERE ${scoped}`)).count,500)
  assert.equal(await f.db.queryOne(`SELECT op_id FROM ${f.table} WHERE op_id='old-0'`),null)
  assert.ok(await f.db.queryOne(`SELECT op_id FROM ${f.table} WHERE op_id='old-1'`))
  await f.commit('new-2')
  assert.equal(await f.db.queryOne(`SELECT op_id FROM ${f.table} WHERE op_id='old-1'`),null)
  assert.equal((await f.db.queryOne('SELECT evicted_count FROM local_portal_outbox_retention WHERE owner_user_id=? AND owner_workspace_id=? AND module=?',['A','ws',module])).evicted_count,2)
  await assert.rejects(f.commit('full'),/queue.*full/i)
  assert.equal(await f.db.queryOne("SELECT id FROM local_capacity_proof WHERE id='full'"),null)
  for (const id of ['old-2','old-3','old-4','old-5','old-6','old-7','old-8','old-9','other-owner',...(module==='notes'?[]:['other-module'])]) {
    assert.ok(await f.db.queryOne(`SELECT op_id FROM ${f.table} WHERE op_id=?`,[id]))
  }
  f.raw.close()
})

test('capacity refusal rolls back attempted eviction counters and retains inherited protected overflow', async () => {
  const f = await setup('notes',0)
  await f.db.runInTransaction([f.insert('terminal-overflow','rejected')])
  await assert.rejects(f.commit('full'),/queue.*full/i)
  assert.equal((await f.db.queryOne(`SELECT COUNT(*) AS count FROM ${f.table}`)).count,501)
  assert.ok(await f.db.queryOne(`SELECT op_id FROM ${f.table} WHERE op_id='terminal-overflow'`))
  assert.equal((await f.db.queryOne("SELECT evicted_count FROM local_portal_outbox_retention WHERE module='notes'"))?.evicted_count??0,0)
  assert.equal((await f.db.queryOne('SELECT COUNT(*) AS count FROM local_capacity_proof')).count,0)
  f.raw.close()
})

for (const state of ['reset','old epoch','unknown epoch']) test(`retention protects terminal-looking rows during ${state}`, async () => {
  const f = await setup('notes',500)
  if (state === 'reset') await f.db.run("INSERT INTO local_portal_resets VALUES('A','ws','reset','[\"notes\"]',1,0,NULL)")
  else await f.db.run(`UPDATE local_portal_outbox SET reset_epoch=${state==='old epoch'?'-1':'NULL'}`)
  await assert.rejects(f.commit('full'),/queue.*full/i)
  assert.equal((await f.db.queryOne(`SELECT COUNT(*) AS count FROM ${f.table}`)).count,500)
  f.raw.close()
})

test('successful eviction publishes cumulative owner/module counters after the transaction commits', async () => {
  const f = await setup('notes'), previous = globalThis.window, events = []
  globalThis.window = new EventTarget()
  window.addEventListener('openpocket:portal-outbox-retention',event=>events.push(event.detail))
  try {
    await f.commit('new-1'); await f.commit('new-2')
    assert.deepEqual(events.map(event=>event.evictedCount),[1,2])
    assert.ok(events.every(event=>event.module==='notes'&&event.owner.userId==='A'&&event.owner.workspaceId==='ws'))
  } finally {
    if (previous === undefined) delete globalThis.window
    else globalThis.window = previous
    f.raw.close()
  }
})
