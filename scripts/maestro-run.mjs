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
      const r = spawnSync(process.execPath, [resolve(ROOT, 'scripts/adb-install-confirm.mjs'), apk],
        { cwd: ROOT, encoding: 'utf8', timeout: 180000 })
      if (have(pkg)) { ok = true; adb(['shell', 'pm', 'enable', pkg], 30000); break }
      console.log(`[driver] ${pkg} 第 ${attempt} 次安装未成功`)
    }
    if (!ok) { console.error(`[driver] ${pkg} 装不上，MIUI 仍在拦截`); return false }
    try { adb(['shell', 'pm', 'enable', pkg], 30000) } catch { /* 装上就是 enabled */ }
    console.log(`[driver] ${pkg} 就位`)
  }
  return true
}

async function preflight() {
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
      return true
    }
  }
  console.error('[preflight] App 60s 内未进入前台，中止')
  return false
}

const src = readFileSync(GO, 'utf8')
const m = src.match(/devPass\s*=\s*"([^"]+)"/)
if (!m) {
  console.error('未能从后端源码定位 dev 口令常量，拒绝以明文兜底')
  process.exit(2)
}

if (!(await preflight())) process.exit(3)

// --no-reinstall-driver 是这台机器上能不能跑通 Maestro 的关键：
// Maestro 2.11 **默认每次 test 之前都重装 driver**，而它的重装是「先卸载再安装」。
// MIUI 会拦下安装那一步，于是每跑一次就亲手把 driver 卸掉且装不回来，
// 下一轮继续卡在 installMaestroApks —— 破坏性循环（实测连踩三次，
// 分别卡在 installMaestroDriverApp / installMaestroServerApp）。
// 改成不重装，driver 由本脚本的 ensureDriver() 负责自愈。
const args = ['--device', DEVICE, 'test', '--no-reinstall-driver', ...flows]
const r = spawnSync(MAESTRO, args, {
  cwd: ROOT,
  stdio: 'inherit',
  shell: true,
  env: {
    ...process.env,
    POCKET_DEV_PASS: m[1], // 只进子进程 env
    // 本地 SQLCipher 主密码是测试装置上本会话约定的值，不是仓库内推导出来的。
    // 仍然只经 env 传递，避免出现在 flow 文件里。
    POCKET_MASTER: process.env.POCKET_MASTER || 'PocketTest2026',
    JAVA_HOME: process.env.JAVA_HOME || 'C:\\Program Files\\Eclipse Adoptium\\jdk-21.0.12.101-hotspot',
    MAESTRO_CLI_NO_ANALYTICS: 'true',
  },
})
process.exit(r.status ?? 1)
