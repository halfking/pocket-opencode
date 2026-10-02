// 真机安装 preflight —— 让 `adb install` 在 HyperOS/MIUI 上不再需要人工确认。
//
// ── 为什么需要这个脚本 ──
//
// HyperOS 会在**全新安装**（包未在设备上）时拉起
//   com.miui.securitycenter/com.miui.permcenter.install.AdbInstallActivity
// 没人点就超时，报 INSTALL_FAILED_USER_RESTRICTED: Install canceled by user，
// 自动化流程整个挂住。本脚本把「装之前把机器调好」和「要弹就自动点掉」
// 合成一条命令（方案 C）。
//
// ── 三条必须记住的实测结论（都是负控/对照实验打出来的）──
//
// **① 更新路径与全新安装是两条不同的路径，结论不能互相外推。**
// `adb install -r` 覆盖一个**已装**的包，实测**根本不弹窗**、直接 Success。
// 而卸载后装一个**未装过**的包（实测用 maestro-server.apk）**必弹**且
// 不点就 USER_RESTRICTED。Maestro 每次开会话都先 uninstall 再 install driver，
// 所以它撞的一直是后一条路径。
// ⚠️ 只在 `-r` 更新路径上测，会得出"这台机根本不弹窗"的错误结论——
// 那是路径巧合，不是门被关上了。
//
// **② 没有任何 `settings` 键能关掉这个弹窗（四个候选全部证伪）。**
// 在**真会弹的路径**上逐个实测，每个都回读确认写入成功：
//   global.adb_install_need_confirm      = 0  -> 仍弹窗、仍 FAILED
//   global.verifier_verify_adb_installs  = 0  -> 仍弹窗、仍 FAILED
//   global.package_verifier_enable       = 0  -> 仍弹窗、仍 FAILED
//   secure.install_non_market_apps       = 1  -> 仍弹窗、仍 FAILED
//   global.adb_install_enable            = 0  -> 仍弹窗、仍 FAILED
// logcat 显示放行走的是 (BAL_ALLOW_ALLOWLISTED_UID)，且 Activity 是
// visibleRequested:false；真正的门在 /data/user/0/com.miui.securitycenter
// 私有存储里，**无 root 读不到**（实测 Permission denied）。
// ⇒ 本脚本**不宣称关掉了弹窗**，只做「弹了就点掉」这一件被证实有效的事。
//
// **③ 自动点确认键是有效的，且已用对照实验钉死因果。**
// 装 maestro-server.apk：弹窗出现 → 定位到「继续安装」@207,1498 → 点击
// → install Success，且 `pm list packages` 确认 dev.mobile.maestro.test
// 真的装上了。紧接着卸载重装做反向对照：仍弹窗、不点即 USER_RESTRICTED。
// ⇒ 成功是"点了"带来的，不是"本来就能装"。
// （注：2026-10-02 一份 handoff 记着"自动点连点 2 次仍被系统撤销"——
//  本轮实测与该记录相反，以本脚本的对照实验为准。）
//
// 弹窗分支刻意**不盲发回车**：MIUI 那个框的默认焦点在「取消」上，
// 盲回车等于点"拒绝"。必须先 dump 出层次结构、按文案/资源 id 定位到
// 真正的确认键再点；定位不到就如实打印"不盲点"并继续等，不乱按。
//
// 用法：
//   node scripts/device-install-preflight.mjs            # 只体检 + 调优，不装
//   node scripts/device-install-preflight.mjs <apk>      # 体检后直接装
//
// 环境变量：
//   POCKET_SERIAL  设备序列号（默认 192.168.31.19:5555）
import { execFileSync, spawn } from 'node:child_process'
import { existsSync } from 'node:fs'
import { homedir } from 'node:os'

// adb 位置同样不能写死：原值是某台 Windows 开发机的
// C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe，在别的宿主上
// 直接 `[FAIL] adb 不存在` —— 而这条报错的措辞把矛头指向 adb，实际是本脚本
// 绑死了一台机器。改为 env 覆盖 -> PATH -> 常见安装位置。
function whichFirst(cands) {
  for (const c of cands) {
    if (!c) continue
    if (c.includes('/') || c.includes('\\')) {
      if (existsSync(c)) return c
      continue
    }
    const r = execFileSync(process.platform === 'win32' ? 'where' : 'which', [c],
      { encoding: 'utf8', stdio: ['ignore', 'pipe', 'ignore'] })
    const first = (r.stdout || '').split(/\r?\n/).map((s) => s.trim()).filter(Boolean)[0]
    if (r.status === 0 && first) return first
  }
  return null
}

