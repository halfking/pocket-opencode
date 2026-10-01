// verify-au-fix2.mjs — BUG-AU 修复真机复验（第二轮，带硬前置）。
//
// 上一版 verify-au-fix.mjs 的第二次测量**作废**，原因是判据被污染：
// 第一次录音拉起的系统授权页一直没被关掉，第二次采样时它仍然在前台，
// 于是「被系统包抢前台」恒为真。同一份输出里 hash 停在 `#/meetings/new`
// （压根没进录音页）也印证了这点——把这种读数当结论，就是自欺。
//
// 本版加的硬前置（不满足就直接判本轮无效，不产出结论）：
//   P1 录音开始前，系统授权页必须已被关掉，且 `topResumedActivity` 必须是本应用；
//   P2 录音开始前不得处于本地库锁定态（#/login…&unlock=1）；
//   P3 录音开始后 hash 必须真的落到 `#/meetings/meeting-…?record=1`，
//      否则说明没进录音页，这次采样不计入。
//
// 判据：降级键已置位的前提下，第 N 次录音应 0/6 被抢。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9463'
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

// ---- 清场：把上一轮残留的系统授权页关掉，直到本应用回到前台 ----
console.log('=== 清场：关闭残留的系统对话框 ===')
for (let i = 0; i < 6; i++) {
  const top = topPackage()
  if (top === PKG) { console.log(`  第 ${i + 1} 轮：已是本应用在前台，清场完成`); break }
  console.log(`  第 ${i + 1} 轮：前台=${top} → 点按「我知道了」并拉回 App`)
  adbSoft(['shell', 'input', 'tap', '360', '1482'])
  await sleep(1500)
  adbSoft(['shell', 'am', 'start', '-n', `${PKG}/.MainActivity`])
  await sleep(2500)
}

// ---- 硬前置 ----
console.log('\n=== 硬前置检查 ===')
const pre = await readState()
const preTop = topPackage()
console.log(`  P1 前台=${preTop} ${preTop === PKG ? '✅' : '❌'}`)
console.log(`  P2 hash=${pre.hash} ${pre.hash && pre.hash.startsWith('#/login') ? '❌ 本地库锁定' : '✅'}`)
console.log(`  降级键=${pre.key} ${pre.key === '1' ? '✅（上一轮录音已探测到劫持并置位）' : '⚠️ 未置位，本次将是修复后的第一次录音'}`)
if (preTop !== PKG) { console.log('\n❌ P1 不满足，本轮作废（不产出结论）'); process.exit(6) }
if (pre.hash && pre.hash.startsWith('#/login')) { console.log('\n❌ P2 不满足，本轮作废（本地库锁定）'); process.exit(7) }

// ---- 录音并测量 ----
console.log('\n=== 开始录音，连采 6 次 ===')
await ev(`(async () => {
  const to = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
  location.hash = '#/meetings/new'
  await to(new Promise((r) => setTimeout(r, 3000)), 7000)
  return 'ok'
})()`)
let stolen = 0, counted = 0, p3ok = 0
for (let i = 0; i < 6; i++) {
  const top = topPackage()
  const st = await readState()
  const onRecordPage = /#\/meetings\/meeting-.*record=1/.test(st.hash || '')
  if (onRecordPage) { counted++; p3ok++; if (top !== PKG) stolen++ }
  console.log(`  [${i + 1}] ${onRecordPage ? '' : '⚠️P3不满足(不计) '}前台=${top === PKG ? '本应用' : '⚠️ ' + top}  hash=${st.hash} vis=${st.vis} 降级键=${st.key}`)
  if (i < 5) await sleep(3000)
}

// 收尾
await ev(`(async () => {
  const to = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
  location.hash = '#/gateway'
  await to(new Promise((r) => setTimeout(r, 1800)), 5000)
  const btn = document.querySelector('.rec-pill .rec-stop')
  if (btn) btn.click()
  await to(new Promise((r) => setTimeout(r, 2500)), 6000)
  return 'ok'
})()`)

console.log('\n===== 结论 =====')
if (!counted) {
  console.log('  ❌ P3 全程不满足：一次都没真正进到录音页，本轮无效，不作结论。')
} else if (pre.key === '1' && stolen === 0) {
  console.log(`  ✅ 修复在真机生效：降级键已置位的前提下，${counted} 次有效采样里被系统抢前台 ${stolen} 次。`)
  console.log('     ⇒ 播报已永久让位，系统 TTS 授权页不再打断录音主流程。')
} else if (pre.key === '1') {
  console.log(`  ❌ 修复未生效：降级键已是 1，但仍有 ${stolen}/${counted} 次被抢。`)
} else {
  console.log(`  · 本次是修复后的第一次录音：被抢 ${stolen}/${counted}（预期行为，需再录一次才是判据）。`)
}
ws.close()
process.exit(0)
