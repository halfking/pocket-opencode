#!/usr/bin/env node
/**
 * diag-marketplace-empty.mjs — 查「后端有包、UI 显示暂无」的成因。
 *
 * 现象：POST submit/review/publish 全部成功，`GET /api/marketplace/packages?kind=skill`
 * 用 admin token 明明返回 1 个包，但真机上 `/marketplace/skills` 显示「暂无技能包」。
 *
 * 三种可能，这里逐个测，不猜：
 *   A. workspace 不一致 —— 应用会话的 workspace 与 API 调用用的不是同一个
 *   B. 客户端过滤 —— store 拿到了但视图丢了（已排除：filtered 空搜索返回全量）
 *   C. 请求根本没发出 / 发出但被拒 —— 记 network 面板
 *
 * 用法：POCKET_SERIAL=... POCKET_MASTER=... node scripts/diag-marketplace-empty.mjs
 */
import { execFileSync } from 'node:child_process'
import { requireDevPass } from './lib/dev-pass.mjs'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9251'
const MASTER = process.env.POCKET_MASTER || ''
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const net = []
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  if (m.method === 'Network.responseReceived' && /marketplace/.test(m.params?.response?.url || '')) {
    net.push({ url: m.params.response.url, status: m.params.response.status, type: m.params.type })
  }
  if (m.method === 'Network.loadingFailed') net.push({ failed: m.params?.errorText, type: m.params?.type })
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
await send('Network.enable')
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }))?.result?.value

if (MASTER) {
  await ev(`location.hash = '#/login'`); await sleep(2600)
  if (await ev(`!!document.querySelector('input[placeholder*="主密码"]')`)) {
    await ev(`(function(){var el=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(MASTER)});el.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`)
    await sleep(1700)
    await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim().indexOf('解锁')>=0});if(b)b.click();return 1})()`)
    await sleep(4200)
  }
  if (await ev(`!!document.querySelector('input[placeholder*="用户名"]')`)) {
    const devPass = requireDevPass()
    const fillBy = (sel, val) => `(function(){var el=document.querySelector(${JSON.stringify(sel)});if(!el)return 'NF';var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(val)});el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return 'ok'})()`
    await ev(fillBy('input[placeholder*="用户名"]', 'admin'))
    await ev(fillBy('input[type="password"]', devPass)); await sleep(900)
    await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim().indexOf('登录')>=0});if(b)b.click();return 1})()`)
    await sleep(6500)
  }
}

console.log('--- 1. 应用会话里的 workspace 线索 ---')
console.log(await ev(`(function(){
  var out = {};
  try {
    for (var i=0;i<localStorage.length;i++){
      var k = localStorage.key(i);
      if (/workspace|ws_|auth|token|session/i.test(k)) {
        var v = localStorage.getItem(k) || '';
        out[k] = v.length > 160 ? v.slice(0,160)+'…' : v;
      }
    }
  } catch(e){ out.err = String(e); }
  return JSON.stringify(out, null, 1);
})()`))

console.log('\n--- 2. JWT 里的 workspace claim（解 payload，不打印签名）---')
console.log(await ev(`(function(){
  try {
    var keys = Object.keys(localStorage);
    for (var i=0;i<keys.length;i++){
      var v = localStorage.getItem(keys[i]) || '';
      var m = v.match(/eyJ[A-Za-z0-9_\\-]+\\.[A-Za-z0-9_\\-]+/);
      if (m) {
        var parts = m[0].split('.');
        var payload = JSON.parse(atob(parts[1].replace(/-/g,'+').replace(/_/g,'/')));
        return JSON.stringify({ from: keys[i], payload: payload }, null, 1);
      }
    }
    return 'NO_JWT_IN_LOCALSTORAGE';
  } catch(e){ return 'ERR ' + String(e); }
})()`))

console.log('\n--- 3. 从页面直接请求 packages（用应用自己的同源 + 会话）---')
console.log(await ev(`(async function(){
  try {
    var r = await fetch('/api/marketplace/packages?kind=skill', { headers: { Accept: 'application/json' } });
    var txt = await r.text();
    return JSON.stringify({ status: r.status, body: txt.slice(0, 400) }, null, 1);
  } catch(e){ return 'ERR ' + String(e); }
})()`))

console.log('\n--- 4. 重新导航到技能市场，抓 network ---')
net.length = 0
await ev(`location.hash = '#/marketplace/skills'`)
await sleep(4000)
console.log('network 事件:', JSON.stringify(net, null, 1))

console.log('\n--- 5. 页面上的实际文案 ---')
console.log(await ev(`(function(){
  var t = (document.body.innerText||'').replace(/\\s+/g,' ').trim();
  return JSON.stringify({ len: t.length, text: t.slice(0,260),
    articles: document.querySelectorAll('article').length }, null, 1);
})()`))
process.exit(0)
