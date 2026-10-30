import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture, gate } from '../../test-support/notes-sqlite-fixture.mjs'

const credential = (f, token, scope = 'rss') => ({ token, user_id: f.auth.userId, workspace_id: f.auth.workspaceId,
  tenant_id: 'tenant-a', scopes: [scope], issued_at: Math.floor(Date.now()/1000), expires_at: Math.floor(Date.now()/1000)+30 })
async function setup(f) {
  f.auth.authMethod = 'redclaw'
  localStorage.setItem('op_redclaw_portal_base', 'https://portal.example')
  return (await f.loadModule('/src/api/http.ts')).http
}

test('RSS and mail metadata use a separate in-memory resource credential without replacing the login token', () => fixture(async f => {
  const http = await setup(f), calls = []
  globalThis.fetch = async (url, options) => {
    calls.push({ url: String(url), options })
    if (String(url).endsWith('/resource-token')) {
      assert.equal(options.headers.Authorization, 'Bearer token-A')
      const scope = JSON.parse(options.body).scopes[0]
      return Response.json(credential(f, `resource-${scope}`, scope))
    }
    assert.equal(options.headers.Authorization, String(url).includes('/api/rss') ? 'Bearer resource-rss' : 'Bearer resource-email-metadata')
    return Response.json([])
  }
  await http('/api/rss/sources'); await http('/api/rss/items'); await http('/api/email/accounts')
  assert.equal(calls.filter(c => c.url.endsWith('/resource-token')).length, 2)
  assert.equal(f.auth.token, 'token-A')
  assert.equal(localStorage.getItem('pocket_token'), 'token-A')
  assert.equal([...localStorage.keys()].some(key => /resource/.test(key)), false)
}))

test('resource rejection renews only the scoped credential once and never refreshes or clears the login', () => fixture(async f => {
  const http = await setup(f), calls = [], resources = []
  globalThis.fetch = async (url, options) => {
    calls.push(String(url))
    if (String(url).endsWith('/resource-token')) {
      const token = `resource-${resources.length+1}`; resources.push(token)
      return Response.json(credential(f, token))
    }
    assert.equal(options.headers.Authorization, `Bearer ${resources.at(-1)}`)
    return new Response('{}', { status: resources.length === 1 ? 401 : 200, headers: { 'Content-Type': 'application/json' } })
  }
  await http('/api/rss/sources')
  assert.deepEqual(resources, ['resource-1','resource-2'])
  assert.equal(calls.some(url => url.includes('/api/auth/refresh')), false)
  assert.equal(f.auth.userId, 'user-A')
  assert.equal(f.auth.token, 'token-A')
}))

test('unsupported pocket resource capabilities fail closed without sending the RedClaw bearer', () => fixture(async f => {
  const http = await setup(f), calls=[]
  globalThis.fetch = async url => { calls.push(String(url)); return Response.json({}) }
  await assert.rejects(http('/api/emails/mail/body'), /pocket_resource_unavailable/)
  await assert.rejects(http('/api/email/send', { method: 'POST', body: '{}' }), /pocket_resource_unavailable/)
  await assert.rejects(http('/api/meetings/m/summary', { method:'POST' }), /pocket_resource_unavailable/)
  assert.equal(calls.length, 0)
  assert.equal(f.auth.token, 'token-A')
}))

test('unsupported invoice downloads and meeting AI do not dispatch or manufacture a fallback result', () => fixture(async f => {
  await setup(f); await f.setModules({meetings:'cloud'})
  const {emailApi}=await f.loadModule('/src/api/email.ts')
  const {meetingsApi}=await f.loadModule('/src/api/meetings.ts')
  const calls=[]
  globalThis.fetch=async url => {calls.push(String(url)); return Response.json({})}
  for (const download of [() => emailApi.fetchInvoiceFile('id'), () => emailApi.fetchInvoiceThumb('id'), () => emailApi.fetchInvoiceExport('file')])
    await assert.rejects(download(),/pocket_resource_unavailable/)
  const segments=[{text:'Original meeting words',speakerLabel:'Alice',startMs:0,endMs:10}]
  await assert.rejects(meetingsApi.summarize('m',segments),/pocket_resource_unavailable/)
  await assert.rejects(meetingsApi.refine('m',segments),/pocket_resource_unavailable/)
  await assert.rejects(meetingsApi.recommend('m',segments,''),/pocket_resource_unavailable/)
  assert.equal(calls.length,0)
  assert.equal(f.auth.token,'token-A')
}))

test('a local module blocks credential exchange as well as the resource request', () => fixture(async f => {
  const http = await setup(f), calls=[]
  await f.setModules({ rss: 'local' })
  globalThis.fetch = async url => { calls.push(String(url)); return Response.json(credential(f, 'resource')) }
  await assert.rejects(http('/api/rss/sources', { method:'POST', body:'{}' }), /module_policy_local/)
  assert.deepEqual(calls, [])
}))

