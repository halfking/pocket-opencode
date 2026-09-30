// 在 MIUI 真机上装包时，系统会弹 com.miui.permcenter.install.AdbInstallActivity
// 确认框；没人点就自动取消，报 INSTALL_FAILED_USER_RESTRICTED。
// 本脚本后台起安装，轮询 uiautomator dump 找到那个对话框并点掉确认键。
import { execFileSync, spawn } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const APK = process.argv[2]
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (args, t = 120000) =>
  execFileSync(ADB, ['-s', S, ...args], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

if (!APK) { console.error('用法: node scripts/adb-install-confirm.mjs <apk>'); process.exit(2) }

// 后台起安装，别等它返回（它会一直等到对话框超时）
const child = spawn(ADB, ['-s', S, 'install', '-r', '-g', APK], { stdio: ['ignore', 'pipe', 'pipe'] })
let out = ''
child.stdout.on('data', (d) => { out += d })
child.stderr.on('data', (d) => { out += d })
let done = false
child.on('exit', (c) => { done = true; out += `\n[exit ${c}]` })

const deadline = Date.now() + 90000
let tapped = 0
while (Date.now() < deadline && !done) {
  await sleep(1200)
  let dump = ''
  try {
    adb(['shell', 'uiautomator', 'dump', '/sdcard/ui.xml'], 20000)
    dump = adb(['shell', 'cat', '/sdcard/ui.xml'], 20000)
  } catch { continue }
  if (!dump.includes('AdbInstallActivity') && !/安装|继续安装|是否安装/.test(dump)) continue

  // 找确认键：优先带 resource-id 的「安装」，退回按文本
  const btns = [...dump.matchAll(/text="([^"]*)"[^>]*resource-id="([^"]*)"[^>]*bounds="\[(\d+),(\d+)\]\[(\d+),(\d+)\]"/g)]
    .map((m) => ({ text: m[1], id: m[2], cx: (+m[3] + +m[5]) / 2, cy: (+m[4] + +m[6]) / 2 }))
  const hit = btns.find((b) => /^(安装|继续安装|确定|允许|继续)$/.test(b.text.trim()))
    || btns.find((b) => /install_yes|btn1|positive/i.test(b.id))
  if (hit) {
    adb(['shell', 'input', 'tap', String(hit.cx), String(hit.cy)], 20000)
    console.log(`[confirm] 已点确认键 "${hit.text}" @${hit.cx},${hit.cy}`)
    tapped++
    await sleep(1500)
  }
}
await sleep(2000)
console.log(out.trim().split(/\r?\n/).filter(Boolean).slice(-4).join('\n'))
console.log(`[confirm] 对话框自动确认次数 = ${tapped}`)
process.exit(done && /Success/i.test(out) ? 0 : 1)
