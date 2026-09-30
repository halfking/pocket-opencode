// 查真机 localStorage 的 API base override + App 自身 WS 的真实目标
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9227'

const adb = (args, timeout = 60000) =>
  execFileSync(ADB, args, { encoding: 'utf8', timeout, maxBuffer: 32 * 1024 * 1024 })
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const all = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
const sock = all.find((s) => s.endsWith(`_${pid}`)) || all[all.length - 1]
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${sock}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }
console.log('page =', page.url)

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const events = []
const send = (m, p = {}) => new Promise((res) => { const i = ++id; pending.set(i, res); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (ev) => {
  const m = JSON.parse(ev.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  const p = m.params || {}
  if (m.method === 'Network.webSocketCreated') events.push(`WS-CREATED ${String(p.url).replace(/token=[^&]+/, 'token=<redacted>')}`)
  if (m.method === 'Network.webSocketHandshakeResponseReceived') events.push(`WS-RESP ${p.response.status}`)
  if (m.method === 'Network.webSocketFrameError') events.push(`WS-ERR "${p.errorMessage}"`)
  if (m.method === 'Network.webSocketFrameReceived') events.push(`WS-FRAME ${String(p.response?.payloadData ?? '').slice(0, 100)}`)
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Network.enable')
await send('Runtime.enable')

const info = await send('Runtime.evaluate', {
  returnByValue: true,
  expression: `JSON.stringify({
    origin: location.origin,
    pocket_api_base: localStorage.getItem('pocket_api_base'),
    allKeys: Object.keys(localStorage).filter(k => /api|server|base/i.test(k)),
  })`,
})
console.log('localStorage =', info?.result?.value)

// hook XHR/fetch 真实出口，再触发 App 自己的重连
events.length = 0
await send('Runtime.evaluate', {
  expression: `(function(){
    window.__seen = [];
    var of = window.fetch;
    window.fetch = function(input, init) {
      try { window.__seen.push(String(input && input.url ? input.url : input).slice(0, 120)); } catch (e) {}
      return of.apply(this, arguments);
    };
    // 强制 App 重连 WS：把 socket 换掉
    try { window.__forceReconnect && window.__forceReconnect(); } catch (e) {}
    return 'hooked';
  })()`,
})
console.log('\n--- navigate to #/ai (triggers App WS) and capture 12s ---')
await send('Runtime.evaluate', { expression: `location.hash = '#/ai'` })
await new Promise((r) => setTimeout(r, 12000))
const seen = await send('Runtime.evaluate', { returnByValue: true, expression: `JSON.stringify([...new Set(window.__seen || [])])` })
console.log('fetch targets =', seen?.result?.value)
console.log('\nCDP events:')
for (const e of [...new Set(events)].slice(0, 20)) console.log(' ', e)
ws.close()
process.exit(0)