for (const status of [401, 403, 502, 503]) test(`exchange ${status} preserves the login and never falls back to its bearer`, () => fixture(async f => {
  const http = await setup(f), calls=[]
  globalThis.fetch = async url => { calls.push(String(url)); return new Response('{}', { status }) }
  await assert.rejects(http('/api/rss/sources'), e => e.status === status)
  assert.equal(calls.length, 1)
  assert.ok(calls[0].endsWith('/resource-token'))
  assert.equal(f.auth.userId, 'user-A')
  assert.equal(f.auth.token, 'token-A')
}))

test('a revoked parent session rejects the request and clears only that current login', () => fixture(async f => {
  const http = await setup(f)
  globalThis.fetch = async () => Response.json({error:'parent_session_invalid'}, { status:401 })
  await assert.rejects(http('/api/rss/sources'), e => e.status === 401)
  assert.equal(f.auth.token, '')
  assert.equal(f.auth.userId, '')
}))

test('the actual middleware nested parent-session error revokes the current login', () => fixture(async f => {
  const http=await setup(f)
  globalThis.fetch=async()=>Response.json({error:{code:'parent_session_invalid',message:'Session rejected'}},{status:401})
  await assert.rejects(http('/api/rss/sources'),e=>e.status===401)
  assert.equal(f.auth.token,'')
  assert.equal(f.auth.userId,'')
}))

test('a nested parent error delayed across account activation cannot clear the replacement login', () => fixture(async f => {
  const http=await setup(f), delayed=gate()
  globalThis.fetch=async()=>({ok:false,status:401,async json(){await delayed.pause();return {error:{code:'parent_session_invalid'}}}})
  const pending=http('/api/rss/sources'), rejected=assert.rejects(pending,{name:'AbortError'})
  await delayed.started;await f.login('B');delayed.release();await rejected
  assert.equal(f.auth.token,'token-B');assert.equal(f.auth.userId,'user-B')
}))

test('unscoped pocket APIs reject before any parent-token request or refresh', () => fixture(async f => {
  const http=await setup(f), calls=[]
  globalThis.fetch=async url=>{calls.push(String(url));return Response.json({error:'unauthorized'},{status:401})}
  for(const path of ['/api/user-settings','/api/tasks','/api/sessions','/api/instances','/api/vault','/api/settings','/api/auth/me','/api/auth/biometric/credentials','/api/new-unknown-service'])
    await assert.rejects(http(path),/pocket_resource_unavailable/)
  await assert.rejects(http('/api/auth/reset-password',{method:'POST',body:'{}'}),/pocket_resource_unavailable/)
  assert.deepEqual(calls,[]);assert.equal(f.auth.token,'token-A');assert.equal(f.auth.userId,'user-A')
}))

test('legacy task, LLM, diagnostic and mobile clients cannot bypass scoped admission', () => fixture(async f => {
  await setup(f)
  const {api}=await f.loadModule('/src/api/client.ts')
  const {llmBffApi}=await f.loadModule('/src/api/llm-bff.ts')
  const {fetchWorkspaceBinding,verifyWorkspaceBinding}=await f.loadModule('/src/api/workspaceBinding.ts')
  const {useConnectivityStore}=await f.loadModule('/src/stores/connectivity.ts')
  const calls=[]
  globalThis.fetch=async url=>{calls.push(String(url));return Response.json({})}
  await assert.rejects(api.getTasks(),/pocket_resource_unavailable/)
  await assert.rejects(llmBffApi.getUsage(),/pocket_resource_unavailable/)
  await assert.rejects(fetchWorkspaceBinding('workspace'),/pocket_resource_unavailable/)
  await assert.rejects(verifyWorkspaceBinding('binding'),/pocket_resource_unavailable/)
  const previous=Object.fromEntries(['window','document'].map(key=>[key,Object.getOwnPropertyDescriptor(globalThis,key)]))
  const browser=Object.assign(new EventTarget(),{setInterval,clearInterval,setTimeout,clearTimeout,location:{origin:'http://127.0.0.1',protocol:'http:',hostname:'127.0.0.1'}})
  const document=Object.assign(new EventTarget(),{visibilityState:'hidden',documentElement:{lang:''}})
  Object.defineProperty(globalThis,'window',{configurable:true,value:browser})
  Object.defineProperty(globalThis,'document',{configurable:true,value:document})
  const connectivity=useConnectivityStore()
  try {
    connectivity.init()
    await assert.rejects(connectivity.runtime.deps.fetchImpl('/api/sessions',{headers:{Authorization:'Bearer token-A'}}),/pocket_resource_unavailable/)
  } finally {
    connectivity.runtime?.stop()
    for(const [key,descriptor] of Object.entries(previous)){if(descriptor)Object.defineProperty(globalThis,key,descriptor);else delete globalThis[key]}
  }
  assert.deepEqual(calls,[]);assert.equal(f.auth.token,'token-A')
}))

