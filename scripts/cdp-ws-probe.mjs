// WS 归因对照探针 v2：区分「Chromium 阻断」/「后端 404」/「401」/「真连不上」
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9224'

const adb = (args, timeout = 60000) =>
  execFileSync(ADB, args, { encoding: 'utf8', timeout, maxBuffer: 32 * 1024 * 1024 })

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const all = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
const sock = all.find((s) => s.endsWith(`_${pid}`)) || all[all.length - 1]
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${sock}`])
console.log('pid =', pid, 'sock =', sock)

const targets = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()
const page = targets.find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }
console.log('page url =', page.url)

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/9222|9223|9224/, PORT))
let id = 0
const pending = new Map()
const events = []
const send = (method, params = {}) =>
  new Promise((res) => { const i = ++id; pending.set(i, res); ws.send(JSON.stringify({ id: i, method, params })) })

ws.addEventListener('message', (ev) => {
  const m = JSON.parse(ev.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  const p = m.params || {}
  const tag = p.requestId
  if (m.method === 'Network.webSocketCreated') events.push(`${tag} created ${p.url}`)
  if (m.method === 'Network.webSocketWillSendHandshakeRequest') events.push(`${tag} handshake-sent`)
  if (m.method === 'Network.webSocketHandshakeResponseReceived') events.push(`${tag} RESPONSE status=${p.response.status}`)
  if (m.method === 'Network.webSocketFrameError') events.push(`${tag} FRAME-ERROR "${p.errorMessage}"`)
  if (m.method === 'Network.webSocketClosed') events.push(`${tag} closed`)
  if (m.method === 'Network.webSocketFrameReceived') events.push(`${tag} frame-received`)
  if (m.method === 'Network.loadingFailed') events.push(`${tag} loadingFailed err="${p.errorText}" blocked=${p.blockedReason ?? '-'}`)
  if (m.method === 'Log.entryAdded' && /websocket|WebSocket|Security|Mixed/i.test(p.entry.text)) {
    events.push(`LOG[${p.entry.level}] ${p.entry.text}`)
  }
})

await new Promise((r) => ws.addEventListener('open', r))
await send('Network.enable')
await send('Runtime.enable')
await send('Log.enable')

// 抓真实 token + 运行时解析出的 API base
const env = await send('Runtime.evaluate', {
  returnByValue: true,
  expression: `JSON.stringify({
    origin: location.origin,
    isSecureContext: window.isSecureContext,
    token: localStorage.getItem('pocket_token') || ''
  })`,
})
const info = JSON.parse(env.result.value)
console.log('origin =', info.origin, ' isSecureContext =', info.isSecureContext, ' tokenLen =', info.token.length)

// 顺带 hook 一次 App 自己的 WS URL：重放一次 connect 的 URL 计算
const appUrl = await send('Runtime.evaluate', {
  returnByValue: true,
  expression: `(async () => {
    const m = await import('/assets/' + '').catch(() => null)
    return 'skip'
  })()`.replace("'skip'", "(() => { try { return localStorage.getItem('pocket_api_base') || '(no pocket_api_base key)' } catch (e) { return 'err' } })()"),
})
console.log('app-recorded api base =', appUrl?.result?.value)

const cases = process.env.POCKET_WS_CASES
  ? process.env.POCKET_WS_CASES.split('|').map((s) => s.split('::'))
  : [
      ['A-404-loopback', 'ws://localhost:8088/__probe_no_such_route__'],
      ['C-401-badtoken', 'ws://localhost:8088/ws?token=probe'],
      ['D-real-token', `ws://localhost:8088/ws?token=${encodeURIComponent(info.token)}`],
      ['E-real-token-127', `ws://127.0.0.1:8088/ws?token=${encodeURIComponent(info.token)}`],
    ]

for (const [name, url] of cases) {
  events.length = 0
  const r = await send('Runtime.evaluate', {
    returnByValue: true, awaitPromise: true,
    expression: `new Promise((res)=>{let d=false;const f=s=>{if(!d){d=true;res(s)}};
      let ws;try{ws=new WebSocket(${JSON.stringify(url)})}catch(e){return f('ctor-throw:'+e.message)}
      ws.onopen=()=>f('OPEN');
      ws.onerror=()=>f('ERROR');
      ws.onclose=e=>f('CLOSE code='+e.code+' clean='+e.wasClean);
      setTimeout(()=>f('TIMEOUT(4s)'),4000)})`,
  })
  console.log(`\n### ${name}  ${url.replace(info.token, '<REALTOKEN>').slice(0, 80)}`)
  console.log('   in-page result:', r?.result?.value ?? JSON.stringify(r))
  await new Promise((x) => setTimeout(x, 600))
  for (const e of events) console.log('   ', e)
}
ws.close()
process.exit(0)
