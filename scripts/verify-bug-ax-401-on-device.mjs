// verify-bug-ax-401-on-device.mjs —— BUG-AX 真机回归判据（黑盒，走 CDP 驱动真实 App）。
//
// BUG-AX 原症状：`client.ts` 的 authFetch 整个面绕过 `http.ts` 的 401 兜底，
// 而 `TasksView.loadTasks()` 又把错误 catch 成空数组 ⇒ **401 被渲染成
// 「暂无运行中的任务」**，用户看到的是一个空列表，没有任何错误提示。
//
// 修复：`authFetch` 遇 401 调 `forceReauth()`（清本地态 + 跳 `#/login?reason=expired`）。
//
// 判据设计 —— 两条分支缺一不可：
//   分支 1（对照）：注入**有效** token → 必须留在应用内、列表有数据、不在登录页。
//   分支 2（被测）：注入**伪造** token → 必须跳到 `#/login`，且**页面不得出现
//                   「暂无运行中的任务」**。
//
// 为什么必须有对照：只跑分支 2 的话，"永远在登录页"这种判据也能通过。
// 分支 1 保证这套判据真能区分"登录页"和"应用内"。
//
// 用法：node scripts/verify-bug-ax-401-on-device.mjs
import { execFileSync } from 'node:child_process'
import http from 'node:http'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9271'
const API = process.env.POCKET_API_BASE || 'http://127.0.0.1:18099'
const PASS = process.env.POCKET_DEV_PASS || process.env.POCKET_AUTH_PASS || ''

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
// 硬规矩：任何 await 都要有上限，否则 CDP 半死会把整个脚本挂住，
// 表现为"没反应"而不是"失败"——排查起来贵得多。
const withTimeout = (p, ms, what) =>
  Promise.race([p, sleep(ms).then(() => { throw new Error(`${what} 超时 ${ms}ms`) })])
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

if (!PASS) { console.error('需要 POCKET_DEV_PASS 或 POCKET_AUTH_PASS'); process.exit(2) }

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

// ── 连接 CDP ──────────────────────────────────────────────────────
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
if (!socks.length) { console.log('NO_DEVTOOLS_SOCKET'); process.exit(2) }
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])