test('direct pocket logins retain ordinary API authority and authentication rejection behavior', () => fixture(async f => {
  const {http}=await f.loadModule('/src/api/http.ts'), calls=[]
  globalThis.fetch=async(url,options)=>{calls.push(String(url));assert.equal(options.headers.Authorization,'Bearer token-A');return Response.json({settings:[]})}
  await http('/api/user-settings')
  assert.equal(calls.length,1);assert.equal(calls[0].endsWith('/api/user-settings'),true)
  globalThis.fetch=async()=>Response.json({error:'unauthorized'},{status:401})
  await assert.rejects(http('/api/user-settings'),e=>e.status===401)
  assert.equal(f.auth.token,'')
}))

for (const [label, patch] of [
  ['user', {user_id:'other'}], ['workspace', {workspace_id:'other'}], ['tenant', {tenant_id:''}],
  ['broader scopes', {scopes:['rss','email-metadata']}], ['different scope', {scopes:['embedding']}],
  ['expired', {expires_at:0}], ['long-lived', {expires_at:Math.floor(Date.now()/1000)+300}],
  ['coerced expiry', {expires_at:String(Math.floor(Date.now()/1000)+30)}],
]) test(`invalid ${label} in the resource authority response cannot dispatch a request`, () => fixture(async f => {
  const http = await setup(f), calls=[]
  globalThis.fetch = async url => { calls.push(String(url)); return Response.json({...credential(f, 'resource'), ...patch}) }
  await assert.rejects(http('/api/rss/sources'), /pocket_resource_unavailable/)
  assert.equal(calls.length, 1)
  assert.equal(f.auth.token, 'token-A')
}))

test('concurrent resource consumers share the exchange while keeping separate operation guards', () => fixture(async f => {
  const http = await setup(f), delayed=gate(), calls=[]
  let current=true
  globalThis.fetch = async (url, options) => {
    calls.push(String(url))
    if (String(url).endsWith('/resource-token')) { await delayed.pause(); return Response.json(credential(f, 'resource')) }
    return Response.json([])
  }
  const first = http('/api/rss/sources', { assertCurrent() { if (!current) throw Object.assign(new Error('old operation'), {name:'AbortError'}) } })
  const rejected = assert.rejects(first, {name:'AbortError'})
  await delayed.started
  const second = http('/api/rss/items')
  current=false; delayed.release()
  await rejected; await second
  assert.equal(calls.filter(url => url.endsWith('/resource-token')).length, 1)
  assert.equal(calls.some(url => url.endsWith('/api/rss/sources')), false)
  assert.equal(calls.some(url => url.endsWith('/api/rss/items')), true)
}))

test('a delayed exchange cannot send after an A to B to A activation', () => fixture(async f => {
  const http = await setup(f), delayed=gate(), calls=[]
  globalThis.fetch = async url => {
    calls.push(String(url)); const response = credential(f,'resource')
    await delayed.pause(); return Response.json(response)
  }
  const pending=http('/api/rss/sources'), rejected=assert.rejects(pending,{name:'AbortError'})
  await delayed.started; await f.login('B'); await f.login('A'); delayed.release()
  await rejected
  assert.equal(calls.length, 1)
  assert.equal(f.auth.userId, 'user-A')
}))

test('a policy change while the exchange JSON is pending blocks the resource dispatch', () => fixture(async f => {
  const http = await setup(f), delayed=gate(), calls=[]
  await f.setModules({rss:'cloud'})
  globalThis.fetch = async url => {
    calls.push(String(url)); const body=credential(f,'resource')
    return { ok:true, async json() { await delayed.pause(); return body } }
  }
  const pending=http('/api/rss/sources', {method:'POST',body:'{}'}), rejected=assert.rejects(pending,/module_policy_local/)
  await delayed.started; const activation=f.setModules({rss:'local'}); delayed.release(); await rejected; await activation
  assert.equal(calls.length, 1)
  assert.equal(f.auth.token,'token-A')
}))

for (const [key,value] of [['op_redclaw_portal_base','https://new-portal.example'],['pocket_api_base','https://new-pocket.example']])
test(`changing ${key} while the exchange is pending rejects its credential`, () => fixture(async f => {
  const http = await setup(f), delayed=gate(), calls=[]
  globalThis.fetch = async url => { calls.push(String(url)); await delayed.pause(); return Response.json(credential(f,'resource')) }
  const pending=http('/api/rss/sources'), rejected=assert.rejects(pending,{name:'AbortError'})
  await delayed.started; localStorage.setItem(key,value); delayed.release(); await rejected
  assert.equal(calls.length, 1)
  assert.equal(f.auth.token,'token-A')
}))
