// Maestro 启动器：在**进程内**从后端源码取 dev 旁路口令，注入环境变量后再拉起 maestro。
//
// 为什么要有这个壳：Maestro flow 里不能出现明文口令，但登录又必须真实走一遍。
// 这里从 Go 源码读常量 -> 只放进子进程 env -> flow 用 ${POCKET_DEV_PASS} 引用。
// 口令全程不出现在：仓库文件、命令行参数、stdout、本对话记录。
//
// 用法：
//   node scripts/maestro-run.mjs <flow.yaml> [更多 flow.yaml ...]
//   node scripts/maestro-run.mjs .maestro/notes-crud.yaml
import { readFileSync, existsSync } from 'node:fs'
import { spawnSync, execFileSync } from 'node:child_process'
import { resolve } from 'node:path'
import { homedir } from 'node:os'

const ROOT = resolve(new URL('..', import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, '$1'))
const GO = resolve(ROOT, 'backend/internal/server/server_assistant.go')

// ---- 可执行文件解析：env 覆盖 -> PATH -> 常见安装位置 --------------------
//
// 为什么不能再写死：这三个常量原本是某台 Windows 开发机的绝对路径
// （C:/workspace/... / C:/Users/86133/... / C:\Program Files\Eclipse Adoptium\...）。
// 换到 Linux 宿主后它们全部指向不存在的文件，于是整套真机 rig 在第一步
// 就以「maestro: not found」/ JAVA_HOME 无效的形式死掉——**看起来像设备问题，
// 实际是 harness 自己绑死在一台机器上**。2026-10-03 实测。
//
// 解析顺序刻意是「显式 env 优先」，这样换机器/换 JDK 不用改仓库。
function whichFirst(candidates) {
  for (const c of candidates) {
    if (!c) continue
    if (c.includes('/') || c.includes('\\')) {
      if (existsSync(c)) return c
      continue
    }
    // 裸命令名：交给 PATH 查
    const r = spawnSync(process.platform === 'win32' ? 'where' : 'which', [c],
      { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] })
    const first = (r.stdout || '').split(/\r?\n/).map(s => s.trim()).filter(Boolean)[0]
    if (r.status === 0 && first) return first
  }
  return null
}

const IS_WIN = process.platform === 'win32'
const MAESTRO = process.env.POCKET_MAESTRO_BIN
  || whichFirst([
    IS_WIN ? 'C:/workspace/openpocket/logs/maestro/maestro/bin/maestro.bat' : null,
    `${homedir()}/.maestro/bin/maestro`,
    'maestro',
  ])
  || 'maestro'

const ADB = process.env.POCKET_ADB_BIN
  || whichFirst([
    IS_WIN ? 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe' : null,
    `${homedir()}/Android/Sdk/platform-tools/adb`,
    `${homedir()}/tools/android-sdk/platform-tools/adb`,
    'adb',
  ])
  || 'adb'

// JAVA_HOME：Maestro 是 JVM 应用，没有它连 `maestro --version` 都跑不起来。
// 同样不写死某台机器的 JDK 路径。
function resolveJavaHome() {
  if (process.env.JAVA_HOME && existsSync(process.env.JAVA_HOME)) return process.env.JAVA_HOME
  const home = homedir()
  const guesses = [
    `${home}/tools/jdk-21.0.12.1+1`,
    '/usr/lib/jvm/java-21-openjdk-amd64',
    '/usr/lib/jvm/default-java',
    'C:/Program Files/Eclipse Adoptium/jdk-21.0.12.101-hotspot',
  ]
  for (const g of guesses) {
    if (existsSync(resolve(g, 'bin', IS_WIN ? 'java.exe' : 'java'))) return g
  }
  return process.env.JAVA_HOME || null
}

// 被测 App 的 applicationId。默认主包；并存包（sttdev 等）用 POCKET_APP_ID 覆盖。
//
// 为什么必须可覆盖：PKG 决定了 force-stop / monkey 启动 / pidof / CDP target 匹配
// 四件事，是整套 rig 里唯一绑死 applicationId 的常量。sttdev 并存包的
// applicationId 是 com.kaixuan.opencode.pocket.sttdev，hardcode 时 preflight 会去
// stop/启动主包、pidof 拿到主包的 pid、CDP 那边也连错 target ——
// **flow 一行都没跑就已经在测错的 App**，而报错形态是「元素找不到」这种
// 看起来像产品缺陷的东西。2026-10-03 实测。
//
// 只接受形如 com.x.y 的合法包名：空串、空白、带空格的都会让 adb 参数变成两个
// token，报错位置离真正的原因很远。宁可在这里 exit 2。
const PKG = (() => {
  const raw = process.env.POCKET_APP_ID
  if (raw === undefined || raw === '') return 'com.kaixuan.opencode.pocket'
  const v = raw.trim()
  if (!/^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z0-9_]+)+$/.test(v)) {
    console.error(`[config] POCKET_APP_ID 不是合法的 applicationId: ${JSON.stringify(raw)}`)
    console.error('[config] 期望形如 com.kaixuan.opencode.pocket.sttdev；不传则用主包。')
    process.exit(2)
  }
  return v
})()
const DRIVER_PKGS = ['dev.mobile.maestro', 'dev.mobile.maestro.test']
// 与 maestro-client.jar 内嵌的是同一份（SHA256 A7F12BBD…1F0B9），用 jar 里解出来的那份。
// 两个包都要装：Maestro 的 installMaestroApks 依次装 maestro-app 与 maestro-server，
// 只装前一个会在 installMaestroServerApp 一步炸掉（实测）。
// ★ 2026-10-07 修跨平台：默认路径原先写死成
//   `C:/workspace/openpocket/logs/maestro/driver-extracted`——那是某台 Windows
//   机器上 extract-maestro-driver.mjs 的产物目录。
// 在 macOS / Linux 上这个目录永远不存在，于是 ensureDriver() 稳定报
//   `[FAIL] APK 不存在: C:/workspace/.../maestro-app.apk`
// 而这行日志与「MIUI 拦截安装」在**同一步、同样的非 0 退出**，
// 于是真因（路径不存在）被读成设备策略问题——实测为此白绕了好几轮。
//
// 现在默认指向 extract-maestro-driver.mjs 的输出（仓库内 logs/），
// 且那个脚本已跨平台化：它会自己在 ~/.maestro/lib 下找到 maestro-client.jar。
// 需要手工指定时仍然可以用 POCKET_MAESTRO_DRIVER_DIR 覆盖。
const DRIVER_APKS = (process.env.POCKET_MAESTRO_DRIVER_DIR
  || `${ROOT}/logs/maestro/driver-extracted`)
const DEVICE = process.env.POCKET_SERIAL || '192.168.31.19:5555'

const flows = process.argv.slice(2)
if (!flows.length) {
  console.error('用法: node scripts/maestro-run.mjs <flow.yaml> [...]')
  process.exit(2)
}

