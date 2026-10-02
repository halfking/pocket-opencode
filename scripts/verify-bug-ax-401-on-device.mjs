// verify-bug-ax-401-on-device.mjs —— BUG-AX 真机回归判据（黑盒，走 CDP 驱动真实 App）。
//
// BUG-AX 原症状：`client.ts` 的 authFetch 整个面绕过 `http.ts` 的 401 兜底，
// 而 `TasksView.loadTasks()` 又把错误 catch 成空数组 ⇒ **401 被渲染成
// 「暂无运行中的任务」**，用户看到的是一个空列表，没有任何错误提示，也回不去登录页。
// 修复：`client.ts:53` —— `authFetch` 遇 401 调 `forceReauth()`（清本地态 + 跳登录页）。
// 触发场景就写在代码注释里：「后端换了 JWT secret → 设备上旧 token 全 401」。
//
// ══ 三条踩坑换来的设计约束（每一条都对应一次实测失败）══════════════════
//
// 1) 被测支不能伪造 token。App 启动时 POST /api/auth/refresh 做 JWT 滑动续期
//    （http.ts REFRESH_PATH），伪造 token 在启动期就被 401 清掉，/api/tasks
//    压根不会发出。后端也没有吊销/黑名单（/api/auth/logout 只撤 RedClaw session，
//    本地 dev 走 POCKET_AUTH_LEGACY_ONLY 没有 RedClaw）。所以唯一忠实的复现是
//    **换 JWT secret，让一个真实且已在应用内的会话被作废**。
//
// 2) 判别式不能用「暂无运行中的任务」。TasksView.vue:198 是
//    `activeTasks.length > 0 ? '当前筛选下没有任务' : '暂无运行中的任务'`，
//    而 activeTasks 过滤 `status === 'active'`。没有运行中任务是**健康状态**，
//    那句文案照样出现 ⇒ 用它当信号 = 恒真判据。
//    正确做法：先造一条 status=active 的任务做夹具，判别式改成
//    「对照支必须看见那条任务；被测支（401 之后）绝不能再看见它」。
//
// 3) 设备侧的 `adb reverse` 映射是**共享可变状态**。实测：判据自己把
//    tcp:18099 指到自己的 18100，几分钟后被并发会话改回 tcp:18099，
//    于是 App 对着别人的后端跑，拿到 401、日志 reason=expired，
//    看起来像"App 把有效会话也踢了"。所以判据必须**自己**建映射，
//    并**从设备侧**验证归属（用 curl 拿真 token 打 /api/tasks 必须是 200）。
//
// ══ 执行模型：一次 App 启动 = 一个状态 ═══════════════════════════════
// 更早一版把两个分支放在同一个 App 进程内交替改 localStorage 再 reload，
// 结果分支串台：有效 token 支没发请求就跳登录，伪造 token 支却拿到 /api/tasks=200。
// `location.reload()` 之后 CDP 执行上下文可能已重建 ⇒ 写入的上下文和读取的不是同一个。
// 本版：阶段 1 写状态后**立刻 force-stop**，阶段 2 冷启动，被测量的进程
// 生下来就带着目标状态。
//
// ══ 反空洞检查（缺一不可）═══════════════════════════════════════════
//   · 写入当场必须读回自证
//   · 必须真的发过 /api/tasks（否则"没有任务"只是"没有请求"）
//   · 作废后旧 token 必须真的 401（否则"没跳登录"什么都不能说明）
//   · 冷启动后 App 持有的 token 必须仍被后端接受（App 会续期，不能比字符串）
//
// 用法：
//   $env:POCKET_AUTH_PASS='<本地 dev 口令>'
//   $env:POCKET_API_BASE='http://127.0.0.1:18100'   # 必须是**你有权重启**的后端
//   node scripts/verify-bug-ax-401-on-device.mjs
import { execFileSync, spawnSync } from 'node:child_process'
import http from 'node:http'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9271'
const API = process.env.POCKET_API_BASE || 'http://127.0.0.1:18099'
const PASS = process.env.POCKET_DEV_PASS || process.env.POCKET_AUTH_PASS || ''
const BACKEND_PORT = new URL(API).port || '80'
const SECRET_A = 'pocket-local-dev-jwt-secret-do-not-use-in-shared-env'
const SECRET_B = 'pocket-local-dev-jwt-secret-ROTATED-by-bug-ax-judge'
const FIXTURE_TITLE = `BUGAX夹具-${Date.now().toString(36)}`

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
// 硬规矩：任何 await 都要有上限，否则 CDP 半死会把脚本挂住，表现为"没反应"而不是"失败"。
const withTimeout = (p, ms, what) =>
  Promise.race([p, sleep(ms).then(() => { throw new Error(`${what} 超时 ${ms}ms`) })])