const list = await withTimeout(fetch(`http://127.0.0.1:${PORT}/json/list`).then((r) => r.json()), 20000, 'CDP /json/list')
const page = list.find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(2) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const netLog = []
// 硬规矩：解包必须用 m.result。传整条 m 的话 .result.value 恒为 undefined，
// 会被误判成"CDP 断了"，而真相只是解包写错了。
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); return }
  // 401 到底有没有发生，是「forceReauth 该不该跑」的唯一判据。
  // 没有它，# 路由停在别处时无法区分「没发请求」和「发了但没 401」。
  if (m.method === 'Network.responseReceived' && /\/api\/tasks/.test(m.params.response.url)) {
    netLog.push(`${m.params.response.status} ${m.params.response.url}`)
  }
})
await withTimeout(new Promise((r) => ws.addEventListener('open', r)), 20000, 'WebSocket open')
const send = (method, params = {}) => withTimeout(
  new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })) }),
  20000, `CDP ${method}`,
)
const ev = async (expr) => (await send('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true }))?.result?.result?.value
await send('Runtime.enable')
await send('Network.enable')

// ── 判据自检（缺了它，下面所有结论都不可信）──────────────────────────
// 2026-10-02 实测踩到：Runtime.enable 之后的**第一次** Runtime.evaluate
// 会返回 undefined（页内执行上下文还没就绪）。后果是
// `localStorage.getItem('pocket_token')` 读回来是 undefined，
// `|| ''` 把它变成"空字符串"，于是打印成「token 已被清空」——
// 一个**看起来完全正常的假结论**，差点被当成"App 启动即踢掉有效会话"。
// 这里用 `1+1` 先探一次，失败就等一拍重试；两者都失败才判判据不可用。
async function evSanity() {
  for (let i = 0; i < 6; i++) {
    const v = (await send('Runtime.evaluate', { expression: '1+1', returnByValue: true }))?.result?.result?.value
    if (v === 2) return true
    await settle(700)
  }
  return false
}
if (!(await evSanity())) {
  console.error('[判据不可用] 连续多次 `1+1` 都拿不到 2 —— CDP 执行上下文没就绪。')
  console.error('  在这种状态下读 localStorage 会得到 undefined，**不能**据此判断 token 有没有被清。')
  process.exit(2)
}
console.log('判据自检: 1+1 → 2 ✅（CDP 求值通道正常，下面的读取可信）')

const hash = () => withTimeout(ev('location.hash'), 15000, 'read hash')
const bodyText = () => withTimeout(ev('document.body.innerText'), 15000, 'read body')
const tokenNow = () => withTimeout(ev(`localStorage.getItem('pocket_token')`), 15000, 'read token')

// 后端侧拿一个真 token
const login = await api('/api/auth/login', { method: 'POST', body: { username: 'admin', password: PASS } })
let real
try { real = JSON.parse(login.body) } catch { real = null }
if (login.status !== 200 || !real?.token) {
  console.error(`❌ 后端登录失败 status=${login.status} body=${String(login.body).slice(0, 120)}`)
  process.exit(1)
}
console.log(`后端登录 ok (user=${real.user || '?'}, ws=${real.workspace_id || '?'})`)

async function settle(ms = 5000) { await sleep(ms) }

async function branch(name, tokenValue, expectLogin) {
  console.log(`\n──────── ${name} ────────`)
  // 三个键必须成套写。2026-10-02 第一版只写 pocket_token、把 pocket_user
  // 清掉、也不写 pocket_workspace_id，于是**有效 token 也被弹回登录页**，
  // 对照分支直接红。差点据此把"应用把有效会话也踢掉"当成产品缺陷报出去 ——
  // 那是判据欠定，不是产品行为。成套写完对照组才绿，结论才有意义。
  await ev(`
    localStorage.removeItem('pocket_token');
    localStorage.removeItem('pocket_user');
    localStorage.removeItem('pocket_workspace_id');
    // App 启动会恢复 pocket:lastRoute，并**覆盖**我们随后设置的 hash。
    // 2026-10-02 实测：没清它时脚本把路由设成 #/tasks，最后读回来的是
    // #/register / #/gateway/2/live 这类从没设过的路由，判据全盘失真。
    localStorage.removeItem('pocket:lastRoute');
    ${tokenValue ? `
      localStorage.setItem('pocket_token', ${JSON.stringify(tokenValue)});
      localStorage.setItem('pocket_user', ${JSON.stringify(real.user || 'admin')});
      localStorage.setItem('pocket_workspace_id', ${JSON.stringify(real.workspace_id || 'ws_user-admin')});
    ` : ''}
    return 1;
  `)
  // ⚠️ 顺序有讲究，踩过一次：
  //   1) 先 reload —— 让 auth store 从 localStorage 重新初始化（state 工厂读 localStorage）
  //   2) **再**设 hash 导航到 #/tasks
  // 反过来写（先设 hash 再 reload）的话，设 hash 那一刻进程内的 store 还是
  // 「已登出」，路由守卫当场把 URL 改写成 #/login?returnTo=…，随后 reload
  // 落在这个 URL 上 —— 于是**有效 token 也显示在登录页**，
  // 对照分支假红，差点被当成"App 把有效会话也踢掉"的产品缺陷。
  await ev('location.reload()')
  await settle(9000)          // 等 store 初始化完
  // 必须先跳走再跳回。vue-router 对**相同** target 是去重的：
  // 对照分支结束时已经在 #/tasks，再赋一次同样的 hash 不会发生导航，
  // 视图不重挂 ⇒ 首屏不拉数据 ⇒ /api/tasks 压根没发出。
  // 那样"没有 401"只是"没发请求"，判据就废了。
  await ev(`location.hash = '#/more'`)
  await settle(2500)
  netLog.length = 0
  await ev(`location.hash = '#/tasks'`)
  await settle(9000)          // 等路由 + 首屏拉取
  const saw401 = netLog.some((l) => l.startsWith('401'))
  console.log(`  /api/tasks 响应 = ${netLog.length ? netLog.join(' | ') : '(没发出 /api/tasks)'}  ${saw401 ? '← 401 确实发生了' : ''}`)
  const h = await hash()
  const txt = (await bodyText()) || ''
  const tok = await tokenNow()
  const atLogin = /#\/login/.test(h || '')
  // BUG-AX 的原始症状文案：401 被渲染成空列表
  const showsEmptyList = /暂无运行中的任务|没有运行中的任务/.test(txt)
  console.log(`  hash        = ${h}`)
  console.log(`  在登录页     = ${atLogin}`)
  console.log(`  显示空列表   = ${showsEmptyList}   ← BUG-AX 的原始症状`)
  console.log(`  token 还在   = ${tok ? '是' : '否（已被清）'}`)
  console.log(`  页面文本片段 = ${txt.replace(/\s+/g, ' ').slice(0, 90)}`)

  if (expectLogin) {
    const ok = atLogin && !showsEmptyList
    console.log(`  判定：${ok ? '✅ 401 走的是 forceReauth（跳登录页），没有被渲染成空列表' : '❌ 不满足「跳登录页 且 不显示空列表」'}`)
    return ok
  }
  const ok = !atLogin && !showsEmptyList
  console.log(`  判定：${ok ? '✅ 对照成立：有效 token 留在应用内（说明判据能区分登录页/应用内）' : '❌ 对照不成立 —— 这套判据连"应用内"都认不出来'}`)
  return ok
}

const control = await branch('分支1 对照：有效 token', real.token, false)
const subject = await branch('分支2 被测：伪造 token', 'zzz.not.a.real.token', true)

const pass = control && subject
console.log(`\n结论：${pass ? '✅ BUG-AX 真机回归通过（对照绿 + 被测绿）' : '❌ 未通过，control=' + control + ' subject=' + subject}`)
process.exit(pass ? 0 : 1)
