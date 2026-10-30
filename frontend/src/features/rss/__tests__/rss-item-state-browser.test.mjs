import test from 'node:test'
import assert from 'node:assert/strict'
import {execFile} from 'node:child_process'
import {promisify} from 'node:util'
import {createRssStateBrowserFixture} from './rss-item-state-browser.mjs'

test('real browser keeps offline stars and private body across owner switches, retries originals and reflects remote stars',{timeout:180000},async()=>{
 const f=await createRssStateBrowserFixture(),exec=promisify(execFile),session=`rss-state-${process.pid}`
 const browser=async(...args)=>(await exec('browser-use',['--session',session,...args],{timeout:60000})).stdout
 const button=async(label)=>{const state=await browser('state'),match=state.match(new RegExp(`\\[(\\d+)\\]<button[^>]*\\/>\\s+(?:[^\\n]*\\n\\s+)?${label}\\s*(?:\\n|$)`));assert.ok(match,`Missing ${label}\n${state}`);return match[1]}
 try{
  await browser('open',f.url);await browser('wait','text','PRIVATE BODY A')
  await browser('click',await button('收藏'));await browser('wait','text','已收藏')
  await browser('click',await button('Sync native'));await browser('wait','text','offline')
  const first=await browser('eval',`JSON.stringify(window.rssStateFixture.requests.filter(r=>r.path.endsWith('/item-states')).at(-1))`)
  assert.doesNotMatch(first,/PRIVATE BODY|Article A/)
  await browser('click',await button('Bob'));await browser('wait','text','PRIVATE BODY B');assert.doesNotMatch(await browser('state'),/PRIVATE BODY A|已收藏/)
  await browser('click',await button('Alice'));await browser('wait','text','PRIVATE BODY A');await browser('wait','text','已收藏')
  await browser('click',await button('Online'));await browser('click',await button('Sync native'));await browser('wait','text','Saved to pocketd')
  const retry=await browser('eval',`JSON.stringify(window.rssStateFixture.requests.filter(r=>r.path.endsWith('/item-states')).at(-1))`);assert.equal(retry,first)
  await browser('click',await button('已收藏'));await browser('wait','text','收藏')
  await browser('click',await button('Remote star'));await browser('wait','text','已收藏');assert.match(await browser('state'),/PRIVATE BODY A/)
 }finally{await browser('close').catch(()=>{});await f.server.close()}
})

test('production list fills an empty starred filter from remote state and removes it on a later read',{timeout:180000},async()=>{
 const f=await createRssStateBrowserFixture(),exec=promisify(execFile),session=`rss-list-state-${process.pid}`
 const browser=async(...args)=>(await exec('browser-use',['--session',session,...args],{timeout:60000})).stdout
 const control=async pattern=>{const state=await browser('state'),match=state.match(pattern);assert.ok(match,`Missing ${pattern}\n${state}`);return match[1]}
 const button=label=>control(new RegExp(`\\[(\\d+)\\]<button[^>]*\\/>\\s+${label}\\s*(?:\\n|$)`))
 try{
  await browser('open',f.url);await browser('wait','text','PRIVATE BODY A');await browser('click',await button('List view'))
  await browser('select',await control(/\[(\d+)\]<select id=rss-status-filter\b/),'starred')
  await browser('wait','text','暂无信息。');assert.doesNotMatch(await browser('state'),/Article A/)
  await browser('click',await button('Remote star'));await browser('wait','text','Article A')
  await browser('click',await button('Remote read'));await browser('wait','text','暂无信息。');assert.doesNotMatch(await browser('state'),/Article A/)
 }finally{await browser('close').catch(()=>{});await f.server.close()}
})