const adb = (a, t = 60000, quiet = false) =>
  execFileSync(ADB, ['-s', SERIAL, ...a], {
    encoding: 'utf8', timeout: t, maxBuffer: 33554432,
    // quiet 只用于「本来就允许失败」的调用：pidof 无匹配、forward --remove 无既有映射。
    // 它们会往 stderr 写错误，经 PowerShell 2>&1 会变成 NativeCommandError 噪声。
    stdio: ['ignore', 'pipe', quiet ? 'ignore' : 'pipe'],
  })
// pidof / grep 在「不匹配」时返回非零退出码 + 空 stdout，execFileSync 直接抛 ——
// 于是"App 没跑"被报成"脚本崩了"。启动路径上必须容忍非零。
const adbSoft = (a, t = 60000) => { try { return adb(a, t, true) } catch { return '' } }

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

// ⚠️ 一律用**单引号**拼装：URL 和 header 值里都不含单引号，
// 所以不需要转义。把 -H 塞进 URL 字符串再套双引号会截断命令，
// 而截断是静默的（curl 000），最容易被读成"设备连不上后端"。
const deviceCode = (path, token) => {
  const cmd = token
    ? `curl -s -m 8 -o /dev/null -w '%{http_code}' -H 'Authorization: Bearer ${token}' 'http://localhost:${DEVICE_PORT}${path}'`
    : `curl -s -m 8 -o /dev/null -w '%{http_code}' 'http://localhost:${DEVICE_PORT}${path}'`
  return adbSoft(['shell', cmd]).trim()
}

// ── 设备→后端 归属：自己建映射，并从设备侧验 ───────────────────────────
// 见文件头第 3 条。这不是锦上添花：不这么做的话，本判据会在并发会话把映射
// 抢回去之后，对着**别人的后端**跑出一整轮"App 把有效会话也踢了"的假结论。
let DEVICE_PORT = '18099'
async function ensureDevicePath(phase) {
  adbSoft(['reverse', '--remove', `tcp:${DEVICE_PORT}`])
  const r = adbSoft(['reverse', `tcp:${DEVICE_PORT}`, `tcp:${BACKEND_PORT}`])
  const list = adbSoft(['reverse', '--list'])
  if (!list.includes(`tcp:${DEVICE_PORT} tcp:${BACKEND_PORT}`)) {
    throw new Error(`REVERSE_FAILED（${phase}）：adb reverse 建映射失败，list=\n${list}\n` +
      `  上一条 adb 输出：${r.slice(0, 200)}`)
  }
  // 从设备自己的网络栈验归属。⚠️ 这里必须**现签一枚新 token**，不能用 real.token：
  // 换 JWT secret 之后 real.token 正是被作废的那枚，拿它探路必然 401，
  // 会被读成"设备没走到我的后端"——而真实情况是设备走得好好的，
  // 是那枚 token 该死。2026-10-03 实测在这个假警报上栽了一轮。
  // 现签的 token 对当前后端必然有效，于是这个检查只回答一件事：
  // 设备现在能不能走到我手上这个后端。
  const fresh = await api('/api/auth/login', { method: 'POST', body: { username: 'admin', password: PASS } })
  let freshTok = ''
  try { freshTok = JSON.parse(fresh.body).token || '' } catch { freshTok = '' }
  if (!freshTok) {
    throw new Error(`ATTRIBUTION_TOKEN_FAILED（${phase}）：宿主侧都签不出新 token（status=${fresh.status}），后端本身有问题。`)
  }
  const code = deviceCode('/api/tasks', freshTok)
  if (code !== '200') {
    throw new Error(`DEVICE_PATH_MISMATCH（${phase}）：设备 curl localhost:${DEVICE_PORT}/api/tasks ` +
      `（带**现签**的 token，宿主侧刚拿到）= ${code || '(无响应)'}，期望 200。\n` +
      '  映射虽然建了，但设备没走到这个后端——常见于并发会话在同一台设备上抢占端口。\n' +
      '  此时**不要**解读后续任何 App 行为。')
  }
  console.log(`  [${phase}] 设备→:18100 归属已确认（设备侧带真 token 访问 = 200）`)
}

