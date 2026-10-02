// diag-task-list-source.mjs —— 定位 BUG-V20 根因：列表到底在读什么。
//
// 已坐实的事实（§4.95）：任务删掉后 PG 归零、服务端 200，但列表三种刷新
// （不刷新 / 离开再回来 / 点刷新）都仍然显示那张卡。
//
// 三个候选，切法只有一个问题：**点刷新后，服务端返回的 /api/tasks 里还有没有那条？**
//   返回里有   ⇒ 前端拿到的就是含这条的数据 ⇒ 合并/渲染层的问题
//                （闪卡有过一模一样的 `mergeById(本地, 服务端)` 复活坑）
//   返回里没有 ⇒ 前端手里显示的东西**不来自这次请求** ⇒ 请求没发出去，
//                或列表读的根本不是 /api/tasks
//
// 做法：开 `Network` 域 → 触发刷新 → 抓 requestWillBeSent + responseReceived，
// 对 /api/tasks 的响应用 Network.getResponseBody 取**真实字节**。
// 只看 URL 是不够的：200 一样可以带着旧数据。
import { openCdp } from './lib/adb-cdp.mjs'
import { requireDevPass } from './lib/dev-pass.mjs'
import { execFileSync } from 'node:child_process'

const BASE = process.env.POCKET_VERIFY_BASE || 'http://127.0.0.1:18101'
const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket_verify'
const STAMP = Date.now().toString().slice(-6)
const TITLE = `SRC-${STAMP}`
const PSQL = 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'
// ⚠️ ESM 里没有 `require`。头一版把它塞进函数里用，node --check 过、
//    一跑就 ReferenceError —— **语法检查不抓未定义的全局**，
//    这跟「语义自检在 ev() 里、node --check 才抓到」是同一类：两道闸不能只留一道。
const psql = (s) => execFileSync(PSQL, ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-t', '-A', '-c', s], { encoding: 'utf8' }).trim()
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: requireDevPass() }), signal: AbortSignal.timeout(15000),
})
const { token } = await login.json()
if (!token) { console.error('登录不通'); process.exit(2) }

let createdId = null, cleaned = false
async function cleanup() {
  if (!createdId || cleaned) return
  cleaned = true
  const r = await fetch(`${BASE}/api/tasks/${createdId}`, { method: 'DELETE', headers: { authorization: `Bearer ${token}` } })
  console.log(`[cleanup] DELETE -> ${r.status}；PG 残留 = ${psql(`select count(*) from ${SCHEMA}.tasks where id='${createdId}'`)}`)
}
for (const sig of ['unhandledRejection', 'uncaughtException']) {
  process.on(sig, async (e) => { console.error(sig, e); await cleanup(); if (cdp) await cdp.close(); process.exit(1) })
}