test('production messages hub marks loaded RSS items read offline for bulk and row actions under their original owner',{timeout:180000},async()=>{
 const f=await createRssStateBrowserFixture(),exec=promisify(execFile),session=`rss-messages-state-${process.pid}`
 const browser=async(...args)=>(await exec('browser-use',['--session',session,...args],{timeout:60000})).stdout
 const control=async pattern=>{const state=await browser('state'),match=state.match(pattern);assert.ok(match,`Missing ${pattern}\n${state}`);return match[1]}
 try{
  await browser('open',f.url);await browser('wait','text','PRIVATE BODY A')
  await browser('click',await control(/\[(\d+)\]<button id=messages\b/));await browser('wait','text','Article A');await browser('wait','text','PRIVATE A NOTIFICATION')
  const before=await browser('eval',`window.rssStateFixture.requests.filter(r=>r.path==='/api/rss/items/article').length`)
  await browser('click',await control(/\[(\d+)\]<button aria-label=Mark all read\b/))
  await browser('eval',`new Promise(resolve=>{const tick=async()=>{await window.rssStateFixture.snapshot();if(window.stateSnapshot.length)resolve(true);else setTimeout(tick,10)};tick()})`)
  assert.match(await browser('eval','JSON.stringify(window.stateSnapshot)'),/\\"status\\":\\"read\\"/)
  assert.equal(await browser('eval',`window.rssStateFixture.requests.filter(r=>r.path==='/api/rss/items/article').length`),before)
  await browser('click',await control(/\[(\d+)\]<button id=bob\b/));await browser('wait','text','Article B');assert.doesNotMatch(await browser('state'),/Article A|PRIVATE A NOTIFICATION/)
  await browser('click',await control(/\[(\d+)\]<button \/>\n(?:(?!<button)[\s\S])*?Article B/))
  await browser('eval',`new Promise(resolve=>{const tick=async()=>{await window.rssStateFixture.snapshot();if(window.stateSnapshot.length)resolve(true);else setTimeout(tick,10)};tick()})`)
  assert.match(await browser('eval','JSON.stringify(window.stateSnapshot)'),/\\"status\\":\\"read\\"/)
 }finally{await browser('close').catch(()=>{});await f.server.close()}
})

test('production messages hub queues remote status during a delayed initial load and clears former owner events',{timeout:180000},async()=>{
 const f=await createRssStateBrowserFixture(),exec=promisify(execFile),session=`rss-messages-race-${process.pid}`
 const browser=async(...args)=>(await exec('browser-use',['--session',session,...args],{timeout:60000})).stdout
 const control=async pattern=>{const state=await browser('state'),match=state.match(pattern);assert.ok(match,`Missing ${pattern}\n${state}`);return match[1]}
 try{
  await browser('open',f.url);await browser('wait','text','PRIVATE BODY A')
  await browser('eval','window.rssStateFixture.pauseHubEmail()');await browser('click',await control(/\[(\d+)\]<button id=messages\b/))
  await browser('eval',`new Promise(resolve=>{const tick=()=>window.hubEmailWaiting&&window.hubRssListReady?resolve(true):setTimeout(tick,10);tick()})`)
  await browser('click',await control(/\[(\d+)\]<button id=remote-read\b/));await browser('eval','window.rssStateFixture.releaseHubEmail()');await browser('wait','text','Article A')
  assert.match(await browser('eval',`document.querySelectorAll('.row.unread').length`),/result: 0/)
  await browser('eval','window.rssStateFixture.pauseHubEmail()');await browser('click',await control(/\[(\d+)\]<button id=bob\b/))
  await browser('eval',`new Promise(resolve=>{const tick=()=>window.hubEmailWaiting&&window.hubRssListReady?resolve(true):setTimeout(tick,10);tick()})`)
  await browser('click',await control(/\[(\d+)\]<button id=remote-read\b/));await browser('click',await control(/\[(\d+)\]<button id=alice\b/));await browser('eval','window.rssStateFixture.releaseHubEmail()')
  await browser('wait','text','PRIVATE A NOTIFICATION');assert.doesNotMatch(await browser('state'),/Article B/)
 }finally{await browser('close').catch(()=>{});await f.server.close()}
})
