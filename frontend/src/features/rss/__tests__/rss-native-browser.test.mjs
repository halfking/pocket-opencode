import test from'node:test'
import assert from'node:assert/strict'
import{execFile}from'node:child_process'
import{promisify}from'node:util'
import{createNativeRssBrowserFixture}from'./rss-native-browser.mjs'

test('new native RSS subscription events reach the portable outbox without enrolling a former discovery owner',{timeout:180000},async()=>{
 const f=await createNativeRssBrowserFixture(),exec=promisify(execFile),session=`rss-native-${process.pid}`
 const browser=async(...args)=>(await exec('browser-use',['--session',session,...args],{timeout:30000})).stdout
 const control=async pattern=>{const state=await browser('state'),m=state.match(pattern);assert.ok(m,`Missing ${pattern}\n${state}`);return m[1]}
 const button=label=>control(new RegExp(`\\[(\\d+)\\]<button[^>]*\\/>\\s+${label}\\s*(?:\\n|$)`))
 try{
  await browser('open',f.url);await browser('wait','text','RSS across devices')
  await browser('input',await control(/\[(\d+)\]<input id=rss-add-url\b/),'https://example.test/native')
  await browser('click',await button('发现候选'));await browser('wait','text','Native A feed')
  await browser('click',await control(/\[(\d+)\]<li\b/));await browser('click',await button('Pull native changes'))
  await browser('wait','selector','.portal-records li strong')
  const snapshot=await browser('eval',`JSON.stringify(window.nativeSnapshot)`)
  assert.match(snapshot,/pocketd-source:A-1/);assert.match(snapshot,/\\"epoch\\":0/)
  await browser('click',await button('Bob'));await browser('wait','text','No shared RSS records yet');assert.doesNotMatch(await browser('state'),/Native A feed/)
  await browser('click',await button('Alice'));await browser('click',await button('Pause discovery'))
  await browser('input',await control(/\[(\d+)\]<input id=rss-add-url\b/),'https://example.test/private-A')
  await browser('click',await button('发现候选'));await browser('eval','new Promise(resolve=>{const tick=()=>window.nativeDiscoveryStarted?resolve(true):setTimeout(tick,10);tick()})')
  await browser('click',await button('Bob'));await browser('click',await button('Release discovery'))
  await browser('wait','text','No shared RSS records yet')
  assert.doesNotMatch(await browser('state'),/private-A|Native A feed/)
  const sent=await browser('eval',`JSON.stringify(window.nativeRssFixture.requests.filter(r=>r.path==='/api/rss/sources'))`)
  assert.doesNotMatch(sent,/owner":"B/)
 }finally{await browser('close').catch(()=>{});await f.server.close()}
})
