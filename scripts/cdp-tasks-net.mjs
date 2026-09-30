// 抓真机 /tasks 页面的实际网络请求，定位「API 返回了 HTML 页面而非 JSON」的真实 URL
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9226'

const adb = (args, timeout = 60000) =>
  execFileSync(ADB, args, { encoding: 'utf8', timeout, maxBuffer: 32 * 1024 * 1024 })

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const all = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
const sock = all.find((s) => s.endsWith(`_${pid}`)) || all[all.length - 1]
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${sock}`])
const targets = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()
const page = targets.find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }
console.log('page url =', page.url, ' origin =', new URL(page.url).origin)

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const events = []
const send = (m, p = {}) => new Promise((res) => { const i = ++id; pending.set(i, res); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (ev) => {
  const m = JSON.parse(ev.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  const p = m.params || {}
  if (m.method === 'Network.requestWillBeSent' && p.request?.url?.includes('/api/')) {
    events.push(`REQ  ${p.request.method} ${p.request.url}`)
  }
  if (m.method === 'Network.responseReceived' && p.response?.url?.includes('/api/')) {
    events.push(`RESP ${p.response.status} ${p.response.mimeType} ${p.response.url}`)
  }
  if (m.method === 'Network.loadingFailed') {
    events.push(`FAIL err="${p.errorText}" blocked=${p.blockedReason ?? '-'} type=${p.type}`)
  }
  if (m.method === 'Runtime.consoleAPICalled') {
    const t = m.params.args.map((a) => a.value ?? a.description ?? '').join(' ')
    if (/task|Task|HTML|JSON/.test(t)) events.push(`CONSOLE.${m.params.type}: ${t.slice(0, 220)}`)
  }
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Network.enable')
await send('Runtime.enable')

// 直接在页面里复现旧代码会产生什么
console.log('\n--- how the browser resolves the buggy string ---')
const r = await send('Runtime.evaluate', {
  returnByValue: true,
  expression: `(function(){
    var origin = location.origin;
    var buggy = new URL('http://localhost:8088/api/tasks', origin).toString().replace(origin, '');
    var out = { origin: origin, buggy: buggy, resolved: null, fetchResult: null, fixed: null };
    try { out.resolved = new URL(buggy, location.href).toString(); } catch (e) { out.resolved = 'THROWS: ' + e.name; }
    out.fixed = 'http://localhost:8088/api/tasks';
    return JSON.stringify(out);
  })()`,
})
console.log(r?.result?.value)

console.log('\n--- live fetch of both URLs ---')
const f = await send('Runtime.evaluate', {
  returnByValue: true, awaitPromise: true,
  expression: `(async () => {
    var out = {};
    var t = localStorage.getItem('pocket_token');
    async function probe(name, url) {
      try {
        var res = await fetch(url, { headers: t ? { Authorization: 'Bearer ' + t } : {} });
        var body = await res.text();
        out[name] = res.status + ' ' + (res.headers.get('content-type')||'?') + ' :: ' + body.slice(0, 90);
      } catch (e) { out[name] = 'THROW ' + e.name + ': ' + e.message; }
    }
    var origin = location.origin;
    var buggy = new URL('http://localhost:8088/api/tasks', origin).toString().replace(origin, '');
    await probe('buggy', buggy);
    await probe('fixed', 'http://localhost:8088/api/tasks');
    return JSON.stringify(out, null, 2);
  })()`,
})
console.log(f?.result?.value)

console.log('\n--- navigate to #/tasks and capture ---')
events.length = 0
await send('Runtime.evaluate', { expression: `location.hash = '#/tasks'` })
await new Promise((r) => setTimeout(r, 5000))
for (const e of [...new Set(events)]) console.log(' ', e)
ws.close()
process.exit(0)