// ── 后端重启（换 JWT secret）─────────────────────────────────────────
// ⚠️ stdio 必须是 'ignore'：ps1 内部用 Start-Process 拉起 pocketd，那个孙进程
// 会继承 stdout/stderr 句柄；若用默认的 'pipe'，spawnSync 会一直等这些管道关闭 ——
// 表现是「后端明明起来了，脚本却卡住不动」，实测卡了 280s。（与 maestro-run.mjs 同一个坑。）
async function restartBackend(secret) {
  const ps = resolve(ROOT, 'scripts', 'start-local-backend.ps1')
  const r = spawnSync('powershell',
    ['-ExecutionPolicy', 'Bypass', '-File', ps, '-Port', String(BACKEND_PORT), '-JwtSecret', secret],
    { cwd: ROOT, encoding: 'utf8', timeout: 180000, stdio: 'ignore' })
  if (r.status !== 0) throw new Error(`后端重启失败 exit=${r.status}（需要环境里有 POCKET_AUTH_PASS）`)
  for (let i = 0; i < 60; i++) {
    await sleep(500)
    try { if ((await api('/healthz')).status === 200) return } catch { /* 还没起来 */ }
  }
  throw new Error('后端重启后 /healthz 60 次轮询仍不通')
}

// ── App 生命周期 ─────────────────────────────────────────────────────
// force-stop 之后 MIUI 有概率吞掉启动意图，所以要重试。
async function launchApp(label) {
  adbSoft(['shell', 'am', 'force-stop', PKG])
  await sleep(1500)
  let pid = ''
  for (let attempt = 1; attempt <= 3 && !pid; attempt++) {
    adbSoft(['shell', 'input', 'keyevent', '224'], 15000)   // 设备可能已熄屏
    adbSoft(['shell', 'monkey', '-p', PKG, '-c', 'android.intent.category.LAUNCHER', '1'], 30000)
    for (let i = 0; i < 20; i++) {
      await sleep(1500)
      pid = adbSoft(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
      if (pid) break
    }
    if (!pid) console.log(`  (${label}) 第 ${attempt} 次启动没拿到 pid，重试`)
  }
  if (!pid) throw new Error(`APP_NOT_RUNNING：${label} 连续 3 次拉起仍无进程`)
  return pid
}

// ── CDP 会话 ─────────────────────────────────────────────────────────
// 每次 App 重启后必须重开：老会话的 WebSocket 指向已死的目标。
async function openSession(pid) {
  const socks = adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
    .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
  if (!socks.length) throw new Error('NO_DEVTOOLS_SOCKET：App 起了但 WebView 还没初始化')
  const sock = socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]
  adbSoft(['forward', '--remove', `tcp:${PORT}`])
  adb(['forward', `tcp:${PORT}`, `localabstract:${sock}`])

  const list = await withTimeout(fetch(`http://127.0.0.1:${PORT}/json/list`).then((r) => r.json()), 20000, 'CDP /json/list')
  const page = list.find((t) => t.type === 'page')
  if (!page) throw new Error('NO_PAGE：CDP 里没有 page 目标')

  const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
  let id = 0
  const pending = new Map()
  const netLog = []
  ws.addEventListener('message', (e) => {
    const m = JSON.parse(e.data)
    if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id); return }
    // 记**全量** /api/*，不只 /api/tasks。
    // 2026-10-03 实测踩到：401 可能由 /api/auth/refresh 先触发（http.ts 早就有兜底），
    // 而不是 /api/tasks（client.ts:53 才是 BUG-AX 的修复点）。只记 /api/tasks 时，
    // 这两种情况在输出里长得一模一样，只能靠猜——而猜错就会把
    // 「refresh 兜底生效」误报成「BUG-AX 修复生效」。
    if (m.method === 'Network.responseReceived' && /\/api\//.test(m.params.response.url)) {
      netLog.push({
        status: m.params.response.status,
        path: m.params.response.url.replace(/^https?:\/\/[^/]+/, '').replace(/^http:\/\/localhost:\d+/, ''),
        isTasks: /\/api\/tasks(\?|$)/.test(m.params.response.url),
      })
    }
  })
  await withTimeout(new Promise((r) => ws.addEventListener('open', r)), 20000, 'WebSocket open')
  const send = (method, params = {}) => withTimeout(
    new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })) }),
    20000, `CDP ${method}`,
  )
  await send('Runtime.enable')
  await send('Network.enable')

  // ── 判据自检 ──────────────────────────────────────────────────────
  // Runtime.enable 之后的**第一次** Runtime.evaluate 返回 undefined（页内执行
  // 上下文还没就绪）。后果是 localStorage 读回来 undefined，`|| ''` 变成"空字符串"，
  // 于是打印成「token 已被清空」——一个看起来完全正常的假结论。
  for (let i = 0; i < 8; i++) {
    const v = (await send('Runtime.evaluate', { expression: '1+1', returnByValue: true }))?.result?.result?.value
    if (v === 2) break
    await sleep(800)
    if (i === 7) throw new Error('JUDGE_UNAVAILABLE：连续多次 `1+1` 都拿不到 2，CDP 执行上下文没就绪。' +
      '在这种状态下读 localStorage 会得到 undefined，不能据此判断 token 有没有被清。')
  }

  // 严格求值：读失败必须响亮地炸掉，绝不允许静默变成"空值"。
  const evStrict = async (expr, what) => {
    const run = () => send('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true })
    let r
    try { r = await run() } catch {
      // 导航之后渲染进程偶发卡顿 >20s。一次超时不等于页面死了，重试一次。
      console.log(`  (读取「${what}」超时，重试一次)`)
      await sleep(3000)
      r = await run()
    }
    if (r?.result?.exceptionDetails) {
      const d = r.result.exceptionDetails
      // ⚠️ d.text 恒为 "Uncaught"。真正的信息在 exception.description
      // （例如 "SyntaxError: Illegal return statement"）。只读 d.text 会把一眼可查的
      // 语法错误报成"未知错误"——2026-10-03 就这样白卡了一轮。
      throw new Error(`页内求值抛错（${what}）：${d.exception?.description || d.text || '(无详情)'} @line${d.lineNumber} col${d.columnNumber}`)
    }
    const v = r?.result?.result?.value
    if (v === undefined) throw new Error(`READ_FAILED：${what} 读回 undefined（读取通道坏了，不是值为空）`)
    return v
  }

  return { netLog, pid, evStrict, close: () => { try { ws.close() } catch { /* 已死 */ } } }
}

