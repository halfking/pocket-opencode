// 诊断六（假设验证）：TasksView.loadTasks() 用 `?source=opencode` 过滤，
// 但通过这个 UI 亲手创建的任务 source 是 'local' ⇒ 自己建的任务自己看不见。
// 用 API 对照：无过滤 vs source=opencode vs source=local，看条数差异。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9367'
const MASTER = process.env.POCKET_MASTER || 'PocketTest2026'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
await new Promise((r) => ws.addEventListener('open', r))
const send = (method, params = {}) =>
  new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })) })
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }))?.result?.value
await send('Runtime.enable')

if (await ev('!!document.querySelector(\'input[placeholder*="主密码"]\')')) {
  await ev(`(function(){var e=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set;s.call(e,${JSON.stringify(MASTER)});e.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`)
  await sleep(700)
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(x=>(x.textContent||'').trim()==='解锁');if(b)b.click();return 1})()`)
  await sleep(5000)
}
await ev(`location.hash='#/ai'`)
await sleep(3500)

const probe = await ev(`(async function(){
  var token = localStorage.getItem('pocket_token') || '';
  var base = localStorage.getItem('pocket_api_base') || '';
  var h = { Authorization: 'Bearer ' + token };
  var cases = ['', '?source=opencode', '?source=local', '?source=acc'];
  var out = [];
  for (var i = 0; i < cases.length; i++) {
    var r = await fetch(base + '/api/tasks' + cases[i], { headers: h });
    var j = await r.json();
    var arr = j.tasks || [];
    out.push({
      q: cases[i] || '(none)', status: r.status, n: arr.length,
      sources: arr.reduce(function(m,t){ m[t.source]=(m[t.source]||0)+1; return m; }, {}),
      statuses: arr.reduce(function(m,t){ m[t.status]=(m[t.status]||0)+1; return m; }, {}),
      titles: arr.slice(0,3).map(function(t){ return (t.title||'').slice(0,30) + ' [' + t.source + '/' + t.status + ']' })
    });
  }
  return JSON.stringify(out, null, 1);
})()`)
console.log('=== /api/tasks 按 source 对照 ===')
console.log(probe)
ws.close()
process.exit(0)
