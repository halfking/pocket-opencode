// 最小探针：逐个打印 verify-scheduled-task-writepath 在建连阶段的每一步，
// 定位「零输出就卡住」到底卡在哪一行。
// 不做任何业务动作，只走到 CDP 握手为止。
import { execFileSync } from 'node:child_process'

const t0 = Date.now()
const mark = (s) => console.log(`[${String(Date.now() - t0).padStart(6)}ms] ${s}`)

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9402'

mark('start')
const adb = (a, t = 20000) => {
  mark(`adb ${a.join(' ')}`)
  const r = execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
  mark('  -> ok')
  return r
}

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
mark(`pid=${pid}`)
if (!pid) { mark('app not running'); process.exit(2) }

const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
mark(`sockets=${JSON.stringify(socks)}`)
const sock = socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]
mark(`chosen=${sock}`)

adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${sock}`])

mark(`fetch http://127.0.0.1:${PORT}/json/list`)
const res = await fetch(`http://127.0.0.1:${PORT}/json/list`)
mark(`fetch status=${res.status}`)
const targets = await res.json()
mark(`targets=${targets.length}`)
targets.forEach((t) => mark(`  type=${t.type} url=${(t.url || '').slice(0, 60)}`))
const page = targets.find((t) => t.type === 'page')
if (!page) { mark('no page target'); process.exit(3) }

mark('open websocket')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
await new Promise((res2, rej) => {
  const t = setTimeout(() => rej(new Error('ws open timeout 15s')), 15000)
  ws.addEventListener('open', () => { clearTimeout(t); res2() })
  ws.addEventListener('error', (e) => { clearTimeout(t); rej(new Error('ws error')) })
})
mark('websocket open')

// 验证 Runtime.evaluate 通路
let id = 0
const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
const send = (method, params = {}) =>
  new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })) })
const r = await send('Runtime.evaluate', { expression: 'location.hash', returnByValue: true })
mark(`evaluate -> hash=${r?.result?.value}`)

// 清理 forward
execFileSync(ADB, ['-s', SERIAL, 'forward', '--remove', `tcp:${PORT}`])
mark('forward removed')
mark('ALL OK')
ws.close()
process.exit(0)
