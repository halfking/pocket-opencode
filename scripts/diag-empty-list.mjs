// diag-empty-list.mjs — 任务列表为空这件事，到底是 App 没请求、请求失败，
// 还是后端真的返回了空？以及为什么 .refresh-indicator 的计算样式不是
// pointer-events: none（源码里明明写了 none）。
//
// 背景（2026-10-01 13:05）：PG 里 17 条任务一条不少，App 任务页却显示
// 「运行中 0 / 全部正常」且无任何错误；同时列表一空，「+ 新任务」就落到
// 下拉刷新提示文字底下，真触摸点不动（diag-tap-newtask.mjs A/B=0、C=1）。
// 这两件事要分开定性，不能混成一句「列表坏了」。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9611'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 30000) => execFileSync(ADB, ['-s', SERIAL, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (a) => { try { return adb(a, 10000) } catch { return '' } }

const pid = adbSoft(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adbSoft(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
let list = null
for (let i = 0; i < 4 && !list; i++) {
  try { list = await (await fetch(`http://127.0.0.1:${PORT}/json/list`, { signal: AbortSignal.timeout(8000) })).json() } catch { await sleep(2000) }
}
const page = list?.find((t) => t.type === 'page')
if (!page) { console.log('CDP_UNREACHABLE'); process.exit(4) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
if (!(await Promise.race([
  new Promise((r) => ws.addEventListener('open', () => r(true), { once: true })),
  new Promise((r) => setTimeout(() => r(false), 10000)),
]))) { console.log('CDP_OPEN_TIMEOUT'); process.exit(5) }
const ev = async (x, ms = 20000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { err: 'TIMEOUT' }
  if (v?.exceptionDetails) return { err: String(v.exceptionDetails.exception?.description || '').slice(0, 200) }
  return { val: v?.result?.value }
}

// 页内所有 await 都要套 Promise.race —— WebView 被节流时定时器会被钳到分钟级
const RACE = `(function(p,ms){return Promise.race([p,new Promise(function(r){setTimeout(function(){r('__TO__')},ms)})])})`

const out = await ev(`(async function(){
  var race = ${RACE};
  var res = {};
  res.hash = location.hash;
  res.fetchNative = String(window.fetch).includes('[native code]');
  var tok = null;
  try { tok = localStorage.getItem('pocket_token') || localStorage.getItem('token') || (JSON.parse(localStorage.getItem('pocket_user')||'{}').token) } catch(e) {}
  res.hasToken = !!tok;
  res.authKeys = [];
  for (var i=0;i<localStorage.length;i++){ var k=localStorage.key(i); if(/token|auth|user|server|workspace/i.test(k)) res.authKeys.push(k); }
  try {
    var h = tok ? { Authorization: 'Bearer ' + tok } : {};
    var r = await race(fetch('http://127.0.0.1:18099/api/tasks', { headers: h }), 12000);
    res.tasksStatus = '__TO__' === r ? 'TIMEOUT' : (r ? r.status : 'NO_RESPONSE');
    if (r && r.status) { var t = await race(r.text(), 8000); res.tasksBody = String(t).slice(0, 300); }
  } catch (e) { res.tasksErr = String(e && e.message || e).slice(0, 160); }
  res.cards = document.querySelectorAll('.task-card').length;
  res.cardsAny = document.querySelectorAll('.task-card, .completed-card, .blocked-card').length;
  res.emptyShown = ((document.querySelector('.empty-inline')||{}).innerText||'').slice(0,60);
  res.triageText = ((document.querySelector('.triage-pill')||{}).innerText||'').slice(0,40);
  res.indicatorPE = (function(){var e=document.querySelector('.refresh-indicator');return e?getComputedStyle(e).pointerEvents:'n/a'})();
  res.indicatorStyle = (function(){var e=document.querySelector('.refresh-indicator');if(!e)return 'n/a';var s=getComputedStyle(e);return 'pe='+s.pointerEvents+' zi='+s.zIndex+' pos='+s.position+' h='+s.height})();
  res.hintRect = (function(){var e=document.querySelector('.refresh-text');if(!e)return 'n/a';var r=e.getBoundingClientRect();return [Math.round(r.left),Math.round(r.top),Math.round(r.right),Math.round(r.bottom)].join(',')})();
  res.btnRect = (function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).filter(function(x){return (x.textContent||'').trim()==='+ 新任务'})[0];if(!b)return 'n/a';var r=b.getBoundingClientRect();return [Math.round(r.left),Math.round(r.top),Math.round(r.right),Math.round(r.bottom)].join(',')})();
  res.elementAtBtnCenter = (function(){
    var b=Array.prototype.slice.call(document.querySelectorAll('button')).filter(function(x){return (x.textContent||'').trim()==='+ 新任务'})[0];
    if(!b) return 'n/a';
    var r=b.getBoundingClientRect();
    var e=document.elementFromPoint(Math.round(r.left+r.width/2), Math.round(r.top+r.height/2));
    if(!e) return 'null';
    return e.tagName.toLowerCase()+'.'+(e.className||'').toString().slice(0,60);
  })();
  return JSON.stringify(res);
})()`)
console.log(out.err || out.val)
ws.close()
process.exit(0)
