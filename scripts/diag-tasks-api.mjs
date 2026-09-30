// 诊断五：UI「暂无运行中的任务」但 PG 有 14 条 active —— 是 API 返回空，还是 UI 没渲染？
// 决定性做法：在页面上下文里用 App 自己的凭据直接打 /api/tasks，看 HTTP 状态与返回条数。
// 若 API 返回 14 条而 UI 显示 0 → 渲染/过滤 bug；若 API 返回 0 → 才是后端或凭据问题。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9366'
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
await sleep(4000)

// 用 App 自己的 token + API base 打一次，拿到真实 HTTP 状态与条数
const probe = await ev(`(async function(){
  var token = localStorage.getItem('pocket_token') || '';
  var base = localStorage.getItem('pocket_api_base') || '';
  var out = { origin: location.origin, base: base, tokenLen: token.length, tries: [] };
  var urls = [base + '/api/tasks', '/api/tasks'];
  for (var i = 0; i < urls.length; i++) {
    try {
      var r = await fetch(urls[i], { headers: token ? { Authorization: 'Bearer ' + token } : {} });
      var txt = await r.text();
      var parsed = null;
      try { parsed = JSON.parse(txt); } catch (e) {}
      var arr = Array.isArray(parsed) ? parsed : (parsed && (parsed.tasks || parsed.items || parsed.data)) || null;
      out.tries.push({
        url: urls[i], status: r.status,
        ctype: r.headers.get('content-type'),
        isArray: Array.isArray(parsed),
        count: Array.isArray(arr) ? arr.length : null,
        keys: parsed && !Array.isArray(parsed) ? Object.keys(parsed).slice(0,10) : null,
        head: txt.slice(0, 220)
      });
    } catch (e) { out.tries.push({ url: urls[i], error: String(e).slice(0,120) }); }
  }
  return JSON.stringify(out, null, 1);
})()`)
console.log('=== 页面内直接打 /api/tasks ===')
console.log(probe)
ws.close()
process.exit(0)
