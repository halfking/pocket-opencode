// diag-auth-boot.mjs —— 决定性证据：注入有效 token 后冷启动，App 到底做了什么。
//
// 为什么需要它：BUG-AX 回归判据的**对照分支**红着（有效 token 也被弹回登录页）。
// 两种完全不同的解释，必须靠网络层证据分开：
//   (a) App 根本没发 /api/* 就跳登录  ⇒ 前端状态/路由守卫的问题
//   (b) App 发了 /api/* 并收到 401      ⇒ 后端拒绝了有效 token（那才是真缺陷）
// 之前两次都只是看 hash 和页面文字，分不开这两者。
import { execFileSync } from 'node:child_process'
import http from 'node:http'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9272'
const API = process.env.POCKET_API_BASE || 'http://127.0.0.1:18099'
const PASS = process.env.POCKET_DEV_PASS || process.env.POCKET_AUTH_PASS || ''

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const withTimeout = (p, ms, what) => Promise.race([p, sleep(ms).then(() => { throw new Error(`${what} 超时 ${ms}ms`) })])
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

function api(path, { token, method = 'GET', body } = {}) {
  return new Promise((res) => {
    const payload = body ? JSON.stringify(body) : ''
    const h = {}
    if (token) h.Authorization = 'Bearer ' + token
    if (payload) { h['Content-Type'] = 'application/json'; h['Content-Length'] = Buffer.byteLength(payload) }
    const u = new URL(API)
    const req = http.request({ host: u.hostname, port: u.port || 80, path, method, headers: h, timeout: 15000 }, (r) => {
      let b = ''; r.on('data', (c) => (b += c)); r.on('end', () => res({ status: r.statusCode, body: b }))
    })
    req.on('error', (e) => res({ status: 0, body: String(e) }))
    req.on('timeout', () => { req.destroy(); res({ status: 0, body: 'timeout' }) })
    if (payload) req.write(payload)
    req.end()
  })
}

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const pages = await withTimeout(fetch(`http://127.0.0.1:${PORT}/json/list`).then((r) => r.json()), 20000, '/json/list')
const page = pages.find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))

let id = 0
const pending = new Map()
const events = []
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); return }
  if (m.method === 'Network.responseReceived') {
    const r = m.params.response
    if (/\/api\//.test(r.url)) events.push({ t: Date.now(), kind: 'resp', status: r.status, url: r.url.replace(API, '<API>') })
  }
  if (m.method === 'Network.requestWillBeSent') {
    const u = m.params.request.url
    if (/\/api\//.test(u)) events.push({ t: Date.now(), kind: 'req', url: u.replace(API, '<API>'), auth: !!m.params.request.headers?.Authorization })
  }
})
await withTimeout(new Promise((r) => ws.addEventListener('open', r)), 20000, 'ws open')
const send = (method, params = {}) => withTimeout(
  new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })) }), 20000, method)
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }))?.result?.result?.value

await send('Runtime.enable')
await send('Network.enable')

const login = await api('/api/auth/login', { method: 'POST', body: { username: 'admin', password: PASS } })
const real = JSON.parse(login.body)
console.log(`后端登录 ok  token=${real.token.length}字符 ws=${real.workspace_id} user=${real.user}`)

// 先验一次这个 token 确实有效（排除后端/脚本侧问题）
const probe = await api('/api/tasks', { token: real.token })
console.log(`宿主侧直接打 /api/tasks → status=${probe.status}  （这是"token 有效"的独立证据）`)

const t0 = Date.now()
await ev(`
  localStorage.setItem('pocket_token', ${JSON.stringify(real.token)});
  localStorage.setItem('pocket_user', ${JSON.stringify(real.user || 'admin')});
  localStorage.setItem('pocket_workspace_id', ${JSON.stringify(real.workspace_id || '')});
  localStorage.setItem('pocket_auth_method', 'password');
  return 1;
`)
console.log('已注入 4 个键，开始冷启动 …')
events.length = 0
await ev('location.reload()')
await sleep(18000)

console.log(`\n=== App 冷启动后 /api/* 请求序列（${events.length} 条）===`)
for (const e of events) {
  const dt = ((e.t - t0) / 1000).toFixed(1)
  console.log(`  +${dt}s  ${e.kind.toUpperCase().padEnd(4)} ${e.status ?? ''} ${e.url}${e.auth ? '  [带 Authorization]' : ''}`)
}
const sawApi = events.some((e) => e.kind === 'resp')
const saw401 = events.some((e) => e.status === 401)
const sawTasks = events.some((e) => /\/api\/tasks/.test(e.url))
console.log(`\nhash            = ${await ev('location.hash')}`)
console.log(`发过 /api/*     = ${sawApi ? '是' : '否 ← App 根本没请求就跳登录'}`)
console.log(`有 401          = ${saw401 ? '是' : '否'}`)
console.log(`打过 /api/tasks = ${sawTasks ? '是' : '否'}`)
console.log(`\n判读：${!sawApi ? '前端状态/路由守卫问题（没发请求就跳登录）' : saw401 ? '后端拒绝了有效 token —— 这才是真缺陷' : '请求成功但仍跳登录 —— 看前端路由逻辑'}`)
process.exit(0)
