// diag-tts-retrigger.mjs — 对照实验：系统 TTS 授权页被手动关掉之后，再录一次还会不会被抢前台。
//
// 上一轮（diag-tts-dialog.mjs）已确证因果链：
//   开始会议录音 → announceSilenced('start') → TextToSpeech.speak()
//   → MIUI「系统语音引擎」首次授权页拉起并抢前台 → 页面 vis=hidden
//   → WebView 节流 → 录音指示条时钟冻住。基线 0/3 被抢，实验 8/8 被抢。
//
// 但还有一个关键变量没定：这是**只有首次**才发生，还是**每次录音**都发生？
// 两者严重程度差一个量级：
//   - 只首次 → 首启体验问题，可接受（但仍该把授权页挪出「按下录音」那一刻）
//   - 每次都发生 → 录音主流程被系统 UI 反复劫持，是 P0。
//
// 所以本脚本做的是「不该红的场景」构造：先手动关掉授权页（模拟用户已同意），
// 再录一次。如果此时不再被抢，才能下「仅首次」的结论。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9461'
const adb = (a, t = 25000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (a) => { try { return adb(a, 8000) } catch { return '' } }
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

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

console.log('=== 前置：确保 App 在前台、且没有录音在跑 ===')
adbSoft(['shell', 'am start -n com.kaixuan.opencode.pocket/.MainActivity'])
await sleep(3000)
console.log('  当前前台: ' + topPackage())
{
  const r = await ev(`JSON.stringify({
    hash: location.hash, vis: document.visibilityState,
    pill: !!document.querySelector('.rec-pill'),
    recWord: /录音中|停止录音|结束录音/.test((document.querySelector('#app')||{}).innerText || ''),
  })`)
  console.log('  ' + (r.value || r.err))
}

console.log('\n=== 第二次录音（授权页已被手动关闭）===')
const exp = []
for (let i = 0; i < 6; i++) {
  if (i === 0) {
    await ev(`(async () => {
      const to = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
      location.hash = '#/__ttsctl2__'
      await to(new Promise((r) => setTimeout(r, 400)), 2000)
      location.hash = '#/meetings/new'
      await to(new Promise((r) => setTimeout(r, 2500)), 6000)
      return 'ok'
    })()`)
  }
  const top = topPackage()
  const stolen = top !== PKG
  const r = await ev(`JSON.stringify({ hash: location.hash, vis: document.visibilityState })`)
  let st = {}
  try { st = JSON.parse(r.value) } catch { st = {} }
  console.log(`  [第${i + 1}次] 前台=${stolen ? '⚠️ ' + top : '本应用'}  hash=${st.hash} vis=${st.vis}`)
  exp.push({ stolen, top })
  if (i < 5) await sleep(3000)
}
const stolen = exp.filter((s) => s.stolen)
console.log(`\n=== 判读 ===`)
console.log(`  授权页关闭后再录音，被抢前台 ${stolen.length}/6 次`)
if (stolen.length === 0) {
  console.log('  ⇒ 授权页**只在首次**出现。这是一次性首启体验，不是每次都劫持。')
  console.log('  ⇒ 严重程度下调：不是 P0 反复劫持，但仍应把授权页挪出「按下录音」那一刻。')
} else {
  console.log('  ⇒ **每次录音都会被系统授权页劫持** —— 这是 P0：录音主流程被系统 UI 反复打断。')
  stolen.forEach((s, i) => console.log(`     第 ${i + 1} 次：${s.top}`))
}

// 收尾：离开宿主页后用全局指示条停掉录音
console.log('\n=== 收尾：停掉录音 ===')
await ev(`(async () => {
  const to = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
  location.hash = '#/gateway'
  await to(new Promise((r) => setTimeout(r, 1500)), 4000)
  return 'ok'
})()`)
await sleep(1500)
{
  const r = await ev(`(async () => {
    const to = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
    const btn = document.querySelector('.rec-pill .rec-stop')
    if (!btn) return 'NO_STOP_BTN(pill=' + !!document.querySelector('.rec-pill') + ')'
    btn.click()
    await to(new Promise((r) => setTimeout(r, 3000)), 6000)
    return JSON.stringify({ pillStillThere: !!document.querySelector('.rec-pill') })
  })()`)
  console.log('  停止结果: ' + (r.value || r.err))
}
ws.close()
process.exit(0)
