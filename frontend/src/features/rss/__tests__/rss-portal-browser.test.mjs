import test from 'node:test'
import assert from 'node:assert/strict'
import {execFile}from'node:child_process'
import{promisify}from'node:util'
import{createRssPortalBrowserFixture}from'./rss-portal-browser.mjs'

test('RSS portable panel creates offline metadata and isolates cached owner/workspace views',{timeout:180000},async()=>{
  const f=await createRssPortalBrowserFixture(),exec=promisify(execFile),session=`rss-portal-${process.pid}`
  const browser=async(...args)=>(await exec('browser-use',['--session',session,...args],{timeout:60000})).stdout
  const control=async pattern=>{const state=await browser('state'),m=state.match(pattern);assert.ok(m,`Missing ${pattern}\n${state}`);return m[1]}
  const button=label=>control(new RegExp(`\\[(\\d+)\\]<button[^>]*\\/>\\s+${label}\\s*(?:\\n|$)`))
  const input=async(id,value)=>browser('input',await control(new RegExp(`\\[(\\d+)\\]<(?:input|textarea) id=${id}\\b`)),value)
  try{
    await browser('open',f.url);await browser('wait','text','RSS across devices')
    await browser('click',await button('New record'));await input('rss-portal-title','Alice offline source');await input('rss-portal-url','https://example.test/feed');await input('rss-portal-category','Category')
    await browser('click',await button('Save'));await browser('wait','text','Alice offline source')
    await input('rss-portal-search','Alice private research query');await browser('click',await button('Search'));await browser('wait','text','No shared RSS records yet')
    await browser('eval','window.rssFixture.pauseNextSourceRead()');await browser('click',await control(/\[(\d+)\]<button id=rss-refresh\b/));await browser('eval','new Promise(resolve=>{const poll=()=>window.__rssReadStarted?resolve(true):setTimeout(poll,10);poll()})')
    await browser('click',await button('Bob'));await browser('eval','window.rssFixture.releaseSourceRead()');await browser('wait','text','Backend B article');assert.doesNotMatch(await browser('state'),/Backend A article|Alice offline source/)
    assert.match(await browser('eval',"JSON.stringify(document.querySelector('#rss-portal-search').value)"),/result: ""/,'account activation must clear the former private query')
    await browser('click',await button('Alice'));await browser('wait','text','Alice offline source')
    await browser('click',await button('Hide/restore RSS'));await browser('click',await button('Bob'));await browser('click',await button('Hide/restore RSS'));await browser('wait','text','No shared RSS records yet');assert.doesNotMatch(await browser('state'),/Alice offline source/)
    await browser('click',await button('Alice'));await browser('wait','text','Alice offline source')
    await input('rss-portal-search','Alice workspace-specific private query')
    await browser('click',await button('Alice other workspace'));await browser('wait','text','No shared RSS records yet')
    assert.match(await browser('eval',"JSON.stringify(document.querySelector('#rss-portal-search').value)"),/result: ""/,'workspace activation must clear the former private query')
    await browser('click',await button('Remote update'));await browser('wait','text','REMOTE A/two')
    await browser('click',await button('Remote delete'));await browser('wait','text','No shared RSS records yet')
    await browser('click',await button('Alice'));await browser('wait','text','Alice offline source')
    await browser('click',await button('Article detail'));await browser('wait','text','Fetched A')
    await browser('click',await button('Save a shared record'));await browser('wait','text','Saved on this device')
    await browser('click',await button('Hide/restore RSS'));await browser('wait','text','Fetched A')
    assert.doesNotMatch(await browser('eval',"JSON.stringify(await window.rssFixture.localDB.bindOperation(window.rssFixture.localDB.captureOperation({kind:'local-edit'})).query(\"SELECT item_json FROM local_portal_records WHERE module='rss'\"))"),/FULL ARTICLE BODY/)
    await browser('click',await button('Edit'));await input('rss-portal-title','Unsaved Alice draft');await browser('click',await button('Bob'));await browser('wait','text','No shared RSS records yet');assert.doesNotMatch(await browser('state'),/Unsaved Alice draft/)
    await browser('click',await button('Sync error'));await browser('wait','text','The record remains on this device');await browser('click',await button('Retry'))
  }finally{await browser('close').catch(()=>{});await f.server.close()}
})
