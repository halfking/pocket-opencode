// device-idle-check — 判定这台设备当前有没有被别的自动化进程驱动。
//
// 为什么需要：本仓库有多个会话同时工作，App 状态（localStorage / 当前路由）
// 会被别人改掉。如果两个进程同时驱动同一台设备，跑出来的 Maestro 结果既不可信
// 也会互相破坏。这个脚本**只读不写**：连采 N 次 App 状态，有变化就说明有人在动。
//
// 用法：node scripts/device-idle-check.mjs [采样次数=4] [间隔毫秒=6000]
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9411'
const N = Number(process.argv[2] || 4)
const GAP = Number(process.argv[3] || 6000)
const adb = (a, t = 60000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const pid = adb(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])

const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map()
ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) } })
await new Promise((r) => ws.addEventListener('open', r))
const ev = async (x, ms = 8000) => {
  const i = ++id
  const v = await new Promise((r) => { const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms); pending.set(i, (y) => { clearTimeout(t); r(y) }); ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } })) })
  return v?.__t ? '__frozen__' : v?.result?.value
}

const snap = async () => ({
  t: new Date().toISOString().slice(11, 19),
  hash: await ev('location.hash'),
  ls: await ev('Object.keys(localStorage).length'),
  api: await ev(`localStorage.getItem('pocket_api_base')`),
})

console.log(`设备 ${S}  pid=${pid}  连采 ${N} 次，间隔 ${GAP}ms（只读，不改任何状态）\n`)
const samples = []
for (let i = 0; i < N; i++) {
  const s = await snap()
  samples.push(s)
  console.log(`  ${s.t}  hash=${s.hash}  localStorage键数=${s.ls}  api_base=${JSON.stringify(s.api)}`)
  if (i < N - 1) await sleep(GAP)
}
adb(['forward', '--remove', `tcp:${PORT}`])

const uniq = (k) => new Set(samples.map((s) => String(s[k]))).size
const changed = uniq('hash') > 1 || uniq('ls') > 1 || uniq('api') > 1
console.log(`\nhash 取值 ${uniq('hash')} 种 / 键数 ${uniq('ls')} 种 / api_base ${uniq('api')} 种`)
if (changed) {
  console.log('❌ 采样期间 App 状态在变 —— 判定为**有人在同时驱动这台设备**，不要启动自动化。')
  process.exit(1)
}
console.log('✅ 采样期间 App 状态稳定，未观测到并发驱动。')
process.exit(0)
