// 宿主直连对照：绕过 adb reverse，直接打 127.0.0.1:8098。
// token 从 CDP 读进内存，既不打印也不落盘 —— 只输出状态码与响应体前缀。
// 用途：分清"设备侧的 reverse 指向错了"和"服务端真的返回无前缀"。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_PORT || '8098'
const PKG = 'com.kaixuan.opencode.pocket'

const adb = (args) => execFileSync(ADB, args, { encoding: 'utf8', timeout: 30000 })

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.error('app not running'); process.exit(1) }
const CDP_PORT = '9333'
try { adb(['-s', SERIAL, 'forward', '--remove', `tcp:${CDP_PORT}`]) } catch {}
adb(['-s', SERIAL, 'forward', `tcp:${CDP_PORT}`, `localabstract:webview_devtools_remote_${pid}`])

const list = await (await fetch(`http://127.0.0.1:${CDP_PORT}/json/list`)).json()
const page = list.find((t) => t.type === 'page' && t.webSocketDebuggerUrl)
if (!page) { console.error('no page target'); process.exit(1) }

const ws = new WebSocket(page.webSocketDebuggerUrl)
await new Promise((res, rej) => {
  ws.addEventListener('open', res, { once: true })
  ws.addEventListener('error', () => rej(new Error('ws error')), { once: true })
  setTimeout(() => rej(new Error('ws open timeout')), 15000)
})
let id = 0
const pending = new Map()
ws.addEventListener('message', (ev) => {
  const m = JSON.parse(ev.data)
  if (m.id && pending.has(m.id)) { const p = pending.get(m.id); pending.delete(m.id); p(m) }
})
const send = (method, params) => new Promise((res) => {
  const n = ++id
  pending.set(n, res)
  ws.send(JSON.stringify({ id: n, method, params }))
  setTimeout(() => { if (pending.has(n)) { pending.delete(n); res({ error: { message: 'timeout' } }) } }, 20000)
})

const r = await send('Runtime.evaluate', {
  expression: 'localStorage.getItem("pocket_token") || ""',
  returnByValue: true,
})
ws.close()
const token = r.result?.result?.value || ''
if (!token) { console.error('no token on device'); process.exit(1) }

const wav = 'UklGRiQAAABXQVZFZm10IBAAAAABAAEAESsAACJWAAACABAAZGF0YQAAAAA='
const res = await fetch(`http://127.0.0.1:${PORT}/api/stt/transcribe`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + token },
  body: JSON.stringify({ audioBase64: wav, filename: 'recording.wav' }),
})
const body = await res.text()
console.log(JSON.stringify({
  port: PORT,
  status: res.status,
  contentType: res.headers.get('content-type'),
  body: body.slice(0, 400),
  hasPrefix: body.includes('stt_unavailable:'),
}, null, 1))