// ── 后端侧准备 ───────────────────────────────────────────────────────
const login = await api('/api/auth/login', { method: 'POST', body: { username: 'admin', password: PASS } })
let real = null
try { real = JSON.parse(login.body) } catch { real = null }
if (login.status !== 200 || !real?.token) {
  console.error(`❌ 后端登录失败 status=${login.status} body=${String(login.body).slice(0, 160)}`)
  console.error('   该端口上可能跑着别的 worktree / 别的会话的后端，它的 POCKET_AUTH_PASS 与你手里这个不同。')
  console.error('   查归属：Get-NetTCPConnection -State Listen -LocalPort <port> | %{ (Get-Process -Id $_.OwningProcess).ProcessName }')
  process.exit(1)
}
console.log(`后端 :${BACKEND_PORT} 登录 ok (user=${real.user || '?'}, ws=${real.workspace_id || '?'}, token=${real.token.length} 字符)`)
const base = await api('/api/tasks', { token: real.token })
console.log(`/api/tasks 基线：status=${base.status} body 长度=${base.body.length}`)

// 夹具：造一条 status=active 的任务，让「有没有任务」真的有区分力。
const created = await api('/api/tasks', {
  method: 'POST', token: real.token,
  // 载荷形态是实测出来的，不是猜的：多传 `type:'note'` 会 400；
  // `source:'acc'` 是只读来源，POST 会 403。最小可用形态就是 title + status。
  body: { title: FIXTURE_TITLE, status: 'active' },
})
let fixture = null
try { fixture = JSON.parse(created.body) } catch { fixture = null }
// POST /api/tasks 返回 201 Created（实测）。写死 !==200 会把成功的创建报成失败。
if (!(created.status >= 200 && created.status < 300) || !fixture?.id) {
  console.log(`⚠️ 夹具任务没建成（status=${created.status}），本判据将退化为「只看路由与 token」，判别力下降`)
} else {
  console.log(`夹具任务已建：id=${fixture.id} status=${fixture.status} title=${FIXTURE_TITLE}`)
}

