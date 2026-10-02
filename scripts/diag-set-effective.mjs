// diag-set-effective.mjs — 补上最后一环：注入的 localStorage 到底有没有写进去。
//
// 前面的结论"App 启动即清空 token"建立在一个未验证的前提上：
// **ev() 写的 4 个键真的落到 localStorage 了。**
// CDP 的 Runtime.evaluate 返回结构是 {result:{result:{value}}}，解包错一层
// 就会静默返回 undefined —— 表达式其实执行了，但我们看不见返回值，
// 于是"写入成功"与"写入失败"长得一模一样。
// 这里在 reload 之前当场读回，并且**显式检查返回值的 undefined**。
import { execFileSync } from 'node:child_process'
import http from 'node:http'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9274'
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
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id) }
})
await withTimeout(new Promise((r) => ws.addEventListener('open', r)), 20000, 'ws')
const send = (method, params = {}) => withTimeout(
  new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })) }), 20000, method)
const evRaw = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }))
const ev = async (x) => evRaw(x)?.result?.result?.value
await send('Runtime.enable')

// 先确认解包本身是对的：用一个必然有值的表达式
const sanity = await ev('1+1')
console.log(`解包自检: 1+1 → ${JSON.stringify(sanity)}  ${sanity === 2 ? '✅ 解包正确' : '❌ 解包就是坏的，前面所有 ev() 结论都要作废'}`)

const login = await api('/api/auth/login', { method: 'POST', body: { username: 'admin', password: PASS } })
const real = JSON.parse(login.body)
console.log(`token 长度=${real.token.length} user=${real.user} ws=${real.workspace_id}`)

const raw = await evRaw(`
  try {
    localStorage.setItem('pocket_token', ${JSON.stringify(real.token)});
    localStorage.setItem('pocket_user', ${JSON.stringify(real.user || 'admin')});
    localStorage.setItem('pocket_workspace_id', ${JSON.stringify(real.workspace_id || '')});
    JSON.stringify({
      ok: true,
      tokenLen: (localStorage.getItem('pocket_token') || '').length,
      user: localStorage.getItem('pocket_user'),
      ws: localStorage.getItem('pocket_workspace_id'),
      sameToken: localStorage.getItem('pocket_token') === ${JSON.stringify(real.token)},
    });
  } catch (err) { JSON.stringify({ ok: false, err: String(err) }); }
`)
const val = raw?.result?.result?.value
console.log(`\n写入后立刻读回（reload 之前）：\n  ${val}`)
let parsed = null
try { parsed = JSON.parse(val) } catch { /* 非 JSON */ }
if (!parsed) {
  console.log('❌ 没拿到 JSON —— 表达式没跑成或解包失败。先别下任何产品结论。')
  console.log(`   原始返回：${JSON.stringify(raw?.result ?? raw).slice(0, 300)}`)
  process.exit(2)
}
console.log(`\n写入是否真的生效：${parsed.ok && parsed.sameToken ? '✅ 是' : '❌ 否'}`)
process.exit(parsed.ok && parsed.sameToken ? 0 : 2)
