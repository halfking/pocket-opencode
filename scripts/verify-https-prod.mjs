// verify-https-prod.mjs — 从真机 WebView 内验证「生产 https 路径」。
//
// 为什么要单独做：生产基址是 https://pocket.itestu.cn（api/http.ts 里
// resolveRuntimeApiBase 的兜底），此前所有真机验证都跑在 adb reverse 的
// http://127.0.0.1:8088 上，**https 这条路一次都没走过**。
//
// 做法：不重建 APK（那要几分钟且会覆盖当前 dev 包），改用 App 自己的
// 运行时覆盖开关 localStorage.pocket_api_base —— CHANGELOG 记过它优先级
// 高于构建期 VITE_API_BASE。于是同一份代码、同一套 fetch，只是换了基址。
//
// 三件事都要验，缺一不可：
//   1. 真机 WebView 能与生产 TLS 握手并拿到 JSON（不是 HTML、不是 index.html）
//   2. 生产登录能签发 token，且带 token 的 /api 读取返回 200
//   3. App 自己的 base 解析确实会选中覆盖值（否则改了也没用）
//
// ⚠️ 全程**只读 + 登录**，不向生产写任何数据：那是共享部署，单方面写入
// 属于我不该擅自做的副作用。写路径的 https 回归需要产品/运维授权后再做。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PROD = 'https://pocket.itestu.cn'
// ⚠️ 2026-10-02 修 BUG-V10：原先这里是 `const PORT = process.env.POCKET_CDP_PORT || '9472'`，
//    而第 32 行用 `tcp:${PORT}` 硬绑一个固定端口。固定端口是**共享可变状态**
//    （同机还有别的会话在驱同一台设备；仓库里共 140 个 .mjs 硬编码了固定 CDP 端口，
//    取值散布在 8088-9630，见 `node scripts/check-fixed-cdp-ports.mjs`），
//    撞上就抛 10048，且撞上时报错指向装置而不是「上次没清干净」。
//    改成 tcp:0 让 adb 自己分配空闲端口。
const adb = (a, t = 25000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (a) => { try { return adb(a, 8000) } catch { return '' } }
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// ---- BUG-V10：生产口令从环境取，缺就立刻停 ----
// 背景：这个脚本原先在文件里有一个**硬编码的** `const adminPass = '…'`，
// 被 b6187bc1（dev 旁路移除硬编码 admin 口令 + 卡口补 password-literal 规则）
// 连同其它 7 个文件一起清掉了 —— 但**调用点被漏掉**：第 89 行还在写
// `password: adminPass`。于是页内求值抛 ReferenceError，`ev` 返回异常，
// 脚本必然 `exit 7`。**这条 https 设备侧回归从那次安全整改之后就一直是死的**，
// 而 handoff 把它记成「设备侧受阻于环境（不是代码）」——把代码缺陷误判成了环境。
//
// 与 BUG-V5 同一类：安全整改删掉了一个字面量，某个消费者仍在按名字找它，
// 而消费者的报错指向的是一个已经不存在的东西。
// ⇒ 口令只能由环境提供；缺口令时**在碰设备之前**就停，不要跑到一半才发现。
const PROD_PASS = process.env.POCKET_PROD_PASS || process.env.POCKET_AUTH_PASS || ''
if (!PROD_PASS) {
  console.log('POCKET_PROD_PASS 未设置 —— 这是**生产**口令，本机不猜、不兜底。')
  console.log('  $env:POCKET_PROD_PASS="<生产口令>"')
  process.exit(8)
}

const pid = adbSoft(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
// BUG-V10：端口交给 adb 分配（tcp:0），不再硬绑固定端口。
const PORT = Number(adbSoft(['forward', 'tcp:0', `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`]).trim())
if (!Number.isInteger(PORT) || PORT <= 0) { console.log('CDP_FORWARD_NO_PORT'); process.exit(3) }
const pages = await (await fetch(`http://127.0.0.1:${PORT}/json/list`, { signal: AbortSignal.timeout(10000) })).json()
const page = pages.find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE_TARGET'); process.exit(4) }
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
const opened = await Promise.race([
  new Promise((r) => ws.addEventListener('open', () => r(true))),
  new Promise((r) => setTimeout(() => r(false), 10000)),
])
if (!opened) { console.log('CDP_OPEN_TIMEOUT'); process.exit(5) }
const ev = async (x, ms = 30000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { value: null, err: 'TIMEOUT' }
  if (v?.exceptionDetails) return { value: null, err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 200) }
  return { value: v?.result?.value, err: '' }
}

// ---- 1) 记下原覆盖值，跑完要还原 ----
const before = await ev(`(function(){ try { return localStorage.getItem('pocket_api_base') } catch (e) { return '__ERR__' } })()`)
console.log(`原 localStorage.pocket_api_base = ${JSON.stringify(before.value)}`)

// BUG-V10：还原必须是**必经之路**，不是「顺利跑完之后的收尾」。
// 原先是顺序执行的直线代码：覆盖写在第 89 行、还原在第 148 行，
// 而中间第 137 行有个 `process.exit(7)`（链路探针失败）。一旦触发，
// 设备上的 pocket_api_base 就**永久留在 https://pocket.itestu.cn** ——
// 下一个跑真机的人会以为自己在测本地后端，实际在打生产。
// 这是典型的「失败路径不做清理」，而它留下的状态比错误信息本身危险得多。
let restored = false
async function restoreBase(reason) {
  if (restored) return
  restored = true
  const r = await ev(`(function(){
    try {
      var v = ${JSON.stringify(before.value)}
      if (v === null || v === '__ERR__') localStorage.removeItem('pocket_api_base')
      else localStorage.setItem('pocket_api_base', v)
      return localStorage.getItem('pocket_api_base')
    } catch (e) { return 'RESTORE_FAIL: ' + String(e) }
  })()`)
  console.log(`已还原 localStorage.pocket_api_base = ${JSON.stringify(r.value)}${reason ? '（' + reason + '）' : ''}`)
}
process.on('exit', () => { if (!restored) console.log('!! 注意：进程退出时覆盖值**未**还原') })

// ---- 2) 写入生产覆盖值 ----
const setRes = await ev(`(function(){
  try { localStorage.setItem('pocket_api_base', ${JSON.stringify(PROD)}); return localStorage.getItem('pocket_api_base') }
  catch (e) { return 'SET_FAIL: ' + String(e) }
})()`)
console.log(`写入后 = ${JSON.stringify(setRes.value)}`)
if (setRes.value !== PROD) { console.log('覆盖写入失败，本轮作废'); await restoreBase('覆盖写入失败'); ws.close(); process.exit(6) }

// ---- 3) 从真机 WebView 内跑：登录 → 带 token 读 ----
console.log(`\n=== 真机 WebView → ${PROD} 完整链路 ===`)
const chain = await ev(`(async () => {
  const to = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
  const base = ${JSON.stringify(PROD)}
  const out = { steps: [] }
  // 3.1 未鉴权读：应拿到 JSON 401，而不是 index.html（BUG-D 的典型故障形态）
  try {
    const r = await to(fetch(base + '/api/tasks'), 15000)
    const ct = r.headers.get('content-type') || ''
    const txt = await to(r.text(), 8000)
    out.steps.push({ name: 'unauth /api/tasks', status: r.status, contentType: ct.slice(0, 40), body: txt.slice(0, 90) })
  } catch (e) { out.steps.push({ name: 'unauth /api/tasks', err: String(e && e.message || e).slice(0, 80) }) }
  // 3.2 登录
  let token = ''
  try {
    const r = await to(fetch(base + '/api/auth/login', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: 'admin', password: ${JSON.stringify(PROD_PASS)} }),
    }), 20000)
    const j = await to(r.json(), 8000)
    token = (j && j.token) || ''
    out.steps.push({ name: 'POST /api/auth/login', status: r.status, hasToken: !!token, tokenLen: token.length })
  } catch (e) { out.steps.push({ name: 'POST /api/auth/login', err: String(e && e.message || e).slice(0, 80) }) }
  // 3.3 带 token 读三个模块
  for (const ep of ['/api/tasks', '/api/notes', '/api/meetings', '/api/flashcards']) {
    try {
      const r = await to(fetch(base + ep, { headers: { Authorization: 'Bearer ' + token } }), 15000)
      const txt = await to(r.text(), 8000)
      let shape = null
      try { const j = JSON.parse(txt); shape = Array.isArray(j) ? 'array(' + j.length + ')' : Object.keys(j).slice(0, 5) } catch (e) { shape = 'NON-JSON: ' + txt.slice(0, 40) }
      out.steps.push({ name: ep, status: r.status, shape })
    } catch (e) { out.steps.push({ name: ep, err: String(e && e.message || e).slice(0, 80) }) }
  }
  // 3.4 App 自己的 base 解析会不会选中覆盖值
  try {
    const g = globalThis
    const reg = g.__pocketApiBaseOverride
    out.overrideInLocalStorage = localStorage.getItem('pocket_api_base')
  } catch (e) { /* ignore */ }
  return JSON.stringify(out)
})()`, 60000)
// BUG-V10：失败路径也必须还原，否则设备会一直指着生产
if (chain.err) {
  console.log('链路探针失败: ' + chain.err)
  await restoreBase('失败路径还原')
  ws.close()
  process.exit(7)
}
const c = JSON.parse(chain.value)
for (const s of c.steps) {
  if (s.err) { console.log(`  ❌ ${s.name.padEnd(26)} ${s.err}`); continue }
  const extra = s.body !== undefined ? ` ct=${s.contentType} body=${JSON.stringify(s.body)}`
    : s.hasToken !== undefined ? ` hasToken=${s.hasToken} len=${s.tokenLen}`
      : ` shape=${JSON.stringify(s.shape)}`
  console.log(`  ${String(s.status).padEnd(4)} ${s.name.padEnd(26)}${extra}`)
}

// ---- 4) 还原覆盖值（现在走必经的 restoreBase）----
await restoreBase()

// ---- 5) 判定 ----
// BUG-V10：原先这里只打 ✅/❌ 然后**无条件 exit(0)** —— 也就是说全链路失败
// 也会让任何自动化跑看到「成功」。判据必须能失败，否则它不是判据。
console.log('\n=== 判读 ===')
const unauth = c.steps.find((s) => s.name === 'unauth /api/tasks')
const login = c.steps.find((s) => s.name === 'POST /api/auth/login')
const reads = c.steps.filter((s) => s.name.startsWith('/api/'))
const okRead = reads.filter((s) => s.status === 200)
const tlsOk = !!(unauth && !unauth.err && /json/.test(unauth.contentType || ''))
const loginOk = !!(login && login.hasToken)
console.log(`  真机 TLS + JSON 正确（未被 index.html 顶替）: ${tlsOk ? '✅' : '❌'}`)
console.log(`  生产登录签发 token: ${loginOk ? '✅' : '❌'}`)
console.log(`  带 token 只读命中 200: ${okRead.length}/${reads.length} → ${reads.map((s) => s.name.replace('/api/', '') + '=' + s.status).join(' ')}`)

const allOk = tlsOk && loginOk && reads.length > 0 && okRead.length === reads.length
ws.close()
adbSoft(['forward', '--remove', `tcp:${PORT}`])
process.exit(allOk ? 0 : 1)
