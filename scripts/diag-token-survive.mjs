// diag-token-survive.mjs — 最小切分：token 是在「启动时」被清，还是「导航时」被清。
//
// 前两版判据都在同一处卡住：注入有效 token 后，App 最终都停在登录页。
// 可能的原因有两类，修法完全不同：
//   (a) 启动即清（boot 里有会话校验，失败就登出）  ⇒ 改启动逻辑
//   (b) 导航时才清（路由守卫/首屏请求拿到 401）    ⇒ 改守卫或请求层
// 所以这里**不导航**，只 reload，然后立刻读 localStorage。
import { execFileSync } from 'node:child_process'
import http from 'node:http'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9273'
const API = process.env.POCKET_API_BASE || 'http://127.0.0.1:18099'
const PASS = process.env.POCKET_DEV_PASS || process.env.POCKET_AUTH_PASS || ''

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const withTimeout = (p, ms, w) => Promise.race([p, sleep(ms).then(() => { throw new Error(`${w} 超时`) })])
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
const netLog = []
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); return }
  if (m.method === 'Network.responseReceived' && /\/api\//.test(m.params.response.url)) {
    netLog.push(`${m.params.response.status} ${m.params.response.url.replace(API, '<API>')}`)
  }
})
await withTimeout(new Promise((r) => ws.addEventListener('open', r)), 20000, 'ws')
const send = (method, params = {}) => withTimeout(
  new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })) }), 20000, method)
const evRaw = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }))
// 2026-10-02：原来的 ev() 在这台设备上对 `1+1` 就返回 undefined，
// 于是「token 被清空」这类**读取**结论全是坏判据读出来的假象。
// 现在读 localStorage / hash 一律走 evStrict，把 undefined 单独标出来，
// 不与「空字符串」混为一谈 —— 这正是它当初骗过我的地方。
const ev = async (x) => evRaw(x)?.result?.result?.value
const evStrict = async (x, what) => {
  const raw = await evRaw(x)
  if (raw?.result?.exceptionDetails) {
    throw new Error(`${what}: 页内抛异常 -> ${raw.result.exceptionDetails.text || 'exception'}`)
  }
  return raw?.result?.result?.value
}
await send('Runtime.enable')
await send('Network.enable')

const login = await api('/api/auth/login', { method: 'POST', body: { username: 'admin', password: PASS } })
const real = JSON.parse(login.body)
const probe = await api('/api/tasks', { token: real.token })
console.log(`后端 token 长度=${real.token.length}  宿主侧 /api/tasks=${probe.status}（200=token 确实有效）`)
console.log(`reload 前：hash=${await ev('location.hash')}  token=${(await ev(`localStorage.getItem('pocket_token')`) || '(无)').slice(0, 12)}`)

await ev(`
  localStorage.setItem('pocket_token', ${JSON.stringify(real.token)});
  localStorage.setItem('pocket_user', ${JSON.stringify(real.user || 'admin')});
  localStorage.setItem('pocket_workspace_id', ${JSON.stringify(real.workspace_id || '')});
  return 1;
`)
netLog.length = 0
await ev('location.reload()')
await sleep(12000)

const tok = (await evStrict(`localStorage.getItem('pocket_token')`, '读 pocket_token')) ?? '(读取失败:undefined)'
const usr = (await evStrict(`localStorage.getItem('pocket_user')`, '读 pocket_user')) ?? '(读取失败:undefined)'
const h = await evStrict('location.hash', '读 hash')
console.log(`\nreload 后（未导航）：`)
console.log(`  hash          = ${h}`)
console.log(`  pocket_token  = ${tok ? tok.slice(0, 12) + '…(' + tok.length + '字符)' : '(已被清空)'}`)
console.log(`  pocket_user   = ${usr || '(已被清空)'}`)
console.log(`  启动期 /api/* =`)
for (const l of netLog) console.log(`     ${l}`)

const clearedAtBoot = !tok
console.log(`\n判读：${clearedAtBoot
  ? '(a) **启动即被清空** —— boot 里有会话校验，失败即登出。问题在启动逻辑，不在路由守卫。'
  : 'token 挺过了启动。(b) 若是「导航时才清」，那问题在路由守卫/首屏请求，不在启动逻辑。'}`)
process.exit(0)