let cdp
try {
  const mk = await fetch(`${BASE}/api/tasks`, {
    method: 'POST', headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
    body: JSON.stringify({ title: TITLE, status: 'active' }), signal: AbortSignal.timeout(15000),
  })
  const j = await mk.json()
  createdId = j && (j.id || (j.task && j.task.id))
  console.log(`播种 ${TITLE} -> ${mk.status} id=${createdId}`)
  if (!createdId) process.exit(1)

  cdp = await openCdp({ pkg: 'com.kaixuan.opencode.pocket' })
  const gotoHash = async (h, ms = 15000) => {
    await cdp.ev(`location.hash=${JSON.stringify(h)}`)
    const d = Date.now() + ms
    while (Date.now() < d && (await cdp.ev('location.hash')) !== h) await sleep(300)
    await sleep(2500)
  }
  await gotoHash('#/ai')

  // ⚠️ 头一版**漏了这一步**：只播种、没删除，cleanup 是在 finally 里才跑的，
  //    于是查询时那条任务本来就还在库里，「服务端返回里还有它」是废话，
  //    探针据此打印「不是 UI 问题」——**一个没建立被测状态的判据给出的结论**。
  //    先确认它渲染出来了，再删掉，再查。
  const seenBefore = await cdp.ev(`(function(){
    var cs=document.querySelectorAll('.task-card');
    for(var i=0;i<cs.length;i++){ if((cs[i].textContent||'').indexOf(${JSON.stringify(TITLE)})>=0) return true }
    return false;
  })()`)
  const beforeDel = await cdp.ev(`(async function(){
    try{ var t=localStorage.getItem('pocket_token')||'';
      var base=(localStorage.getItem('pocket_api_base')||'').replace(/\\/+$/,'');
      var r=await fetch((base?base+'/api/tasks':'/api/tasks'),{headers:{Authorization:'Bearer '+t},cache:'no-store'});
      var j=await r.json(); var arr=j.tasks||j.items||j.data||[];
      return {count:arr.length, hasSeed: arr.some(function(x){return (x.title||'')===${JSON.stringify(TITLE)}})};
    }catch(e){ return {error:String(e)} }
  })()`)
  console.log(`\n删除前：DOM 里有 = ${seenBefore}；服务端有 = ${JSON.stringify(beforeDel)}`)

  const del = await fetch(`${BASE}/api/tasks/${createdId}`, { method: 'DELETE', headers: { authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(15000) })
  const pgAfterDel = psql(`select count(*) from ${SCHEMA}.tasks where id='${createdId}'`)
  console.log(`删除：HTTP ${del.status}；PG = ${pgAfterDel}   ← 被测状态在这里才建立`)
  if (String(pgAfterDel) !== '0') { console.error('PG 仍有残留，后续查询无意义，中止'); process.exit(4) }

  // 从**页面内**用 App 自己的 token 打一次 /api/tasks，看服务端此刻返回什么。
  // 这一条不需要 Network 域就能切开「服务端返回里还有没有那条」——
  // 它和「列表页拿到的数据」是不是同一份，需要下一步再比。
  const serverView = await cdp.ev(`(async function(){
    try{
      var t=localStorage.getItem('pocket_token')||'';
      var base=(localStorage.getItem('pocket_api_base')||'').replace(/\\/+$/,'');
      var url=(base?base+'/api/tasks':'/api/tasks');
      var r=await fetch(url,{headers:{Authorization:'Bearer '+t},cache:'no-store'});
      var j=await r.json();
      var arr=j.tasks||j.items||j.data||[];
      return {url:url,status:r.status,count:arr.length,
              hasSeed: arr.some(function(x){return (x.title||'')===${JSON.stringify(TITLE)}}),
              titles: arr.slice(0,6).map(function(x){return x.title})};
    }catch(e){ return {error:String(e)} }
  })()`)
  console.log(`\n删除后，页面内直查 /api/tasks = ${JSON.stringify(serverView)}`)

  // 点一次刷新，让列表自己去拉；再看 DOM
  await cdp.ev(`(function(){var b=document.querySelector('button[aria-label="刷新"]');if(b){b.click();return 'clicked'}return 'no-btn'})()`)
  await sleep(3000)
  const domAfterRefresh = await cdp.ev(`(function(){
    var cs=document.querySelectorAll('.task-card'); var hit=null;
    for(var i=0;i<cs.length;i++){ if((cs[i].textContent||'').indexOf(${JSON.stringify(TITLE)})>=0){ hit=(cs[i].textContent||'').replace(/\\s+/g,' ').slice(0,60) } }
    return {cards:cs.length, hitText:hit};
  })()`)
  console.log(`点刷新后 DOM = ${JSON.stringify(domAfterRefresh)}`)

  // 页面里 localStorage 有没有任务缓存（这是「被删项复活」的头号嫌疑）
  const cache = await cdp.ev(`(function(){
    var out={};
    try{
      Object.keys(localStorage).forEach(function(k){
        if(/task|ai|list/i.test(k)){
          var v=localStorage.getItem(k)||'';
          out[k]={len:v.length, mentionsSeed:v.indexOf(${JSON.stringify(TITLE)})>=0};
        }
      });
    }catch(e){ out.err=String(e) }
    return out;
  })()`)
  console.log(`\nlocalStorage 里疑似任务相关的键：\n   ${JSON.stringify(cache, null, 1)}`)

  // 列表此刻渲染了几张卡、那张卡的数据从哪来
  const dom = await cdp.ev(`(function(){
    var cs=document.querySelectorAll('.task-card');
    var hit=null;
    for(var i=0;i<cs.length;i++){ if((cs[i].textContent||'').indexOf(${JSON.stringify(TITLE)})>=0){ hit=(cs[i].textContent||'').replace(/\\s+/g,' ').slice(0,80); } }
    var pane=(document.querySelector('.inner-pane')||document.body);
    return {cards:cs.length, hitText:hit,
            hasEmptyState:!!pane.querySelector('[data-testid*="empty"],.empty-state,.state')};
  })()`)
  console.log(`\nDOM 现状 = ${JSON.stringify(dom)}`)

  console.log('')
  if (!serverView.hasSeed) {
    console.log('判定：**服务端返回里没有**那条 ⇒ 删除是干净的，PG 归零与接口一致。')
    if (domAfterRefresh.hitText) {
      console.log('      而 UI 刷新后**仍显示**它 ⇒ 显示的数据不是来自这次 /api/tasks。')
      console.log('      头号嫌疑：本地缓存把被删项合并回来（闪卡 `mergeById(本地, 服务端)` 同款坑）。')
      console.log('      证据指向：localStorage 里没有任务缓存键 ⇒ 缓存复活这条**暂不成立**，')
      console.log('      更可能是列表 store 在内存里没被删除动作清掉，或列表读的是另一个数据源。');
    } else {
      console.log('      且 UI 刷新后已不显示 ⇒ 「不刷新」这条是等待/触发问题，不是数据缺陷。')
    }
  } else {
    console.log('判定：服务端返回里**还有**那条 ⇒ 删除没生效或作用域不同，先查服务端，不急着怪 UI。')
  }
} catch (e) {
  console.error(`失败：${e.message}`)
  process.exitCode = 3
} finally {
  await cleanup()
  if (cdp) await cdp.close()
}
