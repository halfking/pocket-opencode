// verify-au-fix.mjs — BUG-AU 修复的真机复验。
//
// 判据（可证伪的预测）：
//   第 1 次录音 —— 修复前是 6/6 被系统授权页抢前台。修复后**仍会被抢一次**
//     （必须先探测出来才能降级），但 App 会把「本机已被抢过」写进 localStorage。
//   第 2 次录音 —— 播报已被永久关掉，根本不调 TTS ⇒ 预期 **0/6 被抢**。
//
// 上一版 diag-tts-retrigger.mjs 踩的坑：它先跳到未注册路由 `#/__ttsctl2__`
// 当「中立页」，实测直接把 App 带到 `#/login?...&unlock=1`（本地库锁定守卫），
// 于是根本没测到录音。本版**不做中立页跳转**，并在每一步回读 hash，
// 免得又出现「其实压根没进目标页却读出结论」的老问题。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9462'
const adb = (a, t = 25000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (a) => { try { return adb(a, 8000) } catch { return '' } }
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const HIJACK_KEY = 'openpocket.voicePrompt.hijacked'

function topPackage() {
  const s = adbSoft(['shell', 'dumpsys activity activities'])
  const m = s.match(/topResumedActivity=ActivityRecord\{\S+ u0 (\S+?)\//)
  return m ? m[1] : '(读不到)'
}

const pid = adbSoft(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adbSoft(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
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
const ev = async (x, ms = 15000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { value: null, err: 'TIMEOUT' }
  if (v?.exceptionDetails) return { value: null, err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 160) }
  return { value: v?.result?.value, err: '' }
}

const readState = async () => {
  const r = await ev(`JSON.stringify({ hash: location.hash, vis: document.visibilityState, key: localStorage.getItem(${JSON.stringify(HIJACK_KEY)}) })`)
  try { return JSON.parse(r.value) } catch { return { err: r.err } }
}

async function recordOnce(label) {
  console.log(`\n=== ${label} ===`)
  const before = await readState()
  console.log(`  录音前: hash=${before.hash} 降级键=${before.key}  前台=${topPackage()}`)
  if (before.hash && before.hash.startsWith('#/login')) {
    console.log('  ⚠️ 本地库处于锁定态，本轮作废（不是「没被抢」，是压根没进目标页）')
    return { skipped: true }
  }
  await ev(`(async () => {
    const to = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
    location.hash = '#/meetings/new'
    await to(new Promise((r) => setTimeout(r, 3000)), 7000)
    return 'ok'
  })()`)
  const samples = []
  for (let i = 0; i < 6; i++) {
    const top = topPackage()
    const st = await readState()
    const stolen = top !== PKG
    samples.push(stolen)
    console.log(`  [${i + 1}] 前台=${stolen ? '⚠️ ' + top : '本应用'}  hash=${st.hash} vis=${st.vis} 降级键=${st.key}`)
    if (i < 5) await sleep(3000)
  }
  const n = samples.filter(Boolean).length
  const after = await readState()
  console.log(`  → 被抢 ${n}/6；录音后降级键=${after.key}`)
  // 收尾：离开宿主页后用全局指示条停掉
  await ev(`(async () => {
    const to = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
    location.hash = '#/gateway'
    await to(new Promise((r) => setTimeout(r, 1800)), 5000)
    const btn = document.querySelector('.rec-pill .rec-stop')
    if (btn) btn.click()
    await to(new Promise((r) => setTimeout(r, 2500)), 6000)
    return 'ok'
  })()`)
  console.log('  收尾后 hash=' + (await readState()).hash)
  return { n, key: after.key, skipped: false }
}

const r1 = await recordOnce('第 1 次录音（预期：被抢一次并置位降级）')
const r2 = await recordOnce('第 2 次录音（预期：0/6 被抢 —— 这才是修复生效的判据）')

console.log('\n===== 结论 =====')
if (r1.skipped || r2.skipped) {
  console.log('  ❌ 本轮未完成复验：本地库锁定导致有一轮没进目标页。不作结论。')
} else if (r2.n === 0 && r2.key === '1') {
  console.log('  ✅ 修复在真机上生效：')
  console.log(`     第 1 次录音被抢 ${r1.n}/6（探测到系统会抢前台），降级键被置为 "${r2.key}"`)
  console.log(`     第 2 次录音被抢 ${r2.n}/6 —— 播报已永久让位，系统弹窗不再打断录音主流程`)
} else {
  console.log(`  ❌ 修复未生效：第 2 次录音仍被抢 ${r2.n}/6，降级键=${r2.key}`)
}
ws.close()
process.exit(0)