const IS_WIN = process.platform === 'win32'
const ADB = process.env.POCKET_ADB
  || process.env.POCKET_ADB_BIN
  || whichFirst([
    IS_WIN ? 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe' : null,
    `${homedir()}/bin/adb`,              // arm64 包装脚本：自己设 LD_LIBRARY_PATH
    `${homedir()}/Android/Sdk/platform-tools/adb`,
    `${homedir()}/tools/android-sdk/platform-tools/adb`,
    'adb',
  ])
  || 'adb'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const APK = process.argv[2]
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const adb = (args, t = 120000) =>
  execFileSync(ADB, ['-s', S, ...args], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

// MIUI 弹窗的特征串：Activity 名 + 常见的按钮文案
const DIALOG_MARKERS = [
  'AdbInstallActivity',
  'com.miui.permcenter.install',
  'com.miui.securitycenter',
]

function log(...a) { console.log('[preflight]', ...a) }

// ── 1. 设备在线 ────────────────────────────────────────────────────────────
function requireDevice() {
  // 只有「带路径分隔符的名字」才能用 existsSync 判存在。裸命令名（'adb'，
  // 靠 PATH 解析）用 existsSync 判必然为 false——那是相对于 CWD 找同名文件，
  // 于是即便 `which adb` 明明能找到，也会被报成「adb 不存在」。2026-10-03 实测。
  const isPathLike = ADB.includes('/') || ADB.includes('\\')
  if (isPathLike && !existsSync(ADB)) {
    console.error(`[FAIL] adb 不存在: ${ADB}`)
    process.exit(1)
  }
  let line = ''
  try { line = adb(['devices'], 20000) } catch (e) { console.error('[FAIL] adb devices 失败: ' + e.message); process.exit(1) }
  const mine = line.split(/\r?\n/).find((l) => l.includes(S)) || ''
  if (!/\sdevice\s*$/.test(mine)) {
    console.error(`[FAIL] 设备不在线: ${mine || '(未找到 ' + S + ')'}`)
    console.error('  必须在真机上操作：确认 WiFi 仍连着，重开一次无线调试并回报新端口。')
    process.exit(2)
  }
  log(`设备在线 ${S}`)
}

/** 当前前台窗口是否就是 MIUI 安装确认框。 */
function dialogShowing() {
  try {
    const w = adb(['shell', 'dumpsys', 'window'], 30000)
    const focus = (w.match(/mCurrentFocus=(.*)/) || [])[1] || ''
    return DIALOG_MARKERS.some((m) => focus.includes(m)) ? focus.trim() : ''
  } catch { return '' }
}

// ── 2. 把「尽量不弹」的那几个开关推到不弹方向 ───────────────────────────────
//
// 逐条 apply，但**不宣称它们一定生效** —— 见文件头实测结论。
// 每条都回读确认写入成功（写不进去的权限错误要如实报，不能当成功）。
function tuneInstallSwitches() {
  const puts = [
    ['global', 'adb_install_need_confirm', '0'],
    ['global', 'verifier_verify_adb_installs', '0'],
    ['global', 'package_verifier_enable', '0'],
    ['secure', 'install_non_market_apps', '1'],
  ]
  let ok = 0
  for (const [ns, key, val] of puts) {
    try {
      adb(['shell', 'settings', 'put', ns, key, val], 20000)
      const back = adb(['shell', 'settings', 'get', ns, key], 20000).trim()
      if (back === val) { ok++; log(`开关 ${ns}.${key}=${val} 已写入并回读确认`) }
      else log(`开关 ${ns}.${key} 回读=${back}（期望 ${val}），未生效但不影响后续判定`)
    } catch (e) {
      log(`开关 ${ns}.${key} 写入异常: ${e.message.split('\n')[0]}`)
    }
  }
  log(`开关写入 ${ok}/${puts.length} 条（仅为「尽量不弹」，真判据是下面的实测安装）`)
}

// ── 3. 真装一次，边装边看弹不弹；要弹就点掉 ────────────────────────────────
async function installWatching(apk) {
  const child = spawn(ADB, ['-s', S, 'install', '-r', '-g', apk], { stdio: ['ignore', 'pipe', 'pipe'] })
  let out = ''
  let done = false
  let code = null
  child.stdout.on('data', (d) => { out += d })
  child.stderr.on('data', (d) => { out += d })
  // 判据的坑：`exit` 事件可能早于最后一批 `data` 到达，此时 out 还是空的
  // （实测踩过：安装其实 FAILED，却因为 out 没读到而误报"静默安装通过"）。
  // 所以用 `close`（所有 stdio 流都关完）而不是 `exit` 作为完成信号。
  const finished = new Promise((resolve) => {
    child.on('close', (c) => { done = true; code = c; resolve(c) })
    child.on('error', (e) => { done = true; out += `\n[spawn error] ${e.message}`; code = -1; resolve(-1) })
  })

  const deadline = Date.now() + 120000
  let dialogSeen = ''
  let tapped = 0

  while (Date.now() < deadline && !done) {
    await sleep(1000)
    const focus = dialogShowing()
    if (!focus) continue
    if (!dialogSeen) { dialogSeen = focus; log('检测到安装确认弹窗，尝试自动确认') }

    // uiautomator dump 找确认键。取不到就退化成「按回车」——
    // MIUI 的确认框默认焦点在「取消」上，所以**不能**盲发回车，
    // 必须先拿到层次结构定位到真正的确认键再点。
    let dump = ''
    try {
      adb(['shell', 'uiautomator', 'dump', '/sdcard/pf_ui.xml'], 25000)
      dump = adb(['shell', 'cat', '/sdcard/pf_ui.xml'], 25000)
    } catch { continue }
    if (!dump || !DIALOG_MARKERS.some((m) => dump.includes(m))) continue

    const btns = [...dump.matchAll(/text="([^"]*)"[^>]*resource-id="([^"]*)"[^>]*bounds="\[(\d+),(\d+)\]\[(\d+),(\d+)\]"/g)]
      .map((m) => ({ text: m[1], id: m[2], cx: (+m[3] + +m[5]) / 2, cy: (+m[4] + +m[6]) / 2 }))
    const hit = btns.find((b) => /^(安装|继续安装|确定|允许|继续)$/.test(b.text.trim()))
      || btns.find((b) => /install_yes|btn1|button1|positive/i.test(b.id))
    if (!hit) { log('弹窗在但没定位到确认键（不盲点，避免误触「取消」）'); continue }

    adb(['shell', 'input', 'tap', String(hit.cx), String(hit.cy)], 20000)
    log(`已点击确认键 "${hit.text}" @${hit.cx},${hit.cy}`)
    tapped++
    await sleep(1500)
  }

  // 循环退出有两种可能：装完了，或 120s 到期。前者等 close 事件把输出收齐；
  // 后者不能就这么报"静默通过"——必须如实标成超时。
  let timedOut = false
  if (!done) {
    const race = await Promise.race([
      finished.then(() => false),
      sleep(30000).then(() => true),
    ])
    timedOut = race
    if (timedOut) {
      log('等待 120s+30s 后安装仍未结束，判定为超时（不是"静默通过"）')
    }
  }
  const tail = out.trim().split(/\r?\n/).filter(Boolean).slice(-4).join(' | ')
  const success = !timedOut && /Success/i.test(out)
  return { success, code, dialogSeen, tapped, tail, timedOut }
}

requireDevice()
log('--- 阶段 1/2：调整安装确认相关开关 ---')
tuneInstallSwitches()

if (!APK) {
  log('--- 未给 APK 参数，体检结束。')
  log('要连安装一起跑：node scripts/device-install-preflight.mjs <apk路径>')
  process.exit(0)
}
if (!existsSync(APK)) { console.error(`[FAIL] APK 不存在: ${APK}`); process.exit(1) }

log('--- 阶段 2/2：真实安装并观测是否弹窗 ---')
const r = await installWatching(APK)
log(`install 结果: ${r.timedOut ? 'TIMEOUT' : r.success ? 'Success' : 'FAILED(exit=' + r.code + ')'} | ${r.tail}`)
// 「没看到弹窗」只有在真的装成功时才算静默通过；失败/超时时如实说明观测无效，
// 不能因为没观测到弹窗就报一条让人以为一切正常的话。
if (r.timedOut) {
  log('安装超时，未能完成观测')
} else if (!r.success) {
  log(r.dialogSeen
    ? `安装失败（已尝试自动确认 ${r.tapped} 次）`
    : '安装失败且未观测到确认弹窗 —— 失败原因不是安装确认，见上面的 install 输出')
} else if (!r.dialogSeen) {
  log('本次未出现确认弹窗（静默安装通过）')
} else {
  log(`本次出现确认弹窗，已自动确认 ${r.tapped} 次后装成功`)
}
process.exit(r.success ? 0 : 1)