// ── 写入目标登录态，然后杀掉进程 ─────────────────────────────────────
async function plantSession() {
  const s1 = await openSession(await launchApp('写入状态'))
  // ⚠️ 必须先等 App 的启动逻辑跑完再写。App 启动时会做自己的鉴权初始化，
  // 读到空的 localStorage 就 clearLocal() 清掉 4 个键（auth.ts clearLocal）。
  // 2026-10-03 实测：这个清理发生在我们写入**之后**，刚写进去的 token 被抹掉，
  // 读回 0 字符，报 WRITE_VERIFY_FAILED。写入和 App 的启动清理是竞态，
  // 等它跑完再写才不会输。
  await sleep(8000)
  // 三个键必须成套写。2026-10-02 第一版只写 pocket_token、把 pocket_user 清掉、
  // 也不写 pocket_workspace_id，于是**有效 token 也被弹回登录页**，对照分支直接红。
  // 差点据此把"应用把有效会话也踢掉"当成产品缺陷报出去——那是判据欠定，不是产品行为。
  //
  // 必须用 IIFE 包裹：Runtime.evaluate 不是 eval()，**顶层 return 是语法错误**。
  // 写完读回自证；被 App 的启动清理抹掉就重来（最多 3 次，每次之间都要等 ——
  // 竞态不会因为重试而消失，只会被等掉）。
  let ok = false
  let seen = ''
  for (let attempt = 1; attempt <= 3 && !ok; attempt++) {
    if (attempt > 1) {
      console.log(`  (写入被 App 启动清理抹掉了，第 ${attempt} 次重试)`)
      await sleep(6000)
    }
    await s1.evStrict(`(function(){
      localStorage.removeItem('pocket_token');
      localStorage.removeItem('pocket_user');
      localStorage.removeItem('pocket_workspace_id');
      localStorage.setItem('pocket:lastRoute', '#/ai');
      localStorage.setItem('pocket_token', ${JSON.stringify(real.token)});
      localStorage.setItem('pocket_user', ${JSON.stringify(real.user || 'admin')});
      localStorage.setItem('pocket_workspace_id', ${JSON.stringify(real.workspace_id || 'ws_user-admin')});
      return 'written';
    })()`, '写 localStorage')
    // WebView 的 localStorage 是异步刷盘的，写完立刻读也可能读不到自己的写；
    // 所以读回失败先等一拍再判，不直接判失败。
    await sleep(1200)
    const w = JSON.parse(await s1.evStrict(`JSON.stringify({
      token: localStorage.getItem('pocket_token') || '',
    })`, '读回 localStorage'))
    seen = w.token.length
    ok = w.token === real.token
  }
  if (!ok) {
    s1.close()
    throw new Error(`WRITE_VERIFY_FAILED：连写 3 次都没留住（期望 ${real.token.length} 字符，最后一次读回 ${seen} 字符）。这不是产品行为，是判据没写进去。`)
  }
  console.log(`  写入已读回自证：token=${real.token.length} 字符（读回一致）`)
  s1.close()
  await sleep(2500)                                    // 给 WebView 刷盘
  adbSoft(['shell', 'am', 'force-stop', PKG])
  await sleep(1500)
  console.log('  阶段1：目标状态已落盘并随进程一起销毁')
}

