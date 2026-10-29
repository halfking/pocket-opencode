import assert from 'node:assert/strict'
import {test} from 'node:test'
import {fixture,gate} from '../../test-support/notes-sqlite-fixture.mjs'

async function prepare(f,modules=['notes']) {
  await f.setModules(Object.fromEntries(modules.map(module=>[module,'local'])))
  const api=await f.loadModule('/src/api/syncPolicy.ts')
  const binding=()=>({binding:'trusted-fixture-binding',user_id:f.auth.userId,workspace_id:f.auth.workspaceId,tenant_id:'tenant',expires_at:Math.floor(Date.now()/1000)+300})
  return {api,binding}
}
const result=(intent,epoch=1)=>({reset_id:intent.reset_id,purged:Object.fromEntries(intent.modules.map(module=>[module,module==='email'?0:3])),epochs:Object.fromEntries(intent.modules.map(module=>[module,epoch]))})

test('all portal modules use trusted workspace purge and preserve zero counts',()=>fixture(async f=>{
  const modules=['rss','email','notes','meetings'],{api,binding}=await prepare(f,modules)
  f.respond(async(url,options)=>url.endsWith('/binding')?binding():result(JSON.parse(options.body)))
  const response=await api.purgeCloudCopy(modules)
  assert.deepEqual(response.purged,{email:0,meetings:3,notes:3,rss:3})
  const purge=f.requests.find(request=>request.url.includes('/sync/v2/purge'))
  assert.ok(purge);assert.match(purge.url,/workspace_id=workspace/)
  assert.equal(purge.options.headers['X-Workspace-Binding'],'trusted-fixture-binding')
  assert.equal(purge.options.headers.Authorization,'Bearer token-A')
  assert.ok(JSON.parse(purge.options.body).reset_id)
  assert.equal(f.requests.some(request=>request.url.includes('/portal/cloud/purge')),false)
}))

test('lost purge response reuses the same persistent reset id without advancing twice',()=>fixture(async f=>{
  const {api,binding}=await prepare(f),ids=[],applied=new Map()
  f.respond(async(url,options)=>{
    if(url.endsWith('/binding'))return binding()
    const intent=JSON.parse(options.body);ids.push(intent.reset_id)
    if(!applied.has(intent.reset_id))applied.set(intent.reset_id,result(intent,applied.size+1))
    if(ids.length===1)throw new TypeError('response lost after server reset')
    return applied.get(intent.reset_id)
  })
  await assert.rejects(api.purgeCloudCopy(['notes']),/response lost/)
  assert.equal((await f.db.queryOne('SELECT completed FROM local_portal_resets')).completed,0)
  await api.purgeCloudCopy(['notes'])
  assert.equal(ids[0],ids[1]);assert.equal(applied.size,1)
  await api.purgeCloudCopy(['notes'])
  assert.notEqual(ids[2],ids[1]);assert.equal(applied.size,2)
  assert.equal((await f.db.queryOne("SELECT epoch FROM local_portal_sync_epochs WHERE module='notes' AND owner_user_id='user-A'")).epoch,2)
}))

for(const malformed of [null,{}, {purged:[]}, {purged:{notes:-1}}, {purged:{notes:1.5}}, {purged:{notes:0},epochs:{notes:'1'}}, {purged:{notes:0},epochs:{notes:1},reset_id:'wrong'}]) {
 test(`trusted purge rejects malformed acknowledgement ${JSON.stringify(malformed)}`,()=>fixture(async f=>{
   const {api,binding}=await prepare(f)
   f.respond(async url=>url.endsWith('/binding')?binding():malformed)
   await assert.rejects(api.purgeCloudCopy(['notes']),/Invalid trusted cloud reset/)
   assert.equal((await f.db.queryOne('SELECT completed FROM local_portal_resets')).completed,0)
   assert.equal((await f.portalNotesStore(f.db.bindOperation(f.db.captureOperation({kind:'local-edit'}))).currentEpoch()),0)
 }))
}

test('purge response cannot complete under another login or bind that account',()=>fixture(async f=>{
  const {api,binding}=await prepare(f),paused=gate()
  f.respond(async(url,options)=>{
    if(url.endsWith('/binding'))return binding()
    const intent=JSON.parse(options.body);await paused.pause();return result(intent)
  })
  const purging=api.purgeCloudCopy(['notes']);purging.catch(()=>{})
  await paused.started;await f.login('B');paused.release()
  await assert.rejects(purging,error=>error.name==='AbortError')
  assert.equal((await f.db.queryOne("SELECT completed FROM local_portal_resets WHERE owner_user_id='user-A'")).completed,0)
  assert.equal((await f.db.queryOne("SELECT COUNT(*) AS n FROM local_portal_resets WHERE owner_user_id='user-B'")).n,0)
}))

test('a binding for another workspace prevents purge before intent or network mutation',()=>fixture(async f=>{
  const {api,binding}=await prepare(f)
  f.respond(async url=>({...binding(),workspace_id:'untrusted-workspace'}))
  await assert.rejects(api.purgeCloudCopy(['notes']),/authority did not confirm/)
  assert.equal(f.requests.some(request=>request.url.includes('/sync/v2/purge')),false)
}))

test('purge rejects unsupported modules without any network request',()=>fixture(async f=>{
  const {api}=await prepare(f)
  await assert.rejects(api.purgeCloudCopy(['tasks']),/portal module/i)
  assert.equal(f.requests.length,0)
}))

test('policy cloud/local ABA leaves an uncertain purge pending and blocks success',()=>fixture(async f=>{
  const {api,binding}=await prepare(f),paused=gate()
  f.respond(async(url,options)=>{
    if(url.endsWith('/binding'))return binding()
    const intent=JSON.parse(options.body);await paused.pause();return result(intent)
  })
  const purging=api.purgeCloudCopy(['notes']);purging.catch(()=>{})
  await paused.started;await f.setMode('cloud');await f.setMode('local');paused.release()
  await assert.rejects(purging,error=>error.name==='AbortError')
  assert.equal((await f.db.queryOne('SELECT completed FROM local_portal_resets')).completed,0)
}))

test('purge entry captures original identity before awaiting module loading',()=>fixture(async f=>{
  const {api,binding}=await prepare(f)
  f.respond(async(url,options)=>url.endsWith('/binding')?binding():result(JSON.parse(options.body)))
  const pending=api.purgeCloudCopy(['notes']);pending.catch(()=>{})
  await f.login('B');await f.setMode('local')
  await assert.rejects(pending,error=>error.name==='AbortError')
  assert.equal(f.requests.some(request=>request.url.includes('/sync/v2/purge')),false)
  assert.equal((await f.db.queryOne("SELECT COUNT(*) AS n FROM local_portal_resets WHERE owner_user_id='user-B'")).n,0)
}))