// ---- flow 声明的 appId 必须与本进程实际操作的包一致 ----
//
// 两个包各管一半、互不知情，是这类 harness 最容易出的**静默错测**：
//   · PKG 决定 force-stop / monkey / pidof / CDP target —— 它动的是谁；
//   · flow 里的 `appId:` 决定 Maestro 截图与操作谁 —— 它测的是谁。
// 两者不一致时，preflight 会去停主包、CDP 连上主包，而 Maestro 的断言全打在
// sttdev 上：轻则找不到元素，重则**对着一个刚被我 force-stop 的 App 取树**，
// 报错形态仍然只是「Element not found」，与真实的产品缺陷无法区分。
//
// 这里宁可响亮退出。允许的两种一致：声明 == PKG，或 flow 干脆不声明 appId
// （Maestro 默认取 config 里的，也未必对，所以一样要求显式一致）。
{
  const mismatched = []
  for (const f of flows) {
    const p = resolve(ROOT, f)
    if (!existsSync(p)) continue
    const m = readFileSync(p, 'utf8').match(/^appId:\s*(\S+)\s*$/m)
    if (!m) {
      mismatched.push(`${f}: 未声明 appId（期望 ${PKG}）`)
    } else if (m[1] !== PKG) {
      mismatched.push(`${f}: appId=${m[1]}`)
    }
  }
  if (mismatched.length) {
    console.error(`[config] 以下 flow 与本进程操作的包不一致（当前 ${PKG}）：`)
    for (const line of mismatched) console.error(`[config]   ${line}`)
    console.error('[config] 用 POCKET_APP_ID 指定与 flow 一致的包，或改 flow 的 appId。')
    process.exit(2)
  }
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

/**
 * 「查不到」是**有效答案**的那些命令必须走这个（真机 4c308e2e / MIUI 实测）。
 *
 * 为什么：execFileSync 在退出码非 0 时**抛异常**。而 `pidof <不存在的包>` 退 1、
 * `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'` 在无命中时也退 1 ——
 * 两者都是**正常**的「没找到」。用 adb() 读它们，代码永远走不到
 * `if (!pid) throw new Error('APP_NOT_RUNNING')` 那一支，
 * 而是直接以未捕获异常把整个跑批打死，崩点与真实原因隔了好几跳。
 */
const adbSoft = (args, t = 60000) => {
  try {
    return execFileSync(ADB, ['-s', DEVICE, ...args], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
  } catch (e) {
    return (e && e.stdout) || ''   // 「查不到」时 stdout 通常为空，退化成空串
  }
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

/**
 * 取「打开菜单」按钮在**当前设备 locale** 下的 aria-label，取不到返回 null。
 *
 * 为什么需要它：App 的 a11y 标签走 i18n（zh-CN「打开菜单」/ en-US「Open menu」），
 * 而 harness 原先在选择器里写死中文。en-US 设备上判据恒 false，
 * 报错却是「复位失败；App 当前实际停在 #/ai」——hash 其实完全正确，
 * **报错方向与真因相反**，会把人引去查路由守卫。
 *
 * 取值顺序：设备实际 locale → POCKET_LOCALE 环境变量 → 默认 zh-CN。
 * locale 字符串按 BCP47 取前两段（en-US / en_US / zh-Hans-CN 都能落到 en / zh）。
 * 两边都取不到时返回 null，调用方改用「App 外壳容器存在」这条弱判据，
 * 而不是继续拿一种语言去撞另一种语言的设备。
 */
function resolveOpenMenuLabel() {
  const LOCALES = `${ROOT}/frontend/src/locales`
  const wanted = []
  const explicit = process.env.POCKET_LOCALE
  if (explicit) wanted.push(explicit)
  // ⚠️ 2026-10-07 实测踩到：原先只读 `persist.sys.locale`，而**这台设备上它是空的**
  // （两路 adb 都返回 "\n"）。真正的值在 `ro.product.locale`（en-US）。
  // 于是 wanted 退化成只有 ['zh-CN'] 默认值 ⇒ 又拿中文去撞英文设备，
  // 判据恒 false —— 和没修之前**完全一样**，而日志里看不出差别。
  //
  // 所以这里逐个试已知的 locale 属性，取第一个非空的；
  // 顺序按「越稳定越靠前」，persist.* 可写但可能没写过，ro.* 一定有。
  for (const prop of ['persist.sys.locale', 'ro.product.locale', 'ro.sys.locale']) {
    const v = adbSoft(['shell', 'getprop', prop], 8000).trim()
    if (v) wanted.push(v)
  }
  wanted.push('zh-CN')
  for (const raw of wanted) {
    // 文件名是**完整 BCP47**（en-US.json / zh-CN.json / de-DE.json…），
    // ⚠️ 我第一版按语言码拼 `${lang}.json` → en.json，磁盘上根本没有这个文件，
    // 于是取值恒 null、判据退化成弱判据，而日志里看不出来。
    // 所以先试完整 tag，再退回语言码（cover 「设备只报了 en」这类情况）。
    const tag = String(raw).trim()
    const lang = tag.split(/[-_]/)[0].toLowerCase()
    for (const cand of [tag, lang]) {
      const file = `${LOCALES}/${cand}.json`
      if (!existsSync(file)) continue
      try {
        const doc = JSON.parse(readFileSync(file, 'utf8'))
        // 键路径以仓库真实结构为准：layout.openMenu
        // （zh-CN.json:31 "layout": { … "openMenu": "打开菜单" }）。
        // ⚠️ 我第一版写的是 `nav.openMenu` —— 那是我按目录名想当然编的，
        // 实测 node -e require(...) 打出 undefined。锚点必须从真实文件抄。
        const label = doc?.layout?.openMenu
        if (typeof label === 'string' && label) return label
      } catch { /* 坏 JSON 就换下一种写法 */ }
    }
  }
  return null
}

/** 一次性 CDP 求值：连上当前 App 的 WebView，评估一个表达式，拿回值后断开。 */
/**
 * 绑定一个 CDP 转发端口，返回 adb 实际分配到的端口号。
 *
 * 2026-10-02 真机实测的缺陷：原先这里是
 *   `const port = 9500 + Math.floor(Math.random() * 300)`
 * 然后直接 forward。撞上已被占用的端口就抛
 *   `cannot bind listener: cannot bind to 127.0.0.1:9528 ... (10048)`
 * 撞的是**上一轮没清干净的 forward**，或同机另一个会话的 forward。
 *
 * 后果分两种，差别很大：
 *   · 落在 assertFetchIntact 上 → 它 catch 后只打一句「未能判定，不阻断」，
 *     run 继续（2026-10-02 实测就是如此，run 仍然 exit=0）。
 *     也就是说**这个碰撞可以完全静默**：守卫没跑成，但没有人在乎，
 *     绿灯照出。
 *   · 落在 assertAppUsesReverseBase 或 CDP 登录块上 → preflight 直接崩，
 *     而报错「端口被占用」指向的是装置，看不出「真问题是上次没清干净」。
 *
 * 修法不是「多随机几次然后重试」——那只是把概率推低，没有取消它；
 * 本机同时有别的会话在驱设备，端口是**共享可变状态**。
 * 而是让 adb 自己挑空闲端口：`forward tcp:0` 会由 adb 分配一个当前空闲的
 * 端口并把它打印出来。2026-10-02 实测：分配到 55704，`forward --list`
 * 里确实出现该条目。碰撞因此从「概率事件」变成「不可能」。
 *
 * 仍要校验返回值：端口号必须是正整数，否则说明 adb 的行为变了或输出被改，
 * 静默地拿一个 NaN 去拼 URL 会得到一个更费解的报错。
 */
function bindCdpForward(sock) {
  const out = adb(['forward', 'tcp:0', `localabstract:${sock}`], 15000)
  const port = Number(String(out).trim())
  if (!Number.isInteger(port) || port <= 0) {
    throw new Error(`CDP_FORWARD_NO_PORT: adb forward tcp:0 没返回可用端口号，输出=${JSON.stringify(String(out))}`)
  }
  return port
}

async function cdpEval(expr, ms = 8000) {
  // pidof 可能返回**空**，而且不止一种原因：
  //   ① App 刚被 force-stop / reload 换进程，此刻窗口期里 pidof 是空的；
  //   ② App 真的没起来。
  // 两种都用同一个 APP_NOT_RUNNING 报出去，调用方无从区分——
  // 而①是可自愈的（本文件下方「等 pid」那段正是为此），②重试多少次都没用。
  //
  // 2026-10-07 实测：assertAppUsesReverseBase 里 `location.reload()` 之后
  // 紧接着的一次 cdpEval 稳定命中①，抛 APP_NOT_RUNNING，
  // 整个 preflight 崩掉，退出码 3。实测同一时刻 App 就在前台、
  // webview_devtools_remote_<pid> socket 也在，等一秒就好。
  //
  // 所以这里**有界重试 pidof**：最多 10 次、每次 500ms，总计 5s。
  // 5s 是有界而不是无限：App 真的没起来时，多等也等不来，
  // 而让整个 preflight 白等 5s 只为换一个同样的 APP_NOT_RUNNING 不划算。
  let pid = adbSoft(['shell', 'pidof', PKG], 15000).trim().split(/\s+/)[0]
  if (!pid) {
    for (let i = 0; i < 10; i++) {
      await sleep(500)
      pid = adbSoft(['shell', 'pidof', PKG], 15000).trim().split(/\s+/)[0]
      if (pid) break
    }
  }
  if (!pid) throw new Error('APP_NOT_RUNNING')
  // socket 是 App 起来**之后**才注册的，pidof 返回得比它早。
  // 所以这里等它出现（有界），而不是「等不到就退而求其次连别的」——
  // 后者正是连错 App 的根源，而连错之后**没有任何报错**。
  const listSocks = () => adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`], 15000)
    .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
  let socks = []
  for (let i = 0; i < 15; i++) {
    socks = listSocks()
    if (socks.some((s) => s.endsWith(`_${pid}`))) break
    if (i === 0 && socks.length && !socks.some((s) => s.endsWith(`_${pid}`))) {
      // 只提示一次，避免把「正常等 socket」刷成一片警告
      console.log(`[cdp] 等待 pid=${pid} 的 devtools socket（现有：${[...new Set(socks)].join(', ') || '无'}）…`)
    }
    await sleep(1000)
  }
  if (!socks.length) throw new Error('NO_DEVTOOLS_SOCKET')
  // ⚠️ 2026-10-04 修：原来这里是 `find(…) || socks[socks.length - 1]`。
  // 本机同时装着 com.kaixuan.opencode.pocket 与 …pocket.sttdev，两个
  // webview_devtools_remote_<pid> 都是**活的**，「取最后一个」会连到**另一个 App**，
  // 而 /json/list 与 Runtime.evaluate 一切正常、不报错。
  //
  // 这条通道比 lib/adb-cdp.mjs 那条更危险：它是 preflight 用来**填登录表单、
  // 填主密码、复位路由**的。一旦连错包，preflight 会把登录态、路由全写到另一个 App 上，
  // 然后对着另一个 App 的 hash 判「登录成功/失败」——结论与被测对象无关。
  const sock = socks.find((s) => s.endsWith(`_${pid}`))
  if (!sock) {
    const others = [...new Set(socks.filter((s) => !s.endsWith(`_${pid}`)))]
    throw new Error(
      `CDP_SOCKET_PID_MISMATCH：等 15s 仍没有 pid=${pid}（${PKG}）的 webview devtools socket。` +
      `设备上现有：${[...new Set(socks)].join(', ')}。` +
      (others.length
        ? `其中可能属于**其它 App**（本机装了多个包时常见），连过去会静默操作错误的 WebView。`
        : '看起来都是死进程残留。') +
      '不要退回连别的 socket。'
    )
  }
  const port = bindCdpForward(sock)   // 端口被占用会重试，见 bindCdpForward 的注释
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
 *  实测会在 flow 第一条断言就失败（连「打开菜单」都还不在视图树里）。
 *
 *  opts.settle = true 时，判据从「hash **精确等于**目标」放宽为
 *  「hash 连续两次采样不变」。为什么需要这一档：调用方
 *  「清完 token 后制造一次真实 hashchange」的目的是**让路由守卫自己决定**
 *  去哪，而守卫命中未登录时会把 #/ai 重定向到
 *  #/login?returnTo=/ai?__recheck=… —— 这个目标串**永远不等于**请求的串。
 *  于是精确匹配在结构上不可能成立：2026-10-03 实测该步每次都空转满
 *  timeout 并返回 false，紧接着的读 hash 拿到的是重定向**之前**的值，
 *  于是 preflight 报「已清登录态但 App 停在 #/ai，没有落到登录页」并 exit 3。
 *  手工复现同一步骤（清 token → 立刻设 #/ai?__recheck=…）是能正常落到
 *  #/login 的，所以坏的是判据，不是 App。
 *  其它调用方仍用精确匹配——那里「必须停在这个路由」就是真实要求。 */
async function setRoute(hash, readyExpr, timeoutMs = 30000, opts = {}) {
  const settle = opts.settle === true
  const pid = adbSoft(['shell', 'pidof', PKG], 15000).trim().split(/\s+/)[0]
  if (!pid) return false
  const socks = adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`], 15000)
    .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
  // ★ 2026-10-06 修：setRoute 里原来是 `find(…) || socks[socks.length - 1]`。
  //   cdpEval 侧在 2026-10-04 已经把这个兜底删掉并写了理由，**这里漏了**。
  //   本机同时装着 com.kaixuan.opencode.pocket 与 …pocket.sttdev，两个
  //   webview_devtools_remote_<pid> 都是活的，「取最后一个」会连到**另一个 App**，
  //   而 /json/list 与 Runtime.evaluate 一切正常、**不报任何错** ——
  //   结果是断言在另一个 App 上匹配。真机 4c308e2e 上两个包并存，已实测会踩中。
  const sock = socks.find((s) => s.endsWith(`_${pid}`))
  if (!sock) {
    console.log(`[cdp] pid=${pid} 的 socket 没出现（现有：${[...new Set(socks)].join(', ') || '无'}）`)
    return false
  }
  let port
  try {
    port = bindCdpForward(sock)      // 端口被占用会重试
  } catch {
    return false                     // 连不上就别往下走：下面的 finally 会误删别人的 forward
  }
  try {
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
    let lastHash = null
    let stable = 0
    while (Date.now() < deadline) {
      await sleep(800)
      const gotHash = (await ev('location.hash'))?.result?.value
      const gotReady = readyExpr ? (await ev(readyExpr))?.result?.value === true : true
      if (settle) {
        // ★ 2026-10-07 修：「连续两次采样不变」单独用会把
        //   **「守卫还没重算」误判成「已落定」**。
        //   采样间隔 800ms ⇒ 原判据在 ~1.6s 就可能返回 true；
        //   而调用方要的语义是「守卫已经算过一次并把路由改掉了」
        //   （清 token 后把 #/ai?__recheck=… 重定向到 #/login?returnTo=…）。
        //   刚装完的包要重新水合，守卫慢一点，原判据就提前放行
        //   ⇒ 调用方紧接着读 hash 拿到的仍是 #/ai?__recheck=…
        //   ⇒ 报「已清登录态但 App 停在 #/ai」（2026-10-07 在 sttdev 包上实测）。
        //
        // 补上真正缺的那一条：**必须离开我请求的路由**，
        // 且离开之后连续两次采样不变，才算落定。
        if (gotHash && gotHash !== hash && gotHash === lastHash) {
          stable++
          if (stable >= 1 && gotReady) { ws.close(); return true }
        } else {
          stable = 0
        }
        lastHash = gotHash
      } else if (gotHash === hash && gotReady) {
        ws.close(); return true
      }
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
  // 设备侧端口 = App 里烧进去的 API 基址端口（通常 18099）
  // 宿主侧端口 = 这个 worktree 的后端实际监听在哪
  // 两者不同时映射是 tcp:<dev> -> tcp:<host>，守卫核对的目标必须跟着变。
  // 否则"同端口"这个隐含假设会把本 worktree 锁死在"必须抢别人端口"上。
  const dev = process.env.POCKET_DEVICE_PORT || port
  const findMapping = (list) => list.split(/\r?\n/).map((x) => x.trim())
    .find((l) => new RegExp(`tcp:${dev}\\s`).test(l)
      && (l.match(/tcp:\d+\s+tcp:(\d+)/) || [])[1] === port) || null
  if (dev !== port) {
    console.log(`[preflight] 设备端口 ${dev} != 宿主端口 ${port}：App 仍用 ${dev}，映射改指本 worktree 的后端`)
  }
  let list = ''
  try { list = adb(['reverse', '--list'], 15000) } catch { /* 没配 reverse */ }
  // 设备端口上可能挂着**别的会话**的映射（指向它的宿主端口）。那种映射
  // "通得很正常"，但后面全是另一个后端的数据：dev 口令不同 => 登录 401；
  // 即便登进去了，列表恒空且没有任何报错。极易误判成产品缺陷。
  const anyForDev = list.split(/\r?\n/).map((l) => l.trim())
    .find((l) => new RegExp(`tcp:${dev}\\s`).test(l))
  const mapping = findMapping(list)
  const target = mapping ? (mapping.match(/tcp:\d+\s+tcp:(\d+)/) || [])[1] : null
  if (anyForDev && !mapping) {
    const t = (anyForDev.match(/tcp:\d+\s+tcp:(\d+)/) || [])[1]
    console.error(`[preflight] ⚠️ 设备 tcp:${dev} 被映射到了宿主 tcp:${t}（不是本 worktree 的 ${port}），正在改指`)
    try {
      adb(['reverse', '--remove', `tcp:${dev}`], 15000)
      adb(['reverse', `tcp:${dev}`, `tcp:${port}`], 15000)
      if (!findMapping(adb(['reverse', '--list'], 15000))) {
        console.error('[preflight] ❌ 改指失败'); return false
      }
      console.error(`[preflight] ✅ 已改指为 tcp:${dev} → tcp:${port}`)
    } catch (e) {
      console.error(`[preflight] ❌ 改指失败：${e?.message || e}`); return false
    }
  } else if (!mapping) {
    // 设备重连（adb kill-server / WiFi 抖动 / 换 USB 模式）会把 reverse 映射整个清掉。
    // 正确映射唯一（App 的 API 基址端口 → 宿主同一端口），直接补上。
    console.error(`[preflight] ⚠️ 设备上没有 tcp:${dev} 的 adb reverse 映射，正在补建`)
    try {
      adb(['reverse', `tcp:${dev}`, `tcp:${port}`], 15000)
      const after = findMapping(adb(['reverse', '--list'], 15000))
      const now = after ? (after.match(/tcp:\d+\s+tcp:(\d+)/) || [])[1] : null
      if (now !== port) { console.error(`[preflight] ❌ 补建失败，当前映射：${after || '<无>'}`); return false }
      console.error(`[preflight] ✅ 已补建为 tcp:${dev} → tcp:${port}`)
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
      adb(['reverse', '--remove', `tcp:${dev}`], 15000)
      adb(['reverse', `tcp:${dev}`, `tcp:${port}`], 15000)
      const after = findMapping(adb(['reverse', '--list'], 15000))
      const now = after ? (after.match(/tcp:\d+\s+tcp:(\d+)/) || [])[1] : null
      if (now !== port) {
        console.error(`[preflight] ❌ 自愈失败，当前映射：${after || '<无>'}`)
        return false
      }
      console.error(`[preflight] ✅ 已自愈为 tcp:${dev} → tcp:${port}`)
    } catch (e) {
      console.error(`[preflight] ❌ 自愈失败：${e?.message || e}`)
      return false
    }
  }
  // 映射对了，再确认设备上真的能拿到 200（reverse 存在但宿主没监听也会失败）
  try {
    const out = adb(['shell', `curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:${dev}/healthz`], 20000).trim()
    if (out !== '200') {
    console.error(`[preflight] ❌ 设备 curl 127.0.0.1:${dev}/healthz 返回 "${out}"`)
      console.error(`           先起后端：powershell -ExecutionPolicy Bypass -File scripts/start-local-backend.ps1`)
      return false
    }
  } catch {
    console.log(`[preflight] 设备侧 curl 不可用，跳过（映射本身已核对为 tcp:${port} → tcp:${port}）`)
  }
  console.log(`[preflight] 设备可达后端 ${base}（reverse tcp:${dev} → tcp:${port}）✅`)

  // ⚠️ 2026-10-04 补：**POCKET_API_BASE_OVERRIDE=0 时，App 走的是构建期那个 LAN 基址，
  //    reverse 通道完全用不上。** 上面那句「设备可达后端」此时是**误导**——
  //    它证明的是 reverse 通，不是 App 真正要走的那条路通。
  //
  // 实测踩过：设备重启后（pid 从 31052 变成 1315）设备→主机 LAN 方向 100% 丢包
  //（同 /24 网段、IP 都没变、主机能 ping 到设备、反向不行，防火墙 Public/Private 档都是禁用的），
  // App 登录页显示「登录失败，请检查网络连接与后端地址后重试」、token 长度 0。
  // 而 preflight 只在 30s 后报一句「❌ 登录后仍停在登录页（30s），last hash=(超时)」
  // —— 把一个网络问题伪装成登录问题，排查方向整个被带偏（先去查了后端接口，实测 200 正常）。
  //
  // ⇒ 这里在跑 flow 之前就把「App 真正要走的那条路通不通」验掉，通不了就直接说清楚是哪一条。
  if (process.env.POCKET_API_BASE_OVERRIDE === '0') {
    let lanBase = process.env.POCKET_API_BASE_LAN || null
    if (!lanBase) {
      for (const p of [resolve('frontend/.env.android-dev'), resolve('../frontend/.env.android-dev')]) {
        if (!existsSync(p)) continue
        const hit = (readFileSync(p, 'utf8').match(/VITE_API_BASE\s*=\s*(\S+)/) || [])[1]
        if (hit) { lanBase = hit; break }
      }
    }
    // ⚠️ 读不到就**响亮告警**，不许静默跳过。
    //   本 worktree 里 frontend/.env.android-dev 常常不存在（那个文件只在使用
    //   build-mobile 的 worktree 里），于是这条守卫会**整段失效且一声不吭** ——
    //   「判据存在但不触发」比「没有判据」更危险，因为它让人以为有网。
    //   兜底从 App 自己读：登录页页脚就渲染着当前后端地址。
    if (!lanBase) {
      try {
        lanBase = (await cdpEval(`(function(){
          try {
            var m = (document.body.innerText || '').match(/http:\\/\\/[^\\s]{4,60}/g) || [];
            return m.filter(function(u){ return /:\\d{4,5}/.test(u) })[0] || null;
          } catch (e) { return null }
        })()`)) || null
      } catch { /* 取不到就按下面的告警走 */ }
    }
    if (!lanBase) {
      console.error('[preflight] ⚠️ POCKET_API_BASE_OVERRIDE=0，但**读不到 App 实际会用的构建期基址**')
      console.error('           （frontend/.env.android-dev 不存在，页内也没扫到 http://…:port）')
      console.error('           ⇒ 「设备能否到达构建期基址」这条守卫现在是**失效**的，本轮不会替你验它。')
      console.error('           可用 POCKET_API_BASE_LAN=http://<host>:<port> 显式指定以恢复该校验。')
    } else {
      // 设备侧 timeout 兜底：curl -m 5 之外再加一层 shell timeout，
      // 否则遇到黑洞地址（丢包不回 RST）时 adb shell 会一直挂着。
      const probeCmd = `timeout 12 curl -s -m 5 -o /dev/null -w '%{http_code}' ${lanBase}/healthz`
      try {
        const code = adb(['shell', probeCmd], 25000).trim()
        if (code !== '200') {
          console.error(`[preflight] ❌ POCKET_API_BASE_OVERRIDE=0 ⇒ App 走构建期基址 ${lanBase}，`)
          console.error(`           但设备 curl ${lanBase}/healthz 返回 "${code}"（拿不到 200）。`)
          console.error(`           reverse 通道（127.0.0.1:${dev}）是通的，但**这一轮用不上**。`)
          console.error(`           ⇒ 别去查登录/后端：先修设备到主机的网络，或去掉 POCKET_API_BASE_OVERRIDE=0 走 reverse。`)
          return false
        }
        console.log(`[preflight] 构建期基址 ${lanBase} 设备侧可达（200）✅`)
      } catch (e) {
        const msg = String(e?.message || e)
        // ⚠️ 这里必须区分两种失败，**不能一律降级成告警**（2026-10-04 自踩）：
        //   「设备没装 curl」  → 探不了，守卫确实失效，响亮告警但继续；
        //   「探测本身到不了」 → 黑洞地址会让 adb shell 超时抛错，而这**本身就是
        //     「App 走这个基址会连不上」的证据**。当成「curl 不可用」放过，
        //     等于把唯一的判据在最该报警的时候关掉。
        if (/not found|command not found|enoent/i.test(msg)) {
          console.error(`[preflight] ⚠️ 设备上没有 curl，无法验证 ${lanBase} 是否可达`)
          console.error('           ⇒ 这条守卫现在是**失效**的，本轮不会替你验构建期基址。')
          console.error('           （reverse 通道是通的；如果 App 其实走的是构建期基址，登录会失败。）')
        } else {
          console.error(`[preflight] ❌ 探测 ${lanBase} 失败：${msg.split('\n')[0].slice(0, 120)}`)
          console.error('           设备连**探测**都做不到 ⇒ 走到这个基址必然连不上。')
          console.error(`           reverse 通道（127.0.0.1:${dev}）是通的，但**这一轮用不上**。`)
          console.error('           ⇒ 修设备到主机的网络，或去掉 POCKET_API_BASE_OVERRIDE=0 走 reverse。')
          return false
        }
      }
    }
  }
  return true
}

/**
 * 守卫：让 App **真的**走 adb reverse 这条通道，并从 App 自己的网络栈验通。
 *
 * 2026-10-02 查出来的硬伤（此前所有轮次都建立在错前提上）：
 *   本文件上方三处注释都写着「App 的 API 基址是 http://127.0.0.1:18099」，
 *   assertBackendUp / assertDeviceReachesBackend 也都按这个前提去核对。
 *   但装机的那版 APK 是用 frontend/.env.android-dev 构的，而那个文件里是
 *     VITE_API_BASE=http://192.168.31.20:18099      ← **LAN 地址**
//   实际生效的基址是 localStorage.pocket_api_base 优先于它（api-base.ts:4），
//   而这个 key 是**上一轮调试遗留下来的、从来没人断言过**的值。2026-10-02
//   真机读回时它是 http://localhost:18099 —— override 通道确实在起作用，
//   所以初版写的「App 压根没走 reverse」是**错的**（已更正）。
//
//   真缺陷是「**从未断言**」：
//     · key 缺失 ⇒ 落回构建期 LAN 18099 = 另一个会话的后端（口令不同）
//     · key 陈旧 ⇒ 指向一个已经没人监听的端口
//   两种情况下宿主 200 / 设备 curl 200 / reverse 映射正确**三道全绿**，
//   而 App 读的是别的后端。健康检查全绿与功能为空可以同时成立。
 *
 *   后果不是「跑不通」，而是**跑得通但结论是别人的**：宿主侧 200、设备侧
 *   curl 200、reverse 映射正确 —— 三道守卫全绿，而 App 读的是另一个后端
 *   的数据。这正是「所有健康检查都绿、功能却是空的」那一族。
 *
 * 修法：用产品自己支持的开关（设置页「后端服务器」写的同一个 key，
 * frontend/src/config/api-base.ts:8 `pocket_api_base`）把 App 指向
 * http://127.0.0.1:<dev>，由 adb reverse 接到本 worktree 的后端。
 * api-base.ts:136-137 明确：显式填的 loopback **不**被
 * `loopbackBuildRejected` 拒掉，因为「adb reverse 开发流确实需要用户
 * 主动指定 localhost」—— 这条路径是设计内的。
 *
 * 断言必须落在 App 自己的网络栈上（页内 fetch），不是 adb shell curl：
 *   adb shell curl 只证明**手机 OS** 能到那个端口；页内 fetch 才证明
 *   **App 的 WebView + CORS + 解析逻辑**能到。两者不是一回事。
 *
 * POCKET_API_BASE_OVERRIDE=0 可关闭（只在你确实想测构建期那个 LAN 基址时）。
 */
async function assertAppUsesReverseBase() {
  const hostBase = process.env.POCKET_API_BASE || 'http://127.0.0.1:18099'
  const hostPort = (hostBase.match(/:(\d+)/) || [])[1]
  if (!hostPort) { console.error('[preflight] POCKET_API_BASE 里解析不出端口，无法推导设备侧基址'); return false }
  const dev = process.env.POCKET_DEVICE_PORT || hostPort
  const want = `http://127.0.0.1:${dev}`

  if (process.env.POCKET_API_BASE_OVERRIDE === '0') {
    console.log('[preflight] POCKET_API_BASE_OVERRIDE=0：不改 App 基址，'
      + '⚠️ 本轮 App 走的是构建期基址，reverse 通道未被使用')
    return true
  }

  // 构建默认值运行期读不到（import.meta.env 已烘死），但可以从本 worktree
  // 的 env 文件读出来打日志 —— 基址对不上是这里最可能的坑，必须让人看见。
  let buildDefault = '(读不到 .env.android-dev)'
  try {
    const envTxt = readFileSync(resolve(ROOT, 'frontend/.env.android-dev'), 'utf8')
    buildDefault = (envTxt.match(/^\s*VITE_API_BASE\s*=\s*(.+)$/m) || [])[1]?.trim() || '(未定义)'
  } catch { /* 换 checkout 布局时不该因此崩掉 */ }
  console.log(`[preflight] App 构建期基址（.env.android-dev）= ${buildDefault}`)
  console.log(`[preflight] 本轮要 App 改走 ${want} → adb reverse → 宿主 ${hostBase}`)

  const setRes = await cdpEval(`(function(){
    try {
      var before = localStorage.getItem('pocket_api_base');
      localStorage.setItem('pocket_api_base', ${JSON.stringify(want)});
      return JSON.stringify({ before: before, after: localStorage.getItem('pocket_api_base') });
    } catch (e) { return JSON.stringify({ err: String(e && e.message || e) }); }
  })()`)
  let setInfo
  try { setInfo = JSON.parse(String(setRes)) } catch { setInfo = { err: '写入返回的不是 JSON: ' + setRes } }
  if (setInfo.err) {
    console.error(`[preflight] ❌ 写 pocket_api_base 失败：${setInfo.err}`)
    return false
  }
  // 写入读回自证：localStorage.setItem 成功不等于值就是我们要的。
  if (setInfo.after !== want) {
    console.error(`[preflight] ❌ pocket_api_base 写入读回不一致：期望 ${want}，实际 ${setInfo.after}`)
    return false
  }
  console.log(`[preflight] 已设 pocket_api_base：${setInfo.before ?? '(空)'} → ${setInfo.after}`)

  // 基址是模块加载期解析的，改完必须重载才生效。不重载的话页内 fetch
  // 用的还是旧 base，而守卫照样会拿到 200 —— 又是一次假绿。
  await cdpEval('location.reload(); true')
  // 重载后等 App 外壳回来（等 hash 可读 + 有 #app/#root 之类容器）。
  let ready = false
  for (let i = 0; i < 30; i++) {
    await sleep(1000)
    const h = await cdpEval(`(function(){
      try { return (document.querySelector('#app, #root, .ai-view') ? 'ready' : '') + '|' + location.hash; }
      catch (e) { return 'ERR'; }
    })()`)
    if (typeof h === 'string' && h.startsWith('ready|')) { ready = true; break }
  }
  if (!ready) {
    console.error('[preflight] ❌ 重载后 30s 内 App 外壳没回来，基址改动未确认生效')
    return false
  }
  console.log('[preflight] App 已重载，外壳回来了')

  // 页内 fetch：走 App 自己的网络栈（含 CORS），reqwest 的 adb curl 覆盖不到。
  const probe = await cdpEval(`(function(){
    var race = function (p, ms, tag) {
      return Promise.race([p, new Promise(function (r) { setTimeout(function () { r({ err: tag }); }, ms); })]);
    };
    var base = localStorage.getItem('pocket_api_base') || '';
    return race(fetch(base + '/healthz', { cache: 'no-store' })
      .then(function (r) { return r.text().then(function (t) {
        return JSON.stringify({ base: base, status: r.status, body: t.trim().slice(0, 60) });
      }); })
      .catch(function (e) { return JSON.stringify({ base: base, err: String(e && e.message || e) }); }),
      10000, 'timeout');
  })()`, 20000)
  let po
  try { po = JSON.parse(String(probe)) } catch { po = { err: '页内 fetch 返回的不是 JSON: ' + probe } }
  if (po.err || po.status !== 200 || po.body !== 'ok') {
    console.error(`[preflight] ❌ App 内 fetch ${want}/healthz 失败：${JSON.stringify(po)}`)
    console.error('           这一步失败 ⇒ App 到本 worktree 的通道没通，后面全是不可解读的结果。')
    console.error(`           排查：adb reverse --list 里 tcp:${dev} 是否指向 tcp:${hostPort}；`)
    console.error('           宿主该端口是否有 pocketd 在监听；后端是否放行了 WebView 的 CORS。')
    return false
  }
  console.log(`[preflight] App 内 fetch ${po.base}/healthz → ${po.status} ${po.body} ✅（App 确实在打本 worktree 的后端）`)
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
  // Platform dispatch. scripts/start-local-backend.ps1 only exists for Windows;
  // on a Linux host spawning powershell fails and the whole run aborts in
  // preflight, which reads as "the backend is broken" rather than "the launcher
  // for this platform is missing". The POSIX twin is scripts/start-local-backend.sh
  // and it keeps the same guarantees (env parsed without eval, port-owner
  // verified, master key checked).
  const isWindows = process.platform === 'win32'
  const launcher = isWindows
    ? { cmd: 'powershell', args: ['-ExecutionPolicy', 'Bypass', '-File', resolve(ROOT, 'scripts', 'start-local-backend.ps1')] }
    : { cmd: 'bash', args: [resolve(ROOT, 'scripts', 'start-local-backend.sh')] }
  // ⚠️ stdio 必须是 'ignore'：启动脚本内部会再拉起 pocketd 那个孙进程，
  //    孙进程会继承这里的 stdout/stderr 句柄。若用默认的 'pipe'，
  //    spawnSync 会一直等这些管道关闭 —— 表现是「后端明明起来了，
  //    preflight 却卡住不动」，实测卡了 3 分钟。踩过。
  const r = spawnSync(launcher.cmd, launcher.args, {
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

/**
 * 唤醒并解锁设备。
 *
 * 2026-10-02 实测踩到：设备在无人操作时会自动熄屏，`mWakefulness=Asleep`。
 * 这时 `monkey` 的启动意图发得出去，App 进程也真的会起来（pidof 有值），
 * 但**屏幕上没有任何窗口**，于是下面那个"等进前台"的检查必然 60s 超时，
 * 报出来的是「App 60s 内未进入前台，中止」—— 一句指向 App 的报错，
 * 真因却是设备在睡觉。App 到底能不能起，跟它一点关系都没有。
 *
 * keyguard 同理：MIUI 的锁屏会让焦点停在 com.miui.home。
 */
function wakeDevice() {
  try {
    adb(['shell', 'input', 'keyevent', '224'], 15000)          // KEYCODE_WAKEUP
    adb(['shell', 'wm', 'dismiss-keyguard'], 15000)
    adb(['shell', 'input', 'keyevent', '82'], 15000)           // KEYCODE_MENU，解锁兜底
    adb(['shell', 'svc', 'power', 'stayon', 'true'], 15000)   // 测试期间别再睡
  } catch (e) {
    // 唤醒失败不直接判死：部分设备/模拟器没有 keyguard，dismiss 会报错。
    // 真正要不要继续，交给后面的前台检查去判。
  }
  const awake = /mWakefulness=Awake/.test(adb(['shell', 'dumpsys', 'power'], 20000) || '')
  console.log(awake ? '[preflight] 设备已唤醒并保持常亮 ✅' : '[preflight] ⚠️ 设备唤醒未确认，后续前台检查可能失败')
  return awake
}

async function preflight() {
  if (!(await ensureBackend())) return false
  if (!(await assertDeviceReachesBackend())) return false
  if (!(await ensureDriver())) return false
  wakeDevice()
  // 被测 App 必须先确保是 enabled。2026-10-03 在 vivo V2436A（OriginOS，Android 16）
  // 实测：上一轮跑完后系统把 com.kaixuan.opencode.pocket 置成
  // `enabled=3`（DISABLED_FOR_USER），于是 monkey 报
  // 「** No activities found to run, monkey aborted」——包还在、MainActivity
  // 的 MAIN/LAUNCHER filter 也还在，但整个包被禁用，resolve-activity 直接
  // 「No activity found」。现象是「App 装不上了/起不来」，真因是系统清理。
  // ensureDriver() 只对 maestro 的两个包做 pm enable，被测 App 不在其中。
  //
  // enabled 取值：0=DEFAULT（按 manifest，正常）1=ENABLED 2=DISABLED
  // 3=DISABLED_USER 4=DISABLED_UNTIL_USED。要救的是 2/3/4，0 和 1 都不动。
  try {
    const info = adb(['shell', 'dumpsys', 'package', PKG], 30000)
    const u0 = (info.split(/\r?\n/).find((l) => l.trim().startsWith('User 0:')) || '')
    const m = u0.match(/\benabled=(\d+)/)
    const state = m ? Number(m[1]) : null
    if (state === 2 || state === 3 || state === 4) {
      adb(['shell', 'pm', 'enable', PKG], 30000)
      console.log(`[preflight] 被测 App 处于 enabled=${state}（系统清理所致），已 pm enable ${PKG}`)
    } else {
      console.log(`[preflight] 被测 App enabled=${state === null ? '?' : state}（无需处理）`)
    }
  } catch (e) {
    console.log(`[preflight] 检查 ${PKG} 启用状态失败（继续）：${String(e.message || e).slice(0, 120)}`)
  }
  console.log('[preflight] 强停并重新启动 App（绕开 MIUI 吞掉 force-stop 后启动意图的问题）')
  try { adb(['shell', 'am', 'force-stop', PKG]) } catch { /* 本来就没跑 */ }
  await sleep(1500)
  adb(['shell', 'monkey', '-p', PKG, '-c', 'android.intent.category.LAUNCHER', '1'], 30000)
  // 等 WebView 真正起来，而不是盲等固定秒数
  const deadline = Date.now() + 60000
  while (Date.now() < deadline) {
    await sleep(1500)
    const pid = adbSoft(['shell', 'pidof', PKG]).trim()
    if (!pid) continue
    const resumed = adb(['shell', 'dumpsys', 'activity', 'activities'], 30000)
    // 注意别写成 topResumedActivity=\S*\s*<包名>：实际输出是
    //   topResumedActivity=ActivityRecord{6194969 u0 com.kaixuan.opencode.pocket/.MainActivity
    // 中间夹着 `u0`，\S* 跨不过空格，会**永远不匹配**——
    // 于是把「App 明明在前台」误报成「60s 未进前台」。踩过，别改回去。
    // ★ 2026-10-06 修：原判据 `/topResumedActivity.*opencode\.pocket/` 会把
    //   …opencode.pocket.**sttdev** 也判成「在前台」——两包并存时前台是 sttdev
    //   它照样通过，等于没有判据。
    //   ⚠️ 这里**不能用 \b**：sttdev 的包名在 `pocket` 之后紧跟一个 `.`，
    //   而 `.` 是非词字符，`\b` 正好在那里成立 ⇒ 判据仍然恒真（我第一版就这么写，
    //   自测当场抓到：sttdev 在前台仍返回 true）。必须要求包名后接 `/`（类名分隔）
    //   或行尾/空白，不能只要求「不是词字符」。
    if (new RegExp(`topResumedActivity.*\\b${PKG.replace(/\./g, '\\.')}(?:/|\\s|$)`).test(resumed)) {
      console.log(`[preflight] App 已在前台 pid=${pid.trim()}`)
      await assertFetchIntact()
      if (!(await assertAppUsesReverseBase())) return false
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
  //
  // ★ 2026-10-07 修一处 locale 依赖：这个选择器原先**写死中文**「打开菜单」。
  // 设备 locale 是 en-US 时 App 渲染出的是 `aria-label="Open menu"`
  // （i18n 两侧都有：zh-CN.json「打开菜单」/ en-US.json「Open menu」），
  // 于是判据恒 false →「复位失败；App 当前实际停在 #/ai」——
  // **hash 其实完全正确**，报错却指向「起点不确定」，方向完全反了。
  //
  // 现在从 i18n 文件按设备的实际 locale 取值；取不到就退回「只要 App 外壳容器
  // 渲染出来就算 ready」，而不是继续拿中文去撞英文设备。
  const menuLabel = resolveOpenMenuLabel()
  // ★ 2026-10-07 再修一届：上一版改成“按设备 locale 取 aria-label”仍然不够———
  //   **App 的 locale 与设备 locale 可以不一致**。本机 CDP 直读 DOM 实测：
  //     ro.product.locale             = en-US   → 设备的
  //     document.documentElement.lang = zh-CN  → App 的
  //     aria-label 实际渲染 = "打开菜单"
  //   下才按设备 locale 取到 "Open menu"，DOM 里根本没有 → readyExpr 恒 false
  //   → 报“复位到 #/ai 失败；App 当前实际停在 #/ai”——hash 实际完全正确，
  //     **报错方向与真因相反**（同一个坑第二次换了个形态回来）。
  //
  // 没为何不换成“两个标签都试：斥诀的是“★明“数资源、
  // 不省能所刻“才启动眻“斩放形式序转换闪速返回关键页。
  const readyExpr = `!!document.querySelector('[aria-label="\u6253\u5f00\u83dc\u5355"],[aria-label="Open menu"],#app,#root,.ai-view')`
  const ok = await setRoute(route, readyExpr)
  if (!ok && process.env.POCKET_DEBUG_SETROUTE === '1') {
    // 诊断用（默认不输出）：把 readyExpr 与设备 locale 一起打出来。
    // 没有这一行时，「判据为 false」和「取值函数返回了 null」两种形态
    // 在日志里**长得一模一样**——都只是「复位失败」，
    // 而两者的修法完全不同（一个改选择器，一个改 locale 解析）。
    const dbgLocale = ['persist.sys.locale', 'ro.product.locale', 'ro.sys.locale']
      .map((p) => `${p}=${JSON.stringify(adbSoft(['shell', 'getprop', p], 8000).trim())}`).join(' ')
    console.error(`[debug] menuLabel=${JSON.stringify(menuLabel)} ${dbgLocale}`)
    console.error(`[debug] readyExpr=${readyExpr}`)
  }
  if (ok) {
    console.log(`[preflight] 已复位到 ${route} 且 App 外壳已渲染`)
  } else {
    // 失败不等于"状态不确定"。把实际路由读出来，分两种：
    let actual = '(读不到)'
    try { actual = await cdpEval('location.hash') } catch { /* 通道也坏了 */ }
    // A. 落在登录页 = **确定**的未登录态。preflight 会先清登录态，
    //    守卫按设计把 #/ai 弹回 #/login?returnTo=…；smoke-login 这类
    //    "先登录"的 flow 正是要这个起点，判死它反而把正常状态报成错误。
    if (/#\/login/.test(actual || '')) {
      console.log(`[preflight] 未登录态：App 在 ${actual}（这是确定的起点，要登录的 flow 可直接用）`)
    } else {
      // B. 落在别的业务页 = 起点不确定，flow 的断言不可解释。
      console.error(`[preflight] ❌ 复位到 ${route} 失败；App 当前实际停在 ${actual}`)
      console.error('           起点不确定 ⇒ 后续所有"等某个元素出现"的断言都不可解释。')
      console.error('           常见原因：App 进程刚起还没渲染完 / 上一次调试把它停在了深层页面。')
      console.error('           确实要在不确定起点上跑（调试用）：设 POCKET_ALLOW_UNCERTAIN_START=1')
      if (process.env.POCKET_ALLOW_UNCERTAIN_START !== '1') process.exit(3)
      console.error('           已按 POCKET_ALLOW_UNCERTAIN_START=1 继续 —— 本次 flow 结果不可解释')
    }
  }
}

// 复位完成后再清登录态：顺序不能反，否则清完 token 页面又会把旧壳渲染回来。
await resetAppAuth()


/**
 * 守卫：确保本地 SQLCipher 库处于**已解锁**状态，并自证这个守卫不是恒真。
 *
 * 为什么要做（2026-10-02 真机实测）：
 *   本地库的 AES key 走 crypto.ts:53 initAppCrypto() 的 PBKDF2 派生，需要主密码。
 *   守卫只在「导航到依赖本地库的路由」那一刻才判定，所以：
 *     · 停在 #/ai 时**看不出**库锁着（#/ai 不依赖本地库）
 *     · 一进 #/pkm/today 就被弹到 #/login?...&unlock=1（routeGuards.ts:126-129
 *       的 redirectUnlock）
 *   `_goto-pkm.yaml` / `_login.yaml` 里那条 `inputText: ${POCKET_MASTER}`
 *   就是为这件事准备的 —— 但它**必然失效**：Maestro 把 `${...}` 展开成字符串，
 *   变量不在它变量域里时就变成字面量 `"undefined"`
 *   （同 ${POCKET_DEV_PASS}，见本文件上方与 handoff §4.82.5/§4.83.6）。
 *   `_goto-pkm.yaml` 自己的注释其实已经点破了机制：
 *     `evalScript: ${location.hash='#/more'}` → Cannot set property 'hash' of undefined
 *     「说明 evalScript 不在 WebView 的 JS 上下文里跑」
 *
 * 为什么用 CDP 而不是坐标：
 *   那个密码框在 Android 无障碍树里是 `[EditText] t="" cd=""`，Maestro 只能按
 *   坐标点（50%,59%），而坐标依赖布局与机型。DOM 里它有 placeholder
 *   `输入主密码解锁` —— **可访问性树里没有的东西，DOM 里有**。按 placeholder
 *   定位不依赖任何坐标。
 *
 * ⚠️ 判「解锁屏在不在」必须用 bodyText，不能用某个标签的精确文本匹配。
 *    2026-10-02 实测踩过：`document.querySelectorAll('label,div,span,h1,h2')` 里
 *    找 textContent === '解锁本地数据' **恒为 false**，而同一时刻
 *    document.body.innerText 明明以「解锁本地数据 检测到已有登录态…」开头。
 *    用那个检查当守卫 ⇒ 永远判「已解锁」⇒ 跳过解锁 ⇒ 后面全是不可解读的结果。
 *    「恒为 false」的检查和「恒为 true」的一样有害。
 *
 * 收尾必须**再自证一次**：解锁完重新导航回 PKM 页，确认解锁屏**不再出现**。
 * 只报「点了解锁」不算——那正是「判断自己成功」的形状。
 */
async function ensureLocalDbUnlocked() {
  const master = process.env.POCKET_MASTER
  if (!master) {
    console.error('[preflight] 未提供 POCKET_MASTER，无法保证本地库已解锁。')
    console.error('  PKM / 笔记 / 闪卡等依赖本地库的功能会落到解锁屏，断言不可解释。')
    console.error('  $env:POCKET_MASTER="<主密码>"   # 本机测试装置上约定的那个值')
    return false
  }
  // 已解锁的判定：页面上有没有这段文案。用它而不是标签匹配（见上）。
  const ON_UNLOCK = '解锁本地数据'
  const readScreen = async () => {
    const raw = await cdpEval(`(function(){
      try {
        return JSON.stringify({
          hash: location.hash,
          body: (document.body.innerText || '').replace(/\\s+/g, ' ').slice(0, 1200)
        });
      } catch (e) { return JSON.stringify({ err: String(e && e.message || e) }); }
    })()`)
    try { return JSON.parse(String(raw)) } catch { return { err: '读屏返回非 JSON: ' + raw } }
  }
  // 导航到依赖本地库的路由，逼守卫把解锁屏弹出来。
  const goRoute = async (hash) => {
    await cdpEval(`location.hash = ${JSON.stringify(hash)}; true`)
    for (let i = 0; i < 15; i++) {
      await sleep(1000)
      const h = await cdpEval('location.hash')
      if (h && !/^undefined$/.test(String(h))) return String(h)
    }
    return '(读不到)'
  }

  const probeRoute = '#/pkm/today'
  await goRoute(`${probeRoute}?__unlockprobe=${Date.now()}`)
  await sleep(2000)
  let s = await readScreen()
  if (s.err) {
    console.error(`[preflight] ❌ 读屏失败：${s.err}`)
    return false
  }

  if (!String(s.body || '').includes(ON_UNLOCK)) {
    console.log(`[preflight] 本地库已解锁（${s.hash} 无「${ON_UNLOCK}」屏）`)
    return true
  }

  console.log(`[preflight] 本地库锁着（${s.hash}），用 CDP 填主密码解锁`)
  // 解锁屏有**两种形态**，取决于设备是否已绑定生物特征：
  //   A) 未绑定 → 一个 placeholder=「输入主密码解锁」的框 + 一个文本为「解锁」的按钮
  //   B) 已绑定 → placeholder 变成「可留空，点认证使用指纹或人脸」，
  //      按钮文本是「认证」（指纹/人脸），主密码仍可填在同一��框里
  // 2026-10-03 在 vivo V2436A（Android 16，已录指纹）上实测撞的是 B：
  // 旧代码只认 A 的 placeholder，于是报「没有 placeholder=输入主密码解锁 的
  // 输入框」并中止——**把「这台机器的形态不同」报成了「解锁流程坏了」**。
  // 现在按「密码类输入框」定位，按钮按「解锁/认证」两套文案都认。
  const filled = await cdpEval(`(function(){
    var ins = Array.prototype.slice.call(document.querySelectorAll('input'));
    var el = ins.filter(function (e) { return e.type === 'password'; })[0]
      || ins.filter(function (e) { return (e.placeholder || '').indexOf('主密码') >= 0; })[0]
      || ins.filter(function (e) { return e.type === 'password' || e.type === 'text'; })[0];
    if (!el) {
      return '页面上没有可用的密码输入框；实际有：'
        + ins.map(function (e) { return e.placeholder || e.type; }).join(' / ');
    }
    var set = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
    set.call(el, ${JSON.stringify(master)});
    el.dispatchEvent(new Event('input', { bubbles: true }));
    return 'ok len=' + el.value.length + ' ph=' + (el.placeholder || '?');
  })()`)
  if (!String(filled).startsWith('ok ')) {
    console.error(`[preflight] ❌ 填主密码失败：${filled}`)
    return false
  }
  // 解锁/认证按钮必须随输入解禁 —— 这条同时证明 v-model 接上了值。
  const clicked = await cdpEval(`(function(){
    var bs = Array.prototype.slice.call(document.querySelectorAll('button'));
    var b = bs.filter(function (e) { var t=(e.innerText||'').trim(); return t === '解锁' || t.indexOf('认证') >= 0; })[0];
    if (!b) return '没有「解锁」/「认证」按钮；实际有：' + bs.map(function (e) { return (e.innerText||'').trim(); }).join(' / ');
    if (b.disabled) return '「' + (b.innerText||'').trim() + '」仍是 disabled：v-model 没接上值';
    b.click();
    return 'ok clicked ' + (b.innerText||'').trim();
  })()`)
  if (!String(clicked).startsWith('ok ')) {
    console.error(`[preflight] ❌ 点解锁失败：${clicked}`)
    return false
  }
  for (let i = 0; i < 20; i++) {
    await sleep(1000)
    s = await readScreen()
    if (!String(s.body || '').includes(ON_UNLOCK)) break
  }
  if (String(s.body || '').includes(ON_UNLOCK)) {
    console.error(`[preflight] ❌ 点了解锁后 20s 内解锁屏仍在（hash=${s.hash}）`)
    console.error('           主口令不对，或本地库根本没解锁成功。')
    return false
  }
  console.log(`[preflight] 已解锁（hash=${s.hash}）`)

  // ── 自证：回到 PKM 页确认解锁屏**不再出现** ──
  // 少了这一步，上面的「解锁屏消失」可能只是路由换页的副作用，
  // 而下一次导航又被弹回来 —— 也就是解锁其实没生效。
  await goRoute(`${probeRoute}?__unlockverify=${Date.now()}`)
  await sleep(2000)
  const v = await readScreen()
  if (String(v.body || '').includes(ON_UNLOCK)) {
    console.error(`[preflight] ❌ 自证失败：重新导航到 ${probeRoute} 后解锁屏又出现了（hash=${v.hash}）`)
    console.error('           说明刚才的解锁并没有真正生效，别继续跑 flow。')
    return false
  }
  console.log(`[preflight] 自证通过：再次进入 ${probeRoute} 不再弹解锁屏 ✅`)
  return true
}

// 清完 token 之后**必须再走一次路由**：routeGuards.ts 的 syncFromStorage()
// 是在导航时才跑的，而上面那次复位发生在清 token 之前 —— 于是守卫用
// 「还是登录态」的进程内 store 放行，App 就带着一个已经不存在的 token
// 继续停在业务页上。2026-10-02 实测：smoke-login 连红两轮，根因一直看不见，
// 表象却像「App 没反应」。再导航一次，守卫重算，起点才确定是登录页。
//
// 2026-10-02 修掉这里一处自伤：原来无条件 setRoute('#/ai')，但未登录时守卫
// 会把它弹成 '#/login?returnTo=/ai'，hash 永远不等于 '#/ai' ⇒ setRoute 必然
// 空转满 30s 才返回 false。判据最后只读 hash，于是照样判「通过」——
// 代价是每轮白等 30 秒，而且 setRoute 的 ready 判据压根没起作用（形同虚设）。
// 改成：已经在登录页就跳过导航；不在才导航，且只给 8s 短超时。
{
  const want = process.env.POCKET_START_ROUTE || '#/ai'
  let h = '(读不到)'
  try { h = String(await cdpEval('location.hash') || '') } catch { /* 通道也坏了 */ }
  if (/#\/login/.test(h)) {
    console.log(`[preflight] 已在登录页（${h}），无需再导航`)
  } else {
    // ready 判据放宽为 true：登录页没有 App 外壳的「打开菜单」那层。
    // 必须制造一次**真实的 hash 变化**。
    // 2026-10-02 负控实测：App 已经停在 #/ai 时，location.hash = "#/ai"
    // **不产生 hashchange**（浏览器只在字符串真的变了才发）⇒ 路由守卫不重算
    // ⇒ App 带着一个刚被清掉的 token 继续停在业务页上。
    // 追加一次性 query（Vue Router 的 hash 模式正常解析该 query），
    // 保证目标字符串与当前 hash 必然不同，hashchange 一定触发。
    // timeout 给 5s：成功时 hash 变成 #/login，失败也只是回到原页，
    // 两种都不致命，真正的判据是下面那次读 hash。
    // settle=true：这里要的只是「守卫已经算过一次并落定」，落点由守卫决定
    // （见 setRoute 的注释：精确匹配在这里结构上不可能成立）。
    await setRoute(`${want}?__recheck=${Date.now()}`,
      'true', 5000, { settle: true })
    try { h = String(await cdpEval('location.hash') || '') } catch { /* 通道也坏了 */ }
  }
  const atLogin = /#\/login/.test(h)
  console.log(atLogin
    ? `[preflight] 已清登录态并落到登录页（${h}）—— flow 起点确定`
    : `[preflight] ⚠️ 已清登录态但 App 停在 ${h}，没有落到登录页`)
  if (!atLogin && process.env.POCKET_ALLOW_UNCERTAIN_START !== '1') {
    console.error('           flow 里「等登录页出现」必然等不到。请先查为什么守卫没重算。')
    process.exit(3)
  }
}

// ── 登录：CDP 驱动真实表单 ────────────────────────────────────────────
// 为什么不用 Maestro 敲键盘：2026-10-02 实测 `${POCKET_DEV_PASS}` 被 Maestro
// 展开成**字面量 undefined**（_probe-env.yaml 坐实：框内容 adminPWLEN-undefined），
// 而 `--env` 会把口令暴露在进程命令行里。两者都不接受，改由 CDP 直接填真实
// 表单。**换掉的是「谁来敲键盘」，不是「被测什么」** —— 走的是同一个
// LoginView 表单、同一个 POST /api/auth/login、同一个 401/200 判定。
//
// ⚠️ 顺序：这一段必须排在上面「清完 token 后再走一次路由」**之后**。
// 反过来的话起点还没落到登录页，登录表单根本不存在，填表只会拿到 0 个输入框。
// 2026-10-02 就是这么写的，顺序错了以后登录成功必然误报 exit 3。
// POCKET_SKIP_CDP_LOGIN=1 可跳过（例如只想验「未登录态被正确弹回」的 flow）。
if (process.env.POCKET_SKIP_CDP_LOGIN !== '1') {
  const passJson = JSON.stringify(DEV_PASS)
  const userJson = JSON.stringify(process.env.POCKET_DEV_USER || 'admin')
  // 按 placeholder 定位，不按下标：下标取决于当前 Tab 和指纹区块，
  // 一旦 App 停在「解锁」界面（BUG-AV 场景：已登录但 crypto 未初始化）
  // 就会填到错误的框里，而**填错框看起来和填对一样**。
  // 定位不到时把页面上真实的 placeholder 全部打出来，
  // 让报错指向「界面不是登录表单」而不是一句没信息量的「0 个输入框」。
  const filled = await cdpEval(`(function(){
    var ins = Array.prototype.slice.call(document.querySelectorAll('input'));
    function byPh(p) {
      return ins.filter(function (e) { return (e.placeholder || '') === p; })[0];
    }
    var u = byPh('输入用户名'), pw = byPh('输入密码');
    if (!u || !pw) {
      return '页面上没有「输入用户名/输入密码」；实际有 ' + ins.length + ' 个输入框：'
        + (ins.map(function (e) { return e.placeholder || e.type || '?'; }).join(' / ') || '(无)')
        + '；当前路由 ' + location.hash;
    }
    // Vue 的受控 input 必须用原型上的 value setter 再派发 input 事件，
    // 直接写 el.value 不触发 v-model 更新（写进去了但状态没变，提交仍是空）。
    var set = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
    function put(el, v) { set.call(el, v); el.dispatchEvent(new Event('input', { bubbles: true })); }
    put(u, ${userJson});
    put(pw, ${passJson});
    return 'ok user=' + u.value.length + '字符 pass=' + pw.value.length + '字符';
  })()`)
  if (typeof filled !== 'string' || !filled.startsWith('ok ')) {
    console.error(`[preflight] ❌ 填登录表单失败：${filled}`)
    process.exit(3)
  }
  console.log(`[preflight] 已填登录表单（${filled.slice(3)}，用户 ${userJson}）`)
  // 提交：点文本为「登录」的按钮。用精确相等，避开「指纹登录」/「密码登录」。
  const clicked = await cdpEval(`(function(){
    var els = Array.prototype.slice.call(document.querySelectorAll('button, [role=button], input[type=submit]'));
    var texts = els.map(function (e) { return (e.innerText || e.value || '').trim(); });
    var hit = els.filter(function (e) { return (e.innerText || e.value || '').trim() === '登录'; })[0];
    if (!hit) return '页面上没有文本为「登录」的按钮；实际按钮：' + texts.join(' / ');
    hit.click();
    return 'ok clicked';
  })()`)
  if (clicked !== 'ok clicked') {
    console.error(`[preflight] ❌ 提交登录失败：${clicked}`)
    process.exit(3)
  }
  // ⚠️ 2026-10-04 补：**登录后可能压着「创建主密码」弹窗**，它会挡住路由跳转。
  //
  // 现场：登录**其实成功了**（pocket_token 291 字符，与后端一致，
  // /api/auth/login 实测 200），但对话框让 `location.hash` 一直停在
  // `#/login` ⇒ 下面「等 hash 离开 #/login」30s 超时 ⇒ **误报登录失败**。
  // 这条弹窗在「设备上还没有主密码」时必然出现（首登、或 App 数据被清过）。
  //
  // ⚠️⚠️ 填值手法试了四轮才成功，全部记在这里，别再走回头路：
  //   ① native setter + input 事件        → 值不进 v-model，提示不变
  //   ② 补 change/blur/keyup + 回读校验     → 仍不行
  //   ③ 只填第一个密码框                    → 漏了「再次输入主密码」那个
  //   ④ 逐字符派发键盘事件                  → 仍不行
  //   ⑤ **CDP `el.focus()` + adb `input text`（系统级真实输入）→ 成功**
  //      真机实测：焦点落在正确框、两框值都是 11、弹窗消失、hash 跳到 #/ai。
  // 原理：CDP 直接设 value 绕过了 Vue 的事件链；而 `input text` 走系统输入
  // 通路，v-model 一定收得到。用 focus 选框则避开了坐标换算在滚动页面上的错位。
  const master = process.env.POCKET_MASTER
  // ★ 2026-10-06 修：这里原来把 adb 路径**硬编码**成另一台 Windows 开发机的
  //   C:/Users/86133/…，绕过了顶部 whichFirst 那套解析（本机是 /opt/homebrew/bin/adb）。
  //   后果形态极具误导性：每次填框都报 spawnSync …adb.exe ENOENT，
  //   而日志打的是「主密码弹窗：两框已输入，长度=[14,0,0]」——
  //   看着像**第二框不接受这么长的口令**，实际是**那条命令压根没跑起来**。
  //   serial 同理，不再另开一份默认值（ DEVICE 已由 POCKET_SERIAL 解析）。
  const adbBin = ADB
  const serial = DEVICE
  const ash = (cmd) => execFileSync(adbBin, ['-s', serial, 'shell', cmd], {
    encoding: 'utf8', timeout: 30000, maxBuffer: 33554432,
  })
  // ★ 2026-10-06 修：`input text ${master}` 不加引号，口令里的 shell 元字符会被
  //   **设备侧 shell** 解释掉。实测一个 14 位口令（13 字母数字 + 1 元字符）
  //   只填进去 9 位，报出来的还是「两框已输入，长度=[14,9,0]」。
  //   修法：单引号包住，并转义内部的单引号。
  const shQuote = (s) => `'${String(s).replace(/'/g, `'\\''`)}'`
  const inputText = (s) => ash(`input text ${shQuote(s)}`)
  let masterDialogHandled = false
  for (let i = 0; i < 4; i++) {
    await sleep(800)
    const need = await cdpEval(`(function(){
      try {
        if ((document.body.innerText||'').indexOf('创建主密码') < 0) return 'no'
        var ins = Array.from(document.querySelectorAll('input'))
        var p1 = ins.filter(function(x){ return /至少/.test(x.placeholder||'') })[0]
        var p2 = ins.filter(function(x){ return /再次|确认主密码|重复/.test(x.placeholder||'') })[0]
        if (!p1) return 'no-input1'
        if (!p2) return 'no-input2'
        return 'present'
      } catch (e) { return 'err:' + String(e).slice(0, 80) }
    })()`)
    if (need === 'no') { masterDialogHandled = true; break }
    if (need !== 'present') {
      console.log(`[preflight] ⚠️ 「创建主密码」弹窗在但处理不了：${need}`)
      break
    }
    // 逐个框：CDP focus 选框（确定）→ adb input text 敲键盘（v-model 才收得到）
    //
    // ⚠️ 2026-10-05 修的真 bug：这里原来写的是
    //     return ${JSON.stringify(which)}.test(x.placeholder||'')
    //   `JSON.stringify('至少')` 产出的是**字符串字面量** `"至少"`，而字符串
    //   没有 .test 方法 ⇒ 抛 `TypeError: "至少".test is not a function`。
    //
    //   为什么之前一直没暴露：这段只在**设备上还没有主密码**时才会走到
    //   （首登、或 App 数据被清过——本轮卸载重装正好制造了这个状态）。
    //   之前所有轮次设备上都已设过主密码，`need` 直接返回 'no'，这段是死代码。
    //   典型的「长期不可达分支里藏着类型错误」：它安静地坏着，直到环境一变
    //   才第一次被真正执行。
    //
    //   后果形态极具误导性：`need` 的**检测**那一侧（1250/1251 行）写的是
    //   正则字面量 `/至少/.test`，所以它能正确判定「弹窗在、两个框都在」并
    //   继续往下走；只有**填值**这一侧炸掉。于是循环每次在第一个框上失败就
    //   `break`，第二个框永远没被填，值停在 `长度=[8,0,0]`，最后报成
    //   「登录后仍停在登录页（30s）」——**离真因隔了三跳**，看起来像登录坏了。
    //
    //   修法用 new RegExp 而不是把斜杠拼进字符串：既保持 JSON.stringify 的
    //   转义/防注入意图，类型又是对的。
    for (const which of ['至少', '再次']) {
      const f = await cdpEval(`(function(){
        try {
          var ins = Array.from(document.querySelectorAll('input'))
          var re = new RegExp(${JSON.stringify(which)})
          var el = ins.filter(function(x){ return re.test(x.placeholder||'') })[0]
          if (!el) return 'no-el'
          el.focus()
          return document.activeElement === el ? 'focused' : 'focus-failed'
        } catch (e) { return 'err:' + String(e).slice(0,80) }
      })()`)
      if (f !== 'focused') { console.log(`[preflight] ⚠️ focus 失败(/${which}/)：${f}`); break }
      await sleep(400)
      try {
        ash('input keyevent 123')
        for (let k = 0; k < 40; k++) ash('input keyevent 67')
        await sleep(250)
        inputText(master || '')
      } catch (e) {
        console.log(`[preflight] ⚠️ 系统级输入失败：${String(e?.message || e).split('\n')[0]}`)
        break
      }
      await sleep(700)
    }
    const lens = await cdpEval(`JSON.stringify(Array.from(document.querySelectorAll('input'))
        .filter(function(i){ return (i.type||'')==='password' })
        .map(function(e){ return e.value.length }))`)
    console.log(`[preflight] 主密码弹窗：两框已输入，长度=${lens}`)

    // ⚠️⚠️ 2026-10-05 修的真缺陷（本文件此前**从未点过确认按钮**）：
    //   下面 `if (masterDialogHandled)` 那段里有现成的 `b.click()`，
    //   但 `masterDialogHandled` **只在 `need === 'no'`（弹窗已经不在）时**才为 true。
    //   而弹窗**在场**的那条路径（need==='present' → 填两框）走完上面的循环后，
    //   标志仍是 false ⇒ 整段点击确认被跳过 ⇒ 弹窗永远开着 ⇒
    //   后面「等 hash 离开 #/login」30s 超时 ⇒ 报成「登录后仍停在登录页」。
    //
    //   现场（emulator-5554，Android 14）：
    //     填值前 [8,0,0] → 修好填值通路后 [8,8,8]，两个主密码框都拿到了值，
    //     截图里「确认」按钮是**激活态**（深蓝实心，不是 disabled）——
    //     也就是说**只差最后一下点击**，而这一步从来没被执行过。
    //     上一轮轮次日志里那行 `主密码弹窗确认：no-confirm` 是从**另一条路径**
    //     （need==='no'，即设备上已设过主密码）打出来的，
    //     **不能**当成「点击逻辑已验证可用」的证据。
    //
    //   ⇒ 判据从「弹窗是否还在」改成「本轮是否处理过弹窗」。
    masterDialogHandled = true
  }
  if (masterDialogHandled) {
    try { ash('input keyevent 111') } catch { /* 收键盘失败不致命 */ }
    await sleep(500)
    const ok = await cdpEval(`(function(){
      try {
        var b = Array.from(document.querySelectorAll('button'))
                 .filter(function(x){ return /^(确认|确定|OK|Confirm)$/.test((x.textContent||'').trim()) })[0]
        if (!b) return 'no-confirm'
        b.click(); return 'confirmed'
      } catch (e) { return 'err:' + String(e).slice(0,80) }
    })()`)
    console.log(`[preflight] 主密码弹窗确认：${ok}`)
    await sleep(2500)
  }
  // 等它真的离开登录页。点完立刻读会读到还没跳转的旧 hash。
  let landed = '(超时)'
  for (let i = 0; i < 30; i++) {
    await sleep(1000)
    const h = await cdpEval('location.hash')
    if (!/#\/login/.test(String(h || ''))) { landed = String(h); break }
  }
  const loggedIn = !/超时|读不到/.test(landed) && !/#\/login/.test(landed)
  console.log(loggedIn
    ? `[preflight] 登录成功，已进入 ${landed}`
    : `[preflight] ❌ 登录后仍停在登录页（30s），last hash=${landed}`)
  if (!loggedIn) {
    // 这里不查 POCKET_ALLOW_UNCERTAIN_START：登录失败就是失败，
    // 放行它只会把「跑不成」变成「跑成了但结论不可解读」。
    console.error('           不接受「先这样」——本轮所有断言都建立在已登录之上。')
    process.exit(3)
  }
  // 登录成功的判据不能是「hash 变了」——必须回到后端确认这个会话真的能用。
  // token 是 App 启动时 /api/auth/refresh 续期出来的，同为 291 字符但内容已变，
  // 比字符串毫无意义（见本文件上方说明）。这里读后端自己的判据。
  const probe = await cdpEval(`(function(){
    try {
      var t = localStorage.getItem('pocket_token') || '';
      return t.length;
    } catch (e) { return -1; }
  })()`)
  console.log(`[preflight] 登录后 App 内 token 长度=${probe} 字符（下一步由 flow 验后端是否接受）`)
  if (!(typeof probe === 'number' && probe > 100)) {
    console.error('           登录后本地没有像样的 token，登录没有真正落库。')
    process.exit(3)
  }
}
// 本地 SQLCipher 库要在**登录之后**才谈得上解锁：解锁屏的前提是「已有登录态」
// （页面上原话：「检测到已有登录态，但本地加密库未解锁」）。所以这一步必须排在
// 登录块之后；反过来的话守卫会先把你弹回登录页，解锁屏压根不出现。
//
// 旧的写法是把这个责任放在 _goto-pkm.yaml 里，用 `inputText: ${POCKET_MASTER}`。
// 那条**必然失效**：Maestro 把 ${...} 展开成字符串，变量不在它变量域里时就是
// 字面量 "undefined"（同 ${POCKET_DEV_PASS}，见 handoff §4.82.5 / §4.83.6）。
// POCKET_SKIP_CDP_LOGIN=1 表示「本轮就是来测登录屏的」，此时不该去解锁：
// 解锁屏的前提是「已有登录态」，没登录时那条路要么不出现、要么做了也白做，
// 而且它会把 App 从登录屏带走到别的页面，恰好毁掉本轮要测的起点。
if (process.env.POCKET_SKIP_CDP_LOGIN !== '1') {
  if (!(await ensureLocalDbUnlocked())) process.exit(3)
}

// ⚠️ 解锁会把 App 停在 #/pkm/today。起点路由是在**解锁之前**复位的，
//    所以这里必须再复位一次，否则 flow 的第一条断言就不可解释。
//    与 BUG-V8 同一个道理：必须制造真实的 hash 变化，守卫才会重算。
{
  const back = process.env.POCKET_START_ROUTE || '#/ai'
  await setRoute(`${back}?__afterunlock=${Date.now()}`, 'true', 8000)
  let h2 = '(读不到)'
  try { h2 = String(await cdpEval('location.hash') || '') } catch { /* 通道也坏了 */ }
  // ⚠️ 2026-10-02 实测踩到：POCKET_SKIP_CDP_LOGIN=1（有意不登录）时，
  //    守卫会把 #/ai 正确地弹回 #/login?returnTo=/ai?…，而 h2.includes('#/ai')
  //    为 false（那是 `returnTo=/ai`，没有 `#`）⇒ 被误判成「复位失败」。
  //    弹回登录页在「有意不登录」时恰恰是**正确**行为，不能与失败混为一谈。
  const skippedLogin = process.env.POCKET_SKIP_CDP_LOGIN === '1'
  const backOk = skippedLogin ? /#\/login/.test(h2) : h2.includes(back)
  console.log(backOk
    ? `[preflight] 已复位到起点 ${h2}${skippedLogin ? '（有意不登录，落在登录页即为正确结果）' : ''}`
    : `[preflight] ❌ 解锁后没能复位到起点，实际在 ${h2}`)
  if (!backOk && process.env.POCKET_ALLOW_UNCERTAIN_START !== '1') {
    console.error('           flow 的第一条断言就不可解释。先查起点路由为什么回不去。')
    process.exit(3)
  }
}


// --no-reinstall-driver 是这台机器上能不能跑通 Maestro 的关键：
// Maestro 2.11 **默认每次 test 之前都重装 driver**，而它的重装是「先卸载再安装」。
// MIUI 会拦下安装那一步，于是每跑一次就亲手把 driver 卸掉且装不回来，
// 下一轮继续卡在 installMaestroApks —— 破坏性循环（实测连踩三次，
// 分别卡在 installMaestroDriverApp / installMaestroServerApp）。
// 改成不重装，driver 由本脚本的 ensureDriver() 负责自愈。
// 注入给子进程前自检：只打长度，不打明文。
// 2026-10-01 13:40 实测踩到过「Maestro 把 ${POCKET_DEV_PASS} 展开成字符串
// "undefined"」，现场只留下一条 assert `^undefined$` 不成立，根因看不见。
// 这行让「变量到底传没传过去」一眼可见（口令本身仍不落 stdout）。
console.log(`[preflight] 注入子进程：POCKET_MASTER=${(process.env.POCKET_MASTER || 'PocketTest2026').length} 字符 / POCKET_DEV_PASS=${DEV_PASS.length} 字符`)

const SYSTEM_DIALOG_FLOW = '.maestro/_dismiss-system-dialogs.yaml'
// 每次 run 前面插一段 _dismiss-system-dialogs：MIUI 的一次性系统弹窗会在
// flow 中段抢前台（2026-10-01 12:29 实测：抢在 unlock 分支输主密码时，
// 把键盘输入吃掉，「解锁」恒 disabled），用 optional tap 清掉。
//
// ⚠️ 只在 MIUI 系上插。2026-10-03 在 vivo V2436A（Android 16 折叠屏）实测：
// 那两个弹窗是 Xiaomi 独有的，永不出现，于是每轮白白等 ~99s；更糟的是
// vivo 的后台清理在这段时间里把 App 杀掉了（实测跑完 preflight 后
// pidof 为空、焦点落到 com.vivo.browser），于是**真正要跑的 flow 一条都没开始
// 就已经死了**。等一个属于别的厂商的提示，代价是整轮回归。
// 「点不到就跳过」只有在这台机器真的会弹时才有意义。
const isMiui = (() => {
  try {
    const brand = `${adb(['shell', 'getprop', 'ro.product.brand'], 15000)}`
      + `${adb(['shell', 'getprop', 'ro.product.manufacturer'], 15000)}`.toLowerCase()
    const miui = adb(['shell', 'getprop', 'ro.miui.ui.version.name'], 15000)
    return /xiaomi|redmi|poco/i.test(brand) || String(miui).trim().length > 0
  } catch {
    return false
  }
})()
const preFlows = isMiui ? [SYSTEM_DIALOG_FLOW] : []
if (!isMiui) {
  console.log('[preflight] 非 MIUI 设备：跳过 _dismiss-system-dialogs（那些弹窗是 Xiaomi 独有的）')
}
// --no-reinstall-driver 原本是**每台机器都必须开**的：Mixture 为它每次 test
// 之前都重装 driver，而它的重装是「先卸载再安装」，MIUI 会拦下安装那一步，
// 于是每跑一次就亲手把 driver 卸掉且装不回来（实测连踩三次）。
//
// 2026-10-03 vivo V2436A 实测到另一件事，方向相反：这里**不能**让 Maestro 重装。
// 退化的是**长连接上的设备服务器**，不是设备、不是驱动包、不是我们的页面
// （同一台机器跑原生 App、以及把 WebView 停在 about:blank，都是 0 失败）。
// 而「重装」这条路在 vivo 上根本走不通：Maestro 的重装是 uninstall+install，
// install 会拉起 vivo「安全守护」安装框，框上有前置勾选「已了解应用的风险检测结果」，
// 不勾则确认键 disabled —— **Maestro 自己不会点这个勾**，于是它内部那条
// `adb install` 必然挂到超时。2026-10-03 实测两轮都是 150s TIMEOUT 且
// 一行弹窗日志都没有（那是因为 device-install-preflight 的 dialogShowing()
// 只读第一条 mCurrentFocus=，在 OriginOS 上读到 "null"，永远看不见弹窗）。
//
// ⇒ 两个机型都不重装 driver，区别只在「怎么拿一个干净的设备服务器」：
//   MIUI    ：不重装、也不 force-stop（卸载重装会毁掉 driver，见上）
//   非 MIUI ：不重装、但每轮 force-stop driver 进程（包还在，Maestro 会重新拉起）
const reinstallDriver = process.env.POCKET_REINSTALL_DRIVER === '1'
const driverFlag = reinstallDriver ? [] : ['--no-reinstall-driver']
if (reinstallDriver) {
  console.log('[preflight] POCKET_REINSTALL_DRIVER=1：让 Maestro 每轮重装 driver')
}
// 每次 run 之前把 maestro 的 driver/server 进程杀掉，给这一轮一个干净的设备服务器。
//
// 为什么必须：2026-10-03 vivo V2436A 实测到 device server 是**跨轮存活**的，
// 而 gRPC 通道的连接年龄一路涨：31s → 92s → 126s → 152s → 311s。年龄越大
// viewHierarchy 越容易挂满 120s：
//   DeviceServerDiedException ... 'viewHierarchy' (120115ms since last byte,
//   connection age 126190ms) DEADLINE_EXCEEDED
// 也就是说「设备服务器死了」多数时候不是被系统杀掉，而是**上一轮留下的那个还在
// 跑、状态已经劣化**。实测每轮都杀掉 driver 后，同一轮里前两次 dump 从
// 15~25s 降到 0.8s。
//
// 只 force-stop、**不卸载**：包还在原地，Maestro 靠 --no-reinstall-driver 直接
// 把 instrumentation 重新拉起来，既拿到干净连接，又不碰那条会被 vivo 安装框
// 拦死的 install 路径。
//
// 为什么放在 preflight（只对非 MIUI）：MIUI 上卸载重装 driver 就是那个
// 「装不回来」的破坏性循环（见上面 --no-reinstall-driver 的注释），那边反而不
// 能动它，靠 ensureDriver() 的 pm enable + 重试来救。
if (!isMiui) {
  for (const p of DRIVER_PKGS) {
    try {
      adb(['shell', 'am', 'force-stop', p], 30000)
      console.log(`[preflight] 已 force-stop ${p}（避免复用上一轮已劣化的设备服务器）`)
    } catch { /* 没装或已经不在 */ }
  }
  // 给系统一点时间回收，再让 Maestro 重新装
  await sleep(1200)
}

// ⚠️ 多 flow 必须**逐条跑**，每条之前用 CDP 把 App 复位到起点路由。
//
// 2026-10-03 真机全量套件实测（Xiaomi 2411DRN47C）：
//   一次 `maestro test flowA flowB …` 会把所有 flow 跑在同一次调用里，
//   而 App 的复位只发生在 preflight（整批一次）。于是第 2 条及以后的 flow
//   继承的是**上一条 flow 的结束页面**：
//     · notes-crud 结束在 PKM 页 → tasks-crud 第一条 `visible: "AI 工具"` 45s 超时；
//     · flashcards-write 结束在闪卡复习页 → settings-llm-gateway 首条断言失败。
//   对照：tasks-crud **单跑 2/2 通过（41s）** ⇒ 产品没坏，坏的是「起点假设」。
//
// 为什么不在 flow 里点「首页」tab 解决（试过，不可靠）：
//   底部导航**不是全局常驻**。实测 router-mobile.ts 里 /flashcards、/flashcards/decks/:id
//   等子路由 `bottomNav: false`；探针在 #/flashcards 上量到 `nav: []`。
//   从 #/flashcards/decks/:id 退回「有底部导航」的页面需要**两次** back ——
//   而需要几次取决于上一条 flow 恰好停在哪，写死次数就是把一个已知缺陷
//   换成另一个。所以复位放在 harness，用 CDP 直接改路由，不靠 UI 导航猜。
const START_ROUTE = process.env.POCKET_START_ROUTE || '#/ai'

// POCKET_PRE_ROUTE：每条 flow 开始前用 **CDP** 把路由切到指定页面。
//
// ## 它是什么
//
// 一个**诊断用逃生口**，不是修复。它的用途只有一个：把「导航没发生」和
// 「页面坏了」这两种红法区分开 —— 前者用本功能把页面送到位，如果 flow 随即
// 转绿，那问题在导航链路；仍然红，那问题在页面里。
//
// ## 观察到的现象（不是结论）
//
// 底部 tabbar 的 `tapOn` 在这台设备上**有时**报 COMPLETED 而页面没动，
// `retryTapIfNoChange` 也不触发。`_goto-pkm.yaml` 注释里记过两次同样观察。
// 另有一条更硬的约束：flow 内部**无法**用脚本改 hash 绕开它 ——
//
//	evalScript: ${location.hash = '#/more'}
//	  → TypeError: Cannot set property 'hash' of undefined
//	    （evalScript **不在 WebView 的 JS 上下文里**跑）
//
// 而 harness 这一侧有 CDP（lib/adb-cdp.mjs 的 ev，**是**在 WebView 上下文里）。
// 实测 scripts/probe-cdp-route.mjs：设 location.hash='#/more' → 页面内容真的
// 切成「更多功能 / 学习 / 对话 / 会议 / 邮箱 / 定时自动化 / 闪卡 / 设置 …」。
//
// ## ⚠️ 归因更正（2026-10-04）：别把这个当「MIUI 吞 tap」的证据
//
// 2026-10-04 早先那轮，`email-accounts` / `flashcards-write` 都红在
// 「找不到目标页」，当时记下的归因是「MIUI 吞掉底部 tab tap」。
// **这个归因是错的**，用对照实验推翻了：
//
//   同时做了两件事 —— ① 打开 POCKET_PRE_ROUTE；② 清掉首页 7 条
//   自造的测试探针任务（Maestro任务×5 / PG matrix probe×2），它们此前把
//   首页的「需要你介入」面板占满。
//
//	关掉 POCKET_PRE_ROUTE、清完探针后，email-accounts **2/2 通过（47s）**。
//
// ⇒ 真正的原因是**测试残留数据把导航区盖住了**（tap 落在面板上，
// 不是被系统吞掉），不是设备级的 MIUI bug。两件事一起动过、只按「开/关
// pre-route」归因，就会把绕过手段误当成修复，并把错误结论写进注释传播出去。
// 记这一条是因为这正是本注释上一版的错误。
//
// ## 为什么必须自证「真的到了」
//
// setRoute 只能证明 **hash 变了**，不能证明**页面切了**。所以这里额外读一次
// body.innerText，并用 POCKET_PRE_ROUTE_MARK（正则）判「确实在目标页」。
// 负控实测：MARK 填一个绝不可能出现的字符串 → 守卫响亮报「页面自证失败」
// 并打印页面内容前 220 字。
const PRE_ROUTE = (process.env.POCKET_PRE_ROUTE || '').trim()
const PRE_ROUTE_MARK = (process.env.POCKET_PRE_ROUTE_MARK || '').trim()

async function gotoPreRoute() {
  if (!PRE_ROUTE) return
  const sep = PRE_ROUTE.includes('?') ? '&' : '?'
  const want = `${PRE_ROUTE}${sep}__preroute=${Date.now()}`
  const ok = await setRoute(want, 'true', 8000)
  let h = '(读不到)'
  let body = ''
  try {
    h = String(await cdpEval('location.hash') || '')
    body = String(await cdpEval('document.body.innerText') || '').replace(/\s+/g, ' ')
  } catch { /* 通道也坏了；下面的 mark 判据会报出来 */ }
  const line = `[pre-route] ${ok ? '✅' : '⚠️ '} ${PRE_ROUTE} → ${h}`
  if (PRE_ROUTE_MARK) {
    const re = new RegExp(PRE_ROUTE_MARK)
    if (re.test(body)) {
      console.log(`${line} · 页面自证通过（/${PRE_ROUTE_MARK}/ 命中）`)
      return
    }
    console.error(`${line} · ❌ 页面自证**失败**：/${PRE_ROUTE_MARK}/ 没命中。`)
    console.error(`   页面内容前 220 字：${body.slice(0, 220)}`)
    console.error(`   ⇒ flow 很可能仍会红在「找不到目标页」。这不是 flow 的问题，是导航没到位。`)
    return
  }
  console.log(`${line} · 页面内容前 120 字：${body.slice(0, 120)}`)
}

/** 每条 flow 之前的复位：造一次真实 hash 变化，让路由守卫重算。 */
async function resetToStart() {
  const want = `${START_ROUTE}?__reflow=${Date.now()}`
  const ok = await setRoute(want, 'true', 8000)
  let h = '(读不到)'
  try { h = String(await cdpEval('location.hash') || '') } catch { /* 通道也坏了 */ }
  console.log(`[per-flow] ${ok ? '✅' : '⚠️ '} 复位到起点 ${h}`)
}

const maestroEnv = {
  ...process.env,

  POCKET_DEV_PASS: DEV_PASS, // 只进子进程 env

  // 本地 SQLCipher 主密码是测试装置上本会话约定的值，不是仓库内推导出来的。
  // 仍然只经 env 传递，避免出现在 flow 文件里。
  POCKET_MASTER: process.env.POCKET_MASTER || 'PocketTest2026',
  JAVA_HOME: resolveJavaHome() || process.env.JAVA_HOME,
  MAESTRO_CLI_NO_ANALYTICS: 'true',

  // 强制 JVM 按 UTF-8 输出。2026-10-04 实测踩到：Windows 上 JVM 跟随系统
  // ANSI 代码页（GBK）输出，中文断言在失败信息里全变成 U+FFFD ——
  // `[Failed] xxx (Assertion is false: "?????" is visible)`，码点全是 fffd，
  // **哪个断言红了根本读不出来**，只能靠反复跑 + 猜哪个元素没出现。
  // 本轮就因为它把「红在 更多功能 / 定时自动化 / 仅显示启用 / 返回」这四种
  // 完全不同的失败压成了同一串问号，绕了好几轮才定位到真因。
  //
  // 追加而非覆盖：调用方（run-maestro.ps1 等）可能已经带了别的 JVM 参数。
  JAVA_TOOL_OPTIONS: `${process.env.JAVA_TOOL_OPTIONS ?? ''} -Dfile.encoding=UTF-8 -Dsun.stdout.encoding=UTF-8 -Dsun.stderr.encoding=UTF-8`.trim(),
}

// ---- 夹具（2026-10-04）----------------------------------------------------
//
// 有些 flow 的判据依赖一个**前提**，前提不成立时它会恒红，于是等于没有护栏。
// notes-stt-error-visibility 就是这种：ASR 开通后后端不再返回
// `stt_unavailable:`，而它的断言正是围绕这条带错误码的可行动原因。
//
// 声明写在 scripts/.maestro-flows.json 的 `fixture` 字段上，脚本路径**由名字
// 推导**（`fixture: stt-error` ⇒ `scripts/stt-error-fixture.mjs`），不另建
// 映射表 —— 两边各写一份就会出现「checker 放行的名字 harness 认不出」。
//
// ⚠️ 还原必须挂在 finally 上，不能是「flow 通过之后」：
//   夹具把 STT 的 gatewayModel 指到一个不存在的模型，留在库里就等于
//   **用户的语音转写是坏的**。flow 失败恰恰是最需要还原的时刻。
const fixtureFor = (() => {
  const map = new Map()
  let list = []
  try {
    const raw = JSON.parse(readFileSync(resolve(ROOT, 'scripts', '.maestro-flows.json'), 'utf8'))
    list = Array.isArray(raw) ? raw : (raw?.flows ?? [])
  } catch (e) {
    console.error(`[fixture] ⚠️ 读不到 scripts/.maestro-flows.json（${e.message}），本轮不跑任何夹具。`)
    return map
  }
  for (const e of list) {
    if (!e || typeof e !== 'object' || !e.file) continue
    if (e.fixture) map.set(String(e.file), { kind: 'fixture', name: String(e.fixture) })
    else if (e.reset) map.set(String(e.file), { kind: 'reset', name: String(e.reset) })
  }
  return map
})()

/** 配置里标了 kind=subflow 的流 —— 它们只能被父流 runFlow 引用。 */
const subFlows = new Set((() => {
  const out = new Set()
  try {
    const raw = JSON.parse(readFileSync(resolve(ROOT, 'scripts', '.maestro-flows.json'), 'utf8'))
    for (const e of (Array.isArray(raw) ? raw : (raw?.flows ?? []))) {
      if (e && typeof e === 'object' && e.kind === 'subflow' && e.file) out.add(String(e.file))
    }
  } catch { /* 配置读不到就当没有子流，不阻断 */ }
  return out
})())

const fixtureScript = (name) => resolve(ROOT, 'scripts', `${name}-fixture.mjs`)

/** 跑夹具/清场的一步。mode 为 undefined 时表示「不带参数跑」（清场用）。 */
function runFixture(name, mode) {
  const script = fixtureScript(name)
  if (!existsSync(script)) {
    console.error(`[fixture] ❌ 找不到夹具脚本 ${script}`)
    return false
  }
  const args = mode ? [script, mode] : [script]
  const r = spawnSync(process.execPath, args, { cwd: ROOT, stdio: 'inherit', env: maestroEnv })
  const how = mode ? `${name} ${mode}` : `${name}（清场，无参数）`
  if (r.status !== 0) {
    console.error(`[fixture] ❌ ${how} 失败（退出码 ${r.status}）`)
    return false
  }
  console.log(`[fixture] ✅ ${how}`)
  return true
}

const runOne = (flow) =>
  spawnSync(MAESTRO, ['--device', DEVICE, 'test', ...driverFlag, ...preFlows, flow], {
    cwd: ROOT,
    stdio: 'inherit',
    shell: true,
    env: maestroEnv,
  })

/**
 * 每条流之前确认 App 还活着；死了就按 preflight 已验证的路径拉回来。
 *
 * 2026-10-04 真机实测（sttdev 批次）：第 1 条流失败后，第 2 条流开跑前的
 * `resetToStart()` 里 `adb shell pidof <包名>` 返回空，adb() 直接抛异常，
 * 整个 node 进程带栈崩掉 —— 后果有三条，缺一不可：
 *   ① 第 2 条及以后的 flow **一条都没跑**，而报告里只看到第 1 条的红；
 *   ② 末尾那段「App 被强杀 / MIUI wakepath」归因逻辑**永远执行不到**
 *      （它在循环之后，进程已经死了），于是最该被说清的原因被吞掉；
 *   ③ 退出码来自未捕获异常，看不出是「App 掉了」还是「harness 坏了」。
 *
 * ⇒ 判据必须自己长出这个分支：App 不在 → 拉回来 → 拉不起来就**响亮退出**。
 *   沉默崩掉比红更有害：它让「没跑」看起来像「跑过了」。
 */
async function ensureAppAlive() {
  const pid = () => {
    try { return adb(['shell', 'pidof', PKG], 15000).trim() } catch { return '' }
  }
  if (pid()) return true

  console.error(`\n[per-flow] ⚠️  ${PKG} 进程不在了（pidof 为空），正在重新拉起…`)
  try {
    adb(['shell', 'monkey', '-p', PKG, '-c', 'android.intent.category.LAUNCHER', '1'], 30000)
  } catch (e) {
    console.error(`        启动意图发不出去：${String(e.message || e).split('\n')[0]}`)
  }
  // 给它一点时间自己起来；不 blind wait 是因为下一步还要回读 pidof 自证。
  for (let k = 0; k < 20; k++) {
    await sleep(1500)
    if (pid()) { console.error(`[per-flow] ✅ ${PKG} 已重新拉起，继续跑后面的 flow`); return true }
  }

  console.error(`[per-flow] ❌ 拉起 ${PKG} 失败，剩余 ${flows.length - i - 1} 条 flow 都不会跑。`)
  console.error('        典型成因（MIUI 真机实测）：flow 里的 launchApp 会先 force-stop，')
  console.error('        而随后的 start 被 com.miui.securitycenter 的 wakepath 确认框拦下，')
  console.error('        App 从此回不到前台。规避：flow 里不要写 launchApp（preflight 已经')
  console.error('        把它拉起来了），或在「设置 → 应用管理 → 授权管理 → 后台弹出界面」')
  console.error('        里放行本 App。')
  console.error('        ⚠️ 剩下的 flow 是**没跑**而不是「跑过且失败」，别把这份结果当成全绿。')
  return false
}

let r = { status: 0 }
for (const [i, flow] of flows.entries()) {
  if (subFlows.has(flow)) {
    // 独立跑子流必然红在一个**本就不该红**的断言上，而且失败点离根很远：
    // `_set-master-password` 独立跑会在「确认」按钮上 Element not found，
    // 因为它假定的「创建主密码」弹窗只在**从没有过主密码**时存在；
    // 设备上早就设过了。那句报错与「产品坏了」「选择器写错」长得一模一样。
    console.error(`[subflow] ❌ ${flow} 是子流，不能独立运行。`)
    console.error('          它只被父流在 `runFlow: when: visible: …` 条件下引用。')
    console.error('          要验它，请跑引用它的父流。')
    process.exit(1)
  }
  if (!(await ensureAppAlive())) process.exit(1)
  if (i > 0) await resetToStart()
  await gotoPreRoute()
  const fx = fixtureFor.get(flow)
  if (fx) {
    // 前提没造出来就不能跑 flow：那会让这条流红在一个「本就不该红」的原因上，
    // 而失败点离根隔得很远（页面只显示泛化提示 / 空态不出现）。宁可响亮退出。
    if (fx.kind === 'reset') {
      // 清场没有「还原」：它的产物就是 flow 的起点（零卡组之类）。
      // 失败时也不用还原 —— 它本来就是「清到空」。
      if (!runFixture(fx.name, undefined)) {
        console.error('[fixture] ❌ 清场失败，跳过本条 flow（场景没回到起点，它会测成另一条分支）')
        process.exit(1)
      }
    } else if (!runFixture(fx.name, '--induce')) {
      // 反过来仍要尝试还原：induce 可能改了一半。
      console.error('[fixture] ❌ induce 失败，跳过本条 flow（前提不成立时它必红，不是产品回归）')
      runFixture(fx.name, '--restore')
      process.exit(1)
    }
  }
  if (flows.length > 1) console.log(`\n──────── flow ${i + 1}/${flows.length}: ${flow} ────────`)
  let one = { status: 0 }
  try {
    one = runOne(flow)
  } finally {
    // 无论 flow 绿还是红都还原，见上面「还原必须挂在 finally 上」。
    // reset 不进这个分支 —— 它的「还原」等于把 flow 的产出删掉。
    if (fx?.kind === 'fixture') runFixture(fx.name, '--restore')
  }
  if (one.status !== 0) r = one   // 保留失败那次的返回码，交给下面的归因逻辑
}
if (flows.length > 1) console.log(`\n[suite] ${flows.length} 条 flow 跑完`)
// ── 失败归因：把「App 被人/被系统杀掉」和「flow 断言不成立」分开 ──────────
// 2026-10-02 21:53 实测踩到的链条：Maestro 的 launchApp 会先 force-stop 再 start，
// force-stop 把 App 杀掉，而 start 被 MIUI 的 wakepath（后台弹出/自启动）确认框拦下，
// App 再也不回前台 ⇒ launcher 停在最近任务视图 ⇒ flow 第一条断言必然超时。
// 现象离真因隔了两跳（recents ← App 没起来 ← start 被拦），不指名报出来就得重查三轮。
if (r.status !== 0) {
  let diag = ''
  try { diag = adb(['logcat', '-d', '-t', '400'], 30000) } catch { /* logcat 不可用 */ }
  const killed = diag.split(/\r?\n/).filter((l) => /Force stopping ${PKG}|Killing \d+:${PKG}/.test(l)).slice(-2)
  const wakepath = diag.includes('wakepath') || diag.includes('ConfirmStartActivity')
  if (wakepath) {
    console.error('[归因] ❌ MIUI wakepath（后台弹出/自启动）拦下了 App 的启动')
    console.error('        App 被 force-stop 后，start 被 com.miui.securitycenter/…ConfirmStartActivity 拦住，')
    console.error('        于是再也没进前台；屏幕上看到的是最近任务视图，于是第一条断言必然超时。')
    console.error('        这**不是**产品缺陷，也不是 flow 写错了。')
    console.error('        规避：不要在 flow 里用 launchApp（preflight 已经把 App 拉起来了）；')
    console.error('        或在设备「设置 → 应用设置 → 应用管理 → 授权管理 → 后台弹出界面」里放行本 App。')
  }
  if (killed.length) {
    console.error('[归因] 本轮期间 App 进程被强杀过（这会让所有后续断言失去意义）：')
    for (const l of killed) console.error('        ' + l.trim().slice(0, 160))
  }
}
process.exit(r.status ?? 1)