// ── 冷启动 + 触发一次真实的 /api/tasks 拉取 ─────────────────────────
async function probeTasks(label) {
  const s = await openSession(await launchApp(label))
  console.log(`  阶段2：冷启动 pid=${s.pid}`)
  await sleep(9000)                                   // 等 store 初始化 + 首屏

  // 这个进程实际持有的 token。**不能**要求它与我们写进去的逐字节相等：
  // App 启动会 POST /api/auth/refresh 做滑动续期，2026-10-03 实测冷启动后
  // token 同为 291 字符但内容已变。真正的不变量是「它仍被后端接受」。
  const held = (await s.evStrict(`localStorage.getItem('pocket_token') || ''`, '读冷启动 token')) || ''
  const accept = held ? await api('/api/tasks', { token: held }) : { status: 0 }
  console.log(`  冷启动持有 token=${held.length} 字符，后端接受度 /api/tasks=${accept.status}`)
  console.log(`  冷启动落在 = ${await s.evStrict('location.hash', '读冷启动 hash')}`)

  // ⚠️ 这里**不能**清 netLog。数据是 TasksView 在冷启动挂载时取的（它走全局 store
  // 缓存，换路由不会重新拉），2026-10-03 实测清掉之后控制支就再也看不到
  // /api/tasks，尽管页面上夹具任务明明渲染着。启动期的请求正是要观测的对象。
  await sleep(4000)
  await s.evStrict(`location.hash = '#/more'`, '跳离')
  await sleep(3000)
  await s.evStrict(`location.hash = '#/ai'`, '导航到任务看板')
  await sleep(8000)

  // 读 DOM / token 必须在**同一个**会话里做完：中途 close 再重开等于凭空制造一次
  // 「App 恰好此刻重启」的窗口，读到的会是另一个进程的状态——分支串台的另一种形状。
  const h = await s.evStrict('location.hash', '读 hash')
  const txt = (await s.evStrict('document.body.innerText', '读 body')) || ''
  const tok = (await s.evStrict(`localStorage.getItem('pocket_token') || ''`, '读 token')) || ''
  const log = s.netLog
  s.close()
  // 只用 /api/tasks 的响应做判据；其余端点仅作**上下文**打印出来。
  const tasksLog = log.filter((l) => l.isTasks)
  return {
    h, txt, tok, log,
    statuses: tasksLog.map((l) => l.status),
    sawRequest: tasksLog.length > 0,
  }
}

function report(o) {
  const atLogin = /#\/login/.test(o.h || '')
  const seesFixture = o.txt.includes(FIXTURE_TITLE)
  console.log(`  /api/tasks 响应 = ${o.sawRequest ? o.statuses.join(' | ') : '(没发出 /api/tasks)'}`)
  console.log(`  本轮 /api/* 全量 = ${o.log.length ? o.log.map((l) => `${l.status} ${l.path}`).join(' | ') : '(无)'}`)
  console.log(`  hash        = ${o.h}`)
  console.log(`  在登录页     = ${atLogin}`)
  console.log(`  看见夹具任务 = ${seesFixture}${fixture ? '' : '（夹具未建成，本项不作判据）'}`)
  console.log(`  token 还在   = ${o.tok ? `是（${o.tok.length} 字符）` : '否（已被清）'}`)
  console.log(`  页面文本片段 = ${o.txt.replace(/\s+/g, ' ').slice(0, 90)}`)
  return { atLogin, seesFixture }
}

// ══ 归属校验（开跑前一次）═══════════════════════════════════════════
await ensureDevicePath('开局')

// ══ 分支 1：对照（有效 token，应用内应看见夹具任务）══════════════════
console.log('\n──────── 分支1 对照：有效 token ────────')
await plantSession()
const c = await probeTasks('对照测量')
const cr = report(c)
const cOk = c.sawRequest && c.statuses.includes(200) && !cr.atLogin && !!c.tok
  && (fixture ? cr.seesFixture : true)
