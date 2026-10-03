// CDP 握手能成（510ms），但 Runtime.evaluate 永不返回 —— 强烈指向
// 渲染进程主线程被阻塞。原生 window.confirm/alert/onbeforeunload 就是典型元凶：
// 对话框打开时 JS 线程停摆，任何 evaluate 都超时。
//
// 本探针：①监听 Page.javascriptDialogOpening ②evaluate 加超时 ③先试 Page.enable
import { execFileSync } from 'node:child_process'

const t0 = Date.now()
const mark = (s) => console.log(`[${String(Date.now() - t0).padStart(6)}ms] ${s}`)
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = '9403'

const adb = (a, t = 20000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
const sock = socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${sock}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
mark(`pid=${pid} page=${page.url}`)

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)({ ok: true, result: m.result, error: m.error }); pending.delete(m.id); return }
  if (m.method === 'Page.javascriptDialogOpening') {
    mark(`!!! JAVASCRIPT DIALOG OPENING: type=${m.params.type} message=${JSON.stringify(m.params.message)}`)
  }
  if (m.method === 'Inspector.targetCrashed') mark('!!! TARGET CRASHED')
  if (m.method === 'Runtime.exceptionThrown') mark('exception: ' + JSON.stringify(m.params?.exceptionDetails?.exception?.description || '').slice(0, 120))
})
await new Promise((r) => ws.addEventListener('open', r))
mark('ws open')

const send = (method, params = {}, timeoutMs = 6000) => new Promise((resolve) => {
  const i = ++id
  const timer = setTimeout(() => { pending.delete(i); resolve({ ok: false, timeout: true }) }, timeoutMs)
  pending.set(i, (v) => { clearTimeout(timer); resolve(v) })
  ws.send(JSON.stringify({ id: i, method, params }))
})

for (const m of ['Page.enable', 'Runtime.enable', 'DOM.enable']) {
  const r = await send(m)
  mark(`${m} -> ${r.ok ? 'ok' : 'TIMEOUT'}`)
}

const r1 = await send('Runtime.evaluate', { expression: '1+1', returnByValue: true }, 6000)
mark(`evaluate 1+1 -> ${JSON.stringify(r1)}`)

const r2 = await send('Runtime.evaluate', { expression: 'location.hash', returnByValue: true }, 6000)
mark(`evaluate location.hash -> ${JSON.stringify(r2)}`)

mark('closing ws (不让它影响设备)')
try { ws.close() } catch {}
adb(['-s', SERIAL, 'forward', '--remove', `tcp:${PORT}`])
mark('DONE')
process.exit(0)
