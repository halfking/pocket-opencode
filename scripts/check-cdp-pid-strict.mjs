// 验证 openCdp 的 pid 严格匹配（2026-10-04 修复的回归护栏）。
//
// 要证明的性质只有一条：**openCdp 成功时，用的 socket 一定属于目标包的 pid。**
// 原来的实现会「匹配不到就取最后一个」，于是可能连到另一个活着的 App（…sttdev），
// 而且**不报错** —— /json/list 与 Runtime.evaluate 都正常。
//
// 负控怎么造出「匹配不到」？就是 App 重启窗口：pidof 已经返回新 pid，
// 但 webview_devtools_remote_<pid> 还没注册。旧代码在这个窗口里必然连错包。
// 所以这里在 force-stop + 重启过程中反复开通道，统计：
//   - throw 的次数（应当 > 0，说明窗口真实存在）
//   - 成功次数中 socket 后缀与目标 pid 不符的次数（必须为 0）
import { execFileSync } from 'node:child_process'
import { openCdp } from './lib/adb-cdp.mjs'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const adbBin = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const adb = (a, t = 20000) =>
  execFileSync(adbBin, ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

let ok = 0
let mismatch = 0
let threw = 0
const throwKinds = new Map()

// 重启 App，制造 socket 尚未注册的窗口
adb(['shell', `am force-stop ${PKG}`])
await sleep(300)

for (let i = 0; i < 40; i++) {
  if (i === 2) adb(['shell', `monkey -p ${PKG} -c android.intent.category.LAUNCHER 1`])
  try {
    const cdp = await openCdp({ pkg: PKG })
    const want = String(cdp.pid)
    const got = cdp.socket.slice(cdp.socket.lastIndexOf('_') + 1)
    if (got === want) ok++
    else {
      mismatch++
      console.log(`  ❌ 第 ${i} 次：pid=${want} 却连上了 socket=${cdp.socket}（pid=${got}）`)
    }
    await cdp.close()
  } catch (e) {
    threw++
    const kind = String(e?.message || e).split('\n')[0].slice(0, 60)
    throwKinds.set(kind, (throwKinds.get(kind) || 0) + 1)
  }
  await sleep(250)
}

console.log('')
console.log(`成功且 socket 属于目标 pid：${ok}`)
console.log(`成功但连错包：            ${mismatch}   ${mismatch === 0 ? '✅' : '❌'}`)
console.log(`抛错次数：                ${threw}`)
for (const [k, v] of throwKinds) console.log(`    ${v}×  ${k}`)

// 判据：窗口必须真实存在（否则这条测试是空转），且一次都不许连错。
const pass = mismatch === 0 && threw > 0
console.log(pass
  ? '\n✅ pid 严格匹配成立：重启窗口内只会失败，不会静默连到另一个 App。'
  : `\n❌ 不成立：mismatch=${mismatch} threw=${threw}` +
    (threw === 0 ? '（**从未抛错** ⇒ 没造出重启窗口，这条测试是空转，判据失明）' : ''))
if (!pass) process.exitCode = 1
