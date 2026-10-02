// Maestro 启动器：在**进程内**从后端源码取 dev 旁路口令，注入环境变量后再拉起 maestro。
//
// 为什么要有这个壳：Maestro flow 里不能出现明文口令，但登录又必须真实走一遍。
// 这里从 Go 源码读常量 -> 只放进子进程 env -> flow 用 ${POCKET_DEV_PASS} 引用。
// 口令全程不出现在：仓库文件、命令行参数、stdout、本对话记录。
//
// 用法：
//   node scripts/maestro-run.mjs <flow.yaml> [更多 flow.yaml ...]
//   node scripts/maestro-run.mjs .maestro/notes-crud.yaml
import { readFileSync } from 'node:fs'
import { spawnSync, execFileSync } from 'node:child_process'
import { resolve } from 'node:path'

const ROOT = resolve(new URL('..', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1'))
const GO = resolve(ROOT, 'backend/internal/server/server_assistant.go')
const MAESTRO = 'C:/workspace/openpocket/logs/maestro/maestro/bin/maestro.bat'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const PKG = 'com.kaixuan.opencode.pocket'
const DRIVER_PKGS = ['dev.mobile.maestro', 'dev.mobile.maestro.test']
// 与 maestro-client.jar 内嵌的是同一份（SHA256 A7F12BBD…1F0B9），用 jar 里解出来的那份。
// 两个包都要装：Maestro 的 installMaestroApks 依次装 maestro-app 与 maestro-server，
// 只装前一个会在 installMaestroServerApp 一步炸掉（实测）。
const DRIVER_APKS = (process.env.POCKET_MAESTRO_DRIVER_DIR
  || 'C:/workspace/openpocket/logs/maestro/driver-extracted')
const DEVICE = process.env.POCKET_SERIAL || '192.168.31.19:5555'

const flows = process.argv.slice(2)
if (!flows.length) {
  console.error('用法: node scripts/maestro-run.mjs <flow.yaml> [...]')
  process.exit(2)
}

// ---- 确定性前置 ----
// 为什么必须自己做：Maestro 的 launchApp 默认先 am force-stop，而在 MIUI 真机上
// 实测 force-stop 成功（ActivityManager 打了 Killing）但之后**没有任何 Start proc**，
// App 再没起来，45s 内界面停在桌面。改 stopApp:false 能起来，但 App 会保留
// pocket:lastRoute 指向的任意页面（实测撞到过邮件详情），起始状态不可预测。
// 所以这里统一走 adb：强停 + monkey 启动（实测这条路径在 MIUI 上可靠），
// 让每次 run 的起始状态一致。App 也没有注册 deep link，没法用 intent 定位路由。
const adb = (args, t = 60000) =>
  execFileSync(ADB, ['-s', DEVICE, ...args], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

/** 一次性 CDP 求值：连上当前 App 的 WebView，评估一个表达式，拿回值后断开。 */
async function cdpEval(expr, ms = 8000) {
  const pid = adb(['shell', 'pidof', PKG], 15000).trim().split(/\s+/)[0]
  if (!pid) throw new Error('APP_NOT_RUNNING')
  const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`], 15000)
    .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
  const sock = socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]
  if (!sock) throw new Error('NO_DEVTOOLS_SOCKET')
  const port = 9500 + Math.floor(Math.random() * 300)
  adb(['forward', `tcp:${port}`, `localabstract:${sock}`], 15000)
  try {
    const page = (await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()).find((t) => t.type === 'page')
    if (!page) throw new Error('NO_PAGE_TARGET')
    const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${port}/`))
    let id = 0
    const pending = new Map()
    ws.addEventListener('message', (e) => {
      const m = JSON.parse(e.data)
      if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
    })
    await new Promise((r) => ws.addEventListener('open', r))
    const v = await new Promise((r) => {
      const i = ++id
      const t = setTimeout(() => { pending.delete(i); r(null) }, ms)
      pending.set(i, (x) => { clearTimeout(t); r(x) })
      ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: expr, returnByValue: true, awaitPromise: true } }))
    })
    ws.close()
    return v?.result?.value
  } finally {
    try { adb(['forward', '--remove', `tcp:${port}`], 15000) } catch { /* 已经没了 */ }
  }
}