if (!c.sawRequest) {
  console.log('  判定：❌ 判据不可用（anti-vacuity）—— /api/tasks 压根没发出，不构成任何证据')
} else {
  console.log(`  判定：${cOk
    ? '✅ 对照成立：有效 token 留在应用内、/api/tasks=200、token 完好' +
      (fixture ? '、夹具任务在页面上可见' : '') + '（说明判据能区分登录页与应用内，且能看见 200 与真实数据）'
    : '❌ 对照不成立 —— 判据连"应用内"都认不出来，或读不到 200，或看不见夹具任务'}`)
}

// ══ 分支 2：被测（真实会话被服务端作废）══════════════════════════════
let subjectOk = false
console.log('\n──────── 分支2 被测：会话被服务端作废（换 JWT secret）────────')
if (!cOk) {
  console.log('  跳过：对照支不成立，无法证明"同一个应用内会话在作废后发生了什么"。')
} else {
  const heldBefore = c.tok
  console.log('  换 JWT secret …')
  await restartBackend(SECRET_B)
  await ensureDevicePath('换 secret 后')

  // 反空洞：作废必须真的生效，且**从设备侧**生效。
  const hostDead = await api('/api/tasks', { token: heldBefore })
  const devDead = deviceCode('/api/tasks', heldBefore)
  console.log(`  换 secret 后同一个 token：宿主侧 /api/tasks=${hostDead.status}，设备侧=${devDead}`)
  if (hostDead.status !== 401 || devDead !== '401') {
    console.log('  判定：❌ 判据不可用（anti-vacuity）—— 会话并未真的被作废，"App 没跳登录"什么都不能说明')
  } else {
    // 作废之后必须**冷启动**而不是原地换路由：TasksView 走全局 store 缓存，
    // 原地换路由不会重新拉数据（2026-10-03 实测）。新进程 = 空 store，
    // 挂载时必然重新拉一次 /api/tasks。
    const s = await openSession(await launchApp('作废后冷启动'))
    await sleep(9000)
    await s.evStrict(`location.hash = '#/more'`, '跳离')
    await sleep(3000)
    await s.evStrict(`location.hash = '#/ai'`, '导航到任务看板')
    await sleep(8000)
    const h = await s.evStrict('location.hash', '读 hash')
    const txt = (await s.evStrict('document.body.innerText', '读 body')) || ''
    const tok = (await s.evStrict(`localStorage.getItem('pocket_token') || ''`, '读 token')) || ''
    const log = s.netLog
    s.close()

    const tasksLog = log.filter((l) => l.isTasks)
    const o = {
      h, txt, tok, log,
      statuses: tasksLog.map((l) => l.status),
      sawRequest: tasksLog.length > 0,
    }
    const r = report(o)
    if (!o.sawRequest) {
      console.log('  判定：❌ 判据不可用（anti-vacuity）—— /api/tasks 没发出，"没看见任务"只是"没请求"')
    } else if (!o.statuses.includes(401)) {
      console.log('  判定：❌ 被测不成立 —— 会话已作废，但 /api/tasks 没拿到 401')
    } else {
      // 关键判别式：401 之后**不得**再渲染出那条夹具任务。
      // 修复前的样子是：401 被 catch 成空数组 → 页面显示空列表、不跳登录。
      subjectOk = r.atLogin && !tok && (fixture ? !r.seesFixture : true)
      console.log(`  判定：${subjectOk
        ? '✅ 401 走的是 forceReauth（跳登录页 + 清 token' + (fixture ? ' + 不再渲染夹具任务' : '') + '），没有被吞成空列表'
        : '❌ 不满足「/api/tasks=401 且 跳登录页 且 token 被清' + (fixture ? ' 且 夹具任务不再渲染' : '') + '」'}`)
    }
  }
  console.log('  恢复原 JWT secret …')
  await restartBackend(SECRET_A)
  await ensureDevicePath('恢复后')
}

const pass = cOk && subjectOk
console.log(`\n结论：${pass
  ? '✅ BUG-AX 真机回归通过（对照绿 + 被测绿）'
  : `❌ 未通过 —— control=${cOk} subject=${subjectOk}`}`)
process.exit(pass ? 0 : 1)
