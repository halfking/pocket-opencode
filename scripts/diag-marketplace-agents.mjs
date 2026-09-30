// 定性 /api/marketplace/agents 到底是 404 还是 401。
// 只读探测，不用设备 token 会先被鉴权中间件拦下（401），
// 无论路由是否存在都是 401 —— 那样测不出 404。
// 这里用设备 localStorage 里的有效 token 复测，路由不存在才会露 404。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9390'
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
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='解锁'});if(b)b.click();return 1})()`)
  await sleep(5000)
}

const r = await ev(`(async function(){
  var token = localStorage.getItem('pocket_token') || '';
  var base = localStorage.getItem('pocket_api_base') || '';
  var h = { Authorization: 'Bearer ' + token };
  var out = [];
  var urls = [
    '/api/marketplace/agents',
    '/api/marketplace/packages',
    '/api/agents',
    '/api/marketplace/skills'
  ];
  for (var i = 0; i < urls.length; i++) {
    try {
      var res = await fetch(base + urls[i], { headers: h });
      var txt = await res.text();
      out.push({ url: urls[i], withToken: res.status, ctype: res.headers.get('content-type'), head: txt.slice(0, 120) });
    } catch (e) { out.push({ url: urls[i], error: String(e).slice(0, 100) }); }
  }
  return JSON.stringify({ tokenLen: token.length, results: out }, null, 1);
})()`)
console.log(r)
ws.close()
process.exit(0)
