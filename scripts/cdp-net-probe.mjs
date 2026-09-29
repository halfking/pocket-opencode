// CDP 网络层探针：抓 loadingFailed 的确切原因，绕开 console 的泛化 "Failed to fetch"
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'

// 按当前 App PID 选 WebView socket（head -1 可能拿到上次进程的残留 socket）
const pid = execFileSync(ADB, ['-s', SERIAL, 'shell', `pidof com.kaixuan.opencode.pocket`], { encoding: 'utf8' }).trim().split(/\s+/)[0]
const all = execFileSync(ADB, ['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`], { encoding: 'utf8' })
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
const sock = all.find((s) => s.endsWith(`_${pid}`)) || all[all.length - 1]
console.log('app pid =', pid, ' socket =', sock)
execFileSync(ADB, ['-s', SERIAL, 'forward', 'tcp:9223', `localabstract:${sock}`], { encoding: 'utf8' })

const targets = await (await fetch('http://127.0.0.1:9223/json/list')).json()
const page = targets.find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }
console.log('page url =', page.url)

const ws = new WebSocket(page.webSocketDebuggerUrl.replace('9222', '9223'))
let id = 0
const pending = new Map()
const send = (method, params = {}) =>
  new Promise((res) => { const i = ++id; pending.set(i, res); ws.send(JSON.stringify({ id: i, method, params })) })

const events = []
ws.addEventListener('message', (ev) => {
  const m = JSON.parse(ev.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  if (m.method === 'Network.loadingFailed') events.push({ ev: 'loadingFailed', ...m.params })
  if (m.method === 'Network.responseReceived') events.push({ ev: 'response', url: m.params.response.url, status: m.params.response.status, type: m.params.type })
  if (m.method === 'Network.requestWillBeSent') events.push({ ev: 'req', url: m.params.request.url })
})

await new Promise((r) => ws.addEventListener('open', r))
await send('Network.enable')
await send('Runtime.enable')

const target = 'http://192.168.31.20:8088/healthz'
await send('Runtime.evaluate', {
  expression: `fetch('${target}').then(r=>'ok'+r.status).catch(e=>'err:'+e.message)`,
  awaitPromise: true,
})
await new Promise((r) => setTimeout(r, 4000))

console.log('--- network events for', target, '---')
for (const e of events) {
  if (e.url && !e.url.includes('192.168.31.20')) continue
  if (e.ev === 'loadingFailed') {
    console.log(`loadingFailed: errorText="${e.errorText}" blockedReason="${e.blockedReason ?? '-'}" type=${e.type} corsErrorStatus=${e.corsErrorStatus ?? '-'}`)
  } else {
    console.log(`${e.ev}: ${e.url ?? ''} ${e.status ?? ''}`)
  }
}
if (!events.length) console.log('(no network events captured at all)')
ws.close()
process.exit(0)