/** 用 CDP 把 App 复位到指定路由，并等 App 外壳真的渲染出来。
 *  只等 hash 匹配是不够的——hash 变了不代表 DOM 渲染完了，
 *  实测会在 flow 第一条断言就失败（连「打开菜单」都还不在视图树里）。 */
async function setRoute(hash, readyExpr, timeoutMs = 30000) {
  const pid = adb(['shell', 'pidof', PKG], 15000).trim().split(/\s+/)[0]
  if (!pid) return false
  const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`], 15000)
    .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
  const sock = socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]
  if (!sock) return false
  const port = 9500 + Math.floor(Math.random() * 300)
  try {
    adb(['forward', `tcp:${port}`, `localabstract:${sock}`], 15000)
    const page = (await (await fetch(`http://127.0.0.1:${port}/json/list`)).json()).find((t) => t.type === 'page')
    if (!page) return false
    const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${port}/`))
    let id = 0
    const pending = new Map()
    ws.addEventListener('message', (e) => {
      const m = JSON.parse(e.data)
      if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
    })
    await new Promise((r) => ws.addEventListener('open', r))
    const ev = (x, ms = 8000) => new Promise((r) => {
      const i = ++id
      const t = setTimeout(() => { pending.delete(i); r(null) }, ms)
      pending.set(i, (v) => { clearTimeout(t); r(v) })
      ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
    })
    await ev(`location.hash=${JSON.stringify(hash)}`)
    const deadline = Date.now() + timeoutMs
    while (Date.now() < deadline) {
      await sleep(800)
      const gotHash = (await ev('location.hash'))?.result?.value
      const gotReady = readyExpr ? (await ev(readyExpr))?.result?.value === true : true
      if (gotHash === hash && gotReady) { ws.close(); return true }
    }
    ws.close()
    return false
  } catch {
    return false
  } finally {
    try { adb(['forward', '--remove', `tcp:${port}`], 15000) } catch { /* 已经没了 */ }
  }
}

async function ensureDriver() {
  // Maestro 在判定 driver 不可用时会**先卸载再安装**，而 MIUI 会拦下那一步安装，
  // 结果 driver 被卸掉且装不回来，之后每次 run 都在这卡死（实测连踩两次）。
  // 所以这里前置自愈。两条踩坑：
  //  1) MIUI 装完新包常把它置为 enabled=0，Maestro 认为不可用而反复重装 -> 必须 pm enable
  //  2) INSTALL_FAILED_USER_RESTRICTED 在这台机器上是**间歇性**的：
  //     logcat 显示 com.miui.permcenter.install.AdbInstallActivity 弹确认框后被自动取消，
  //     但下一次重试同样的命令就直接 Success 了（确认框没出现，tap 次数 = 0）。
  //     所以这里用「重试 + 必要时自动点确认框」，但**不能声称点框是必需的**——
  //     至少有一次成功路径上确认框压根没出现。
  const have = (p) => adb(['shell', 'pm', 'list', 'packages', p], 30000).includes(`package:${p}`)
  const enabled = (p) => {
    const info = adb(['shell', 'dumpsys', 'package', p], 30000)
    const line = info.split(/\r?\n/).find((l) => l.trim().startsWith('User 0:')) ?? ''
    return /enabled=1/.test(line)
  }

  for (const pkg of DRIVER_PKGS) {
    if (have(pkg) && enabled(pkg)) { console.log(`[driver] ${pkg} 已就位`); continue }
    if (have(pkg)) {
      // pm enable 在包其实已经不在时会抛 Unknown package（上一轮 Maestro 刚把它卸了），
      // 这里不能让它中断整个自愈流程，回落到安装即可。
      try { adb(['shell', 'pm', 'enable', pkg], 30000) } catch { /* 包已消失，走安装 */ }
      if (have(pkg) && enabled(pkg)) {
        console.log(`[driver] ${pkg} 此前被 MIUI 禁用，已 pm enable`)
        continue
      }
    }
    const apk = `${DRIVER_APKS}/${pkg === 'dev.mobile.maestro' ? 'maestro-app' : 'maestro-server'}.apk`
    let ok = false
    for (let attempt = 1; attempt <= 2 && !ok; attempt++) {
      console.log(`[driver] 安装 ${pkg}（第 ${attempt} 次）`)
      // 走 device-install-preflight.mjs 而不是旧的 adb-install-confirm.mjs：
      // 2026-10-02 实测，MIUI 只对**全新安装**弹确认框（`-r` 覆盖已装包不弹），
      // 而 Maestro 每次开会话都先 uninstall 再 install —— 撞的正是会弹那条路径。
      // preflight 会盯窗口焦点、定位「继续安装」并点击；已用对照实验钉死因果：
      // 点了就 Success（pm list 确认包真的在），不点即 USER_RESTRICTED。
      const r = spawnSync(process.execPath, [resolve(ROOT, 'scripts/device-install-preflight.mjs'), apk],
        { cwd: ROOT, encoding: 'utf8', timeout: 180000 })
      if (r.stdout) process.stdout.write(r.stdout)
      if (r.stderr) process.stderr.write(r.stderr)
      if (have(pkg)) { ok = true; adb(['shell', 'pm', 'enable', pkg], 30000); break }
      console.log(`[driver] ${pkg} 第 ${attempt} 次安装未成功`)
    }
    if (!ok) { console.error(`[driver] ${pkg} 装不上，MIUI 仍在拦截`); return false }
    try { adb(['shell', 'pm', 'enable', pkg], 30000) } catch { /* 装上就是 enabled */ }
    console.log(`[driver] ${pkg} 就位`)
  }
  return true
}

/**
 * 守卫：window.fetch 必须是**原生**实现。
 *
 * 为什么要有这个守卫（2026-10-01 踩了，花了小半小时才定位）：
 * 有人/有探针在运行时把 `window.fetch` 换成了一个包装器——它把请求记进
 * `window.__reqLog`，但**没有把底层响应 return 出去**，于是 `await fetch(...)`
 * 一律拿到 `undefined`，App 里每个 http() 调用都在读 `res.ok` 时炸成
 * "Cannot read properties of undefined (reading 'ok')"。
 *
 * 表现极具误导性：看起来像「这个功能的写路径坏了」（当时是闪卡建卡组失败），
 * 实际是**全 App 的网络都断了**。而且那个包装器既不在仓库里、也不在构建产物里
 * （`git grep __reqLog` 与 dist/android assets 均为空），是运行时注入的残留。
 *
 * 判据用 `String(fetch)` 是否含 `[native code]`：原生 fetch 一定含。
 * 命中包装器就直接中止，别浪费一轮 run 去查一个不存在的 bug。
 */
async function assertFetchIntact() {
  try {
    const info = await cdpEval(`JSON.stringify({
      native: String(window.fetch).includes('[native code]'),
      name: (window.fetch && window.fetch.name) || '',
      head: String(window.fetch).slice(0, 60),
    })`)
    const o = JSON.parse(String(info))
    if (o.native) { console.log('[preflight] fetch 为原生实现 ✅'); return true }
    console.error('[preflight] ❌ window.fetch 被运行时替换了，不是原生实现！')
    console.error(`           name="${o.name}"  head=${o.head}`)
    console.error('           这会让 App 所有 http() 调用拿到 undefined，看起来像功能坏了，其实是环境污染。')
    console.error('           处置：重启 App 进程（force-stop 后重新启动）即可恢复。')
    return false
  } catch (e) {
    console.log(`[preflight] fetch 守卫未能判定（${e?.message || e}），不阻断`)
    return true
  }
}

/**
 * 守卫：App 配的后端必须真的在监听。
 *
 * 为什么要有这个守卫（2026-10-01 12:45 实测，代价是一整轮误判）：
 *   App 的 API 基址是 http://127.0.0.1:18099。那次这个实例**没了**，
 *   而 18111 上跑着另一个 worktree 的 pocketd。结果：
 *     · PostgreSQL 里 17 条任务一条不少
 *     · App 任务页显示「运行中 0 / 全部正常」，**且没有任何错误提示**
 *   这和「列表功能坏了 / BUG-AL 复发」在现象上几乎无法区分，
 *   差点把环境问题当成产品缺陷去查。判据不硬就必然误判。
 *   ⇒ 每次 run 前探一次 /healthz；不通就明确报出来并给启动命令，
 *     不要让 flow 去跑一个注定失败的场景。
 */
async function assertBackendUp() {
  const base = process.env.POCKET_API_BASE || 'http://127.0.0.1:18099'
  try {
    const r = await fetch(`${base}/healthz`, { signal: AbortSignal.timeout(6000) })
    if (r.ok) { console.log(`[preflight] 后端可达 ${base} ✅`); return true }
    console.error(`[preflight] ❌ ${base}/healthz 返回 ${r.status}`)
  } catch (e) {
    console.error(`[preflight] ❌ ${base} 连不上：${e?.message || e}`)
  }
  console.error('           App 的一切读取都会静默变成空列表，flow 会给出误导性的失败。')
  console.error('           先起后端：powershell -ExecutionPolicy Bypass -File scripts/start-local-backend.ps1')
  return false
}

/**
 * 守卫：从**设备侧**确认 App 真的能打到我们这个后端。
 *
 * 为什么宿主侧的健康检查不够（2026-10-01 12:57 实测，第二次误判）：
 *   宿主 18099 已经起来了、/healthz 也 200，看起来一切正常。可 App 依然
 *   显示「运行中 0 / 全部正常」且无任何错误。原因是 `adb reverse` 的映射
 *   还指着**别的端口**：
 *       host-33 tcp:18099 tcp:18111      ← 设备 18099 → 宿主 18111
 *   18111 上跑的是另一个 worktree（openpocket-wt-stt）的 pocketd，
 *   它连的是另一份状态。设备上 curl 18099 照样 200，所以**设备侧探测
 *   也不会报错**，只是内容不对。
 *   ⇒ 光看「通不通」分辨不出来，必须**比对映射目标端口**。
 *   这是那种「所有健康检查都绿、功能却是空的」陷阱，只能靠显式断言挡住。
 */
async function assertDeviceReachesBackend() {
  const base = process.env.POCKET_API_BASE || 'http://127.0.0.1:18099'
  const port = (base.match(/:(\d+)/) || [])[1]
  if (!port) { console.log('[preflight] 解析不出端口，跳过设备侧检查'); return true }
  let list = ''
  try { list = adb(['reverse', '--list'], 15000) } catch { /* 没配 reverse */ }
  const mapping = list.split(/\r?\n/).map((l) => l.trim()).find((l) => new RegExp(`tcp:${port}\\s`).test(l))
  const target = mapping ? (mapping.match(/tcp:\d+\s+tcp:(\d+)/) || [])[1] : null
  if (!mapping) {
    // 设备重连（adb kill-server / WiFi 抖动 / 换 USB 模式）会把 reverse 映射整个清掉。
    // 正确映射唯一（App 的 API 基址端口 → 宿主同一端口），直接补上。
    console.error(`[preflight] ⚠️ 设备上没有 tcp:${port} 的 adb reverse 映射，正在补建`)
    try {
      adb(['reverse', `tcp:${port}`, `tcp:${port}`], 15000)
      const after = adb(['reverse', '--list'], 15000)
        .split(/\r?\n/).map((l) => l.trim()).find((l) => new RegExp(`tcp:${port}\\s`).test(l))
      const now = after ? (after.match(/tcp:\d+\s+tcp:(\d+)/) || [])[1] : null
      if (now !== port) { console.error(`[preflight] ❌ 补建失败，当前映射：${after || '<无>'}`); return false }
      console.error(`[preflight] ✅ 已补建为 tcp:${port} → tcp:${port}`)
    } catch (e) {
      console.error(`[preflight] ❌ 补建失败：${e?.message || e}`)
      return false
    }
  } else if (target !== port) {
    // 这个映射在本次调试里被外力改回去过至少三次（另一条 worktree 的调试、
    // adb server 重连都会动它），每次都让人重新排查一轮。
    // 正确映射是唯一的（App 的 API 基址端口 → 宿主同一端口），所以直接自愈，
    // 但**必须大声打印**：它改变了设备状态，不能悄悄发生。
    console.error(`[preflight] ⚠️ 设备 tcp:${port} 被映射到了宿主 tcp:${target}，正在自愈`)
    console.error(`           App 读到的是别的实例（很可能是另一个 worktree）的数据，`)
    console.error(`           表现为「列表恒空但没有任何报错」，极易被误判成产品缺陷。`)
    try {
      adb(['reverse', '--remove', `tcp:${port}`], 15000)
      adb(['reverse', `tcp:${port}`, `tcp:${port}`], 15000)
      const after = adb(['reverse', '--list'], 15000)
        .split(/\r?\n/).map((l) => l.trim()).find((l) => new RegExp(`tcp:${port}\\s`).test(l))
      const now = (after || '').match(/tcp:\d+\s+tcp:(\d+)/)
      if (!now || now[1] !== port) {
        console.error(`[preflight] ❌ 自愈失败，当前映射：${after || '<无>'}`)
        return false
      }
      console.error(`[preflight] ✅ 已自愈为 tcp:${port} → tcp:${port}`)
    } catch (e) {
      console.error(`[preflight] ❌ 自愈失败：${e?.message || e}`)
      return false
    }
  }
  // 映射对了，再确认设备上真的能拿到 200（reverse 存在但宿主没监听也会失败）
  try {
    const out = adb(['shell', `curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:${port}/healthz`], 20000).trim()
    if (out !== '200') {
      console.error(`[preflight] ❌ 设备 curl 127.0.0.1:${port}/healthz 返回 "${out}"`)
      console.error(`           先起后端：powershell -ExecutionPolicy Bypass -File scripts/start-local-backend.ps1`)
      return false
    }
  } catch {
    console.log(`[preflight] 设备侧 curl 不可用，跳过（映射本身已核对为 tcp:${port} → tcp:${port}）`)
  }
  console.log(`[preflight] 设备可达后端 ${base}（reverse tcp:${port} → tcp:${target}）✅`)
  return true
}

/**
 * 可选：每次 run 前清掉 App 的登录态，逼它走一遍真实登录。
 *
 * 为什么需要（2026-10-01 13:15~13:35 实测）：
 *   App 把 token 存在 localStorage。后端每重启一次（尤其换 JWT secret），
 *   设备上那枚 token 就作废；而 App **不会自己重新登录**，于是：
 *     · 任务列表恒空（每个请求都 401）
 *     · 「创建」点下去没反应（请求根本没发出去，PG 里查不到新行）
 *   这两个现象看起来都像「tasks 写路径坏了」，真因是环境。
 *   ⇒ 默认清登录态：这样每次 run 的 token 一定是当前后端签的，
 *     顺带把登录路径也真实跑一遍（而不是跳过它）。
 *   设 POCKET_RESET_AUTH=0 可关闭。
 */
async function resetAppAuth() {
  if (process.env.POCKET_RESET_AUTH === '0') return true
  try {
    const info = await cdpEval(`(function(){
      try {
        var before = (localStorage.getItem('pocket_token')||'').length;
        ['pocket_token','pocket_user','pocket_workspace_id','pocket_auth_method'].forEach(function(k){localStorage.removeItem(k)});
        return JSON.stringify({removedTokenLen: before, after: (localStorage.getItem('pocket_token')||'').length});
      } catch (e) { return JSON.stringify({err: String(e && e.message || e)}) }
    })()`)
    const o = JSON.parse(String(info))
    if (o.err) { console.log(`[preflight] 清理登录态未生效（${o.err}），继续`); return true }
    console.log(`[preflight] 已清除 App 登录态（原 token ${o.removedTokenLen} 字符）→ 本次会真实走一遍登录`)
    return true
  } catch (e) {
    console.log(`[preflight] 清理登录态失败（${e?.message || e}），不阻断`)
    return true
  }
}

async function ensureBackend() {
  if (await assertBackendUp()) return true
  const base = process.env.POCKET_API_BASE || 'http://127.0.0.1:18099'
  // 只有本地后端才自动拉起；指向远端时拉起本机 pocketd 是错的。
  if (!/127\.0\.0\.1|localhost/.test(base)) return false
  if (process.env.POCKET_AUTOSTART_BACKEND === '0') return false
  console.error('[preflight] 尝试自动拉起本 worktree 的 pocketd …')
  const ps = resolve(ROOT, 'scripts', 'start-local-backend.ps1')
  // ⚠️ stdio 必须是 'ignore'：ps1 内部用 Start-Process 拉起 pocketd，
  //    那个孙进程会继承这里的 stdout/stderr 句柄。若用默认的 'pipe'，
  //    spawnSync 会一直等这些管道关闭 —— 表现是「后端明明起来了，
  //    preflight 却卡住不动」，实测卡了 3 分钟。踩过。
  const r = spawnSync('powershell', ['-ExecutionPolicy', 'Bypass', '-File', ps], {
    cwd: ROOT, encoding: 'utf8', timeout: 180000, stdio: 'ignore',
  })
  if (r.status !== 0) {
    console.error(`[preflight] ❌ 启动脚本失败（exit=${r.status}）：${(r.stderr || '').slice(0, 300)}`)
    return false
  }
  if (await assertBackendUp()) {
    console.error('[preflight] ✅ 后端已拉起')
    return true
  }
  console.error('[preflight] ❌ 拉起后 /healthz 仍不通')
  return false
}

async function preflight() {
  if (!(await ensureBackend())) return false
  if (!(await assertDeviceReachesBackend())) return false
  if (!(await ensureDriver())) return false
  console.log('[preflight] 强停并重新启动 App（绕开 MIUI 吞掉 force-stop 后启动意图的问题）')
  try { adb(['shell', 'am', 'force-stop', PKG]) } catch { /* 本来就没跑 */ }
  await sleep(1500)
  adb(['shell', 'monkey', '-p', PKG, '-c', 'android.intent.category.LAUNCHER', '1'], 30000)
  // 等 WebView 真正起来，而不是盲等固定秒数
  const deadline = Date.now() + 60000
  while (Date.now() < deadline) {
    await sleep(1500)
    const pid = adb(['shell', 'pidof', PKG]).trim()
    if (!pid) continue
    const resumed = adb(['shell', 'dumpsys', 'activity', 'activities'], 30000)
    // 注意别写成 topResumedActivity=\S*\s*<包名>：实际输出是
    //   topResumedActivity=ActivityRecord{6194969 u0 com.kaixuan.opencode.pocket/.MainActivity
    // 中间夹着 `u0`，\S* 跨不过空格，会**永远不匹配**——
    // 于是把「App 明明在前台」误报成「60s 未进前台」。踩过，别改回去。
    if (/topResumedActivity.*opencode\.pocket/.test(resumed)) {
      console.log(`[preflight] App 已在前台 pid=${pid.trim()}`)
      await assertFetchIntact()
      return true
    }
  }
  console.error('[preflight] App 60s 内未进入前台，中止')
  return false
}

// ── dev 口令来源 ──────────────────────────────────────────────────────
// 2026-10-02 修复（两侧独立发现同一个问题，这里合并两侧增量）：
//
// 本脚本原先用正则从 backend/internal/server/server_assistant.go 抠
// `devPass = "…"` 常量。那天的安全整改**有意删掉了那个硬编码口令** ——
// server_assistant.go 现在是 `devPass := s.cfg.DevAuthPass`，拿不到配置就
// 关闭 dev 旁路；config.go 的 DevAuthPass 读 POCKET_AUTH_PASS 且**无缺省值**；
// internal/repohygiene/secrets_test.go 会把硬编码口令判成违规。
//
// 于是本脚本每天都在第 2 步 exit(2)，整条真机 Maestro 链路不可用，而报错
// （"未能从后端源码定位 dev 口令常量"）指向的是一个**已经不存在的东西** ——
// 典型的「报错指向错误原因」。
//
// 现在的取值顺序（三者取第一个非空）：
//   1. POCKET_DEV_PASS   —— 本 harness 专用，与服务端变量不同名，
//                           避免"给 harness 设的值顺手把服务端也改了"
//   2. POCKET_AUTH_PASS  —— 服务端真正读的那个
//   3. 源码常量          —— **仅当老 checkout 还在用**时兜底（不新增任何
//                           硬编码口令，只是读一个已经存在的）
//
// 不设时不猜、不用明文兜底：报错里写明**两个值必须一致**。不一致的表现
// 极具误导性 —— 登录 401 → 任务列表空 → 看起来像"列表功能坏了"（见 BUG-AX）。
const src = readFileSync(GO, 'utf8')
const DEV_PASS = process.env.POCKET_DEV_PASS
  || process.env.POCKET_AUTH_PASS
  || (src.match(/devPass\s*=\s*"([^"]+)"/) || [])[1]
  || ''
if (!DEV_PASS) {
  console.error('[preflight] 未提供 dev 口令。')
  console.error('  本仓库已移除源码里的硬编码 devPass（internal/repohygiene 会判违规），')
  console.error('  口令必须显式提供，并且**同一个值**要同时给到两处：')
  console.error('    $env:POCKET_DEV_PASS="<口令>"      # 本 harness，给 Maestro flow 用')
  console.error('    $env:POCKET_AUTH_PASS="<口令>"     # 起 pocketd 时用')
  console.error('  两者不一致的表现极具误导性：登录 401 -> 任务列表空 -> 看起来像列表功能坏了。')

  process.exit(2)
}

if (!(await preflight())) process.exit(3)

// 复位到 #/ai。App 会记住 pocket:lastRoute 并在重启后恢复过去，
// 实测撞到过恢复到「邮件详情」和「笔记页」——起始状态不确定，
// flow 里所有「等某个页面元素出现」的断言就都可能不成立。
// 每次 run 都复位一次，后面所有 flow 才可以假定起点是 AI 工具页。
{
  const route = process.env.POCKET_START_ROUTE || '#/ai'
  // 顺带等 App 外壳真的渲染出来：只等 hash 匹配时，flow 第一条断言（打开菜单）
  // 仍可能失败——hash 变了但 DOM 还没画完。ready 判据用 aria-label，结构性、稳定。
  const ok = await setRoute(route, `!!document.querySelector('[aria-label="打开菜单"]')`)
  console.log(ok
    ? `[preflight] 已复位到 ${route} 且 App 外壳已渲染`
    : '[preflight] ⚠️ 复位路由/等渲染未成功，flow 的起始状态可能不确定')
}

// 复位完成后再清登录态：顺序不能反，否则清完 token 页面又会把旧壳渲染回来。
await resetAppAuth()

// --no-reinstall-driver 是这台机器上能不能跑通 Maestro 的关键：
// Maestro 2.11 **默认每次 test 之前都重装 driver**，而它的重装是「先卸载再安装」。
// MIUI 会拦下安装那一步，于是每跑一次就亲手把 driver 卸掉且装不回来，
// 下一轮继续卡在 installMaestroApks —— 破坏性循环（实测连踩三次，
// 分别卡在 installMaestroDriverApp / installMaestroServerApp）。
// 改成不重装，driver 由本脚本的 ensureDriver() 负责自愈。
// 每次 run 前面插一段 _dismiss-system-dialogs：MIUI 的一次性系统弹窗会在
// flow 中段抢前台（2026-10-01 12:29 实测：抢在 unlock 分支输主密码时，
// 把键盘输入吃掉，「解锁」恒 disabled），用 optional tap 清掉。
// 注入给子进程前自检：只打长度，不打明文。
// 2026-10-01 13:40 实测踩到过「Maestro 把 ${POCKET_DEV_PASS} 展开成字符串
// "undefined"」，现场只留下一条 assert `^undefined$` 不成立，根因看不见。
// 这行让「变量到底传没传过去」一眼可见（口令本身仍不落 stdout）。

console.log(`[preflight] 注入子进程：POCKET_MASTER=${(process.env.POCKET_MASTER || 'PocketTest2026').length} 字符 / POCKET_DEV_PASS=${DEV_PASS.length} 字符`)

const SYSTEM_DIALOG_FLOW = '.maestro/_dismiss-system-dialogs.yaml'
const args = ['--device', DEVICE, 'test', '--no-reinstall-driver', SYSTEM_DIALOG_FLOW, ...flows]
const r = spawnSync(MAESTRO, args, {
  cwd: ROOT,
  stdio: 'inherit',
  shell: true,
  env: {
    ...process.env,

    POCKET_DEV_PASS: DEV_PASS, // 只进子进程 env
    // 本地 SQLCipher 主密码是测试装置上本会话约定的值，不是仓库内推导出来的。
    // 仍然只经 env 传递，避免出现在 flow 文件里。
    POCKET_MASTER: process.env.POCKET_MASTER || 'PocketTest2026',
    JAVA_HOME: process.env.JAVA_HOME || 'C:\\Program Files\\Eclipse Adoptium\\jdk-21.0.12.101-hotspot',
    MAESTRO_CLI_NO_ANALYTICS: 'true',
  },
})
process.exit(r.status ?? 1)
