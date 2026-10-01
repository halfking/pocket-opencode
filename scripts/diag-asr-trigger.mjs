// diag-asr-trigger.mjs — 隔离实验：单独立起 Web Speech API 的语音识别，
// 会不会把 MIUI 系统语音引擎拉起来抢前台？
//
// 为什么必须单独验：BUG-AU 修了「录音语音播报（TTS）」那条路并让它自愈降级，
// 但真机复验发现**降级键已是 1、系统对话框仍然 6/6 抢前台**。
// 剩下最可疑的第二条路是 `startLiveCaption()` —— 它在 start() 里**先于**播报被调用，
// 且用的是 SpeechRecognition（Web Speech API），在 Android 上同样委托给系统 ASR。
//
// 但「最可疑」不等于「就是它」。本脚本不碰录音、不碰播报，只在页内
// 单独 new 一个 SpeechRecognition 并 start()，然后看 topResumedActivity 变不变。
// 变量只有一个，因果才干净。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9464'
const adb = (a, t = 25000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (a) => { try { return adb(a, 8000) } catch { return '' } }
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

function topPackage() {
  const s = adbSoft(['shell', 'dumpsys activity activities'])
  const m = s.match(/topResumedActivity=ActivityRecord\{\S+ u0 (\S+?)\//)
  return m ? m[1] : '(读不到)'
}

// 清场
for (let i = 0; i < 6; i++) {
  if (topPackage() === PKG) break
  adbSoft(['shell', 'input', 'tap', '360', '1482'])
  await sleep(1200)
  adbSoft(['shell', 'am', 'start', '-n', `${PKG}/.MainActivity`])
  await sleep(2200)
}
console.log('清场后前台: ' + topPackage())

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
  if (v?.exceptionDetails) return { value: null, err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 200) }
  return { value: v?.result?.value, err: '' }
}

// ---- 能力探测 ----
console.log('\n=== 页内语音识别能力探测 ===')
{
  const r = await ev(`JSON.stringify({
    SpeechRecognition: typeof window.SpeechRecognition,
    webkitSpeechRecognition: typeof window.webkitSpeechRecognition,
    vis: document.visibilityState,
  })`)
  console.log('  ' + (r.value || r.err))
}

// ---- 隔离实验：只起语音识别，不碰录音/播报 ----
console.log('\n=== 隔离实验：new SpeechRecognition().start() ===')
const before = topPackage()
console.log(`  起始前台=${before}`)
{
  const r = await ev(`(() => {
    const Rec = window.SpeechRecognition || window.webkitSpeechRecognition
    if (!Rec) return 'NO_RECOGNITION_API'
    try {
      const rec = new Rec()
      rec.lang = 'zh-CN'
      rec.continuous = true
      rec.interimResults = true
      rec.onerror = () => {}
      rec.onend = () => {}
      window.__probeRec = rec
      rec.start()
      return 'STARTED'
    } catch (e) { return 'THREW: ' + String(e && e.message || e).slice(0, 120) }
  })()`)
  console.log('  调用结果: ' + (r.value || r.err))
}
let stolen = 0
for (let i = 0; i < 5; i++) {
  await sleep(2500)
  const top = topPackage()
  const st = await ev(`JSON.stringify({ vis: document.visibilityState })`)
  let v = {}
  try { v = JSON.parse(st.value) } catch { /* ignore */ }
  const s = top !== PKG
  if (s) stolen++
  console.log(`  [${i + 1}] 前台=${s ? '⚠️ ' + top : '本应用'}  vis=${v.vis}`)
}
console.log(`\n=== 判读 ===`)
console.log(`  单独起语音识别即被系统抢前台：${stolen}/5 次`)
if (stolen > 0) {
  console.log('  ⇒ **坐实**：`startLiveCaption()` 里的 SpeechRecognition 是 BUG-AU 的**第二条独立触发路径**。')
  console.log('     它在 start() 里先于语音播报被调用，所以只修播报那一路不足以解决劫持。')
} else {
  console.log('  ⇒ 否证：语音识别不是触发者。第二条路径另有其人，需继续查。')
}
// 收尾：停掉探针起的识别
await ev(`(() => { try { window.__probeRec && window.__probeRec.stop() } catch (e) {} return 'ok' })()`)
ws.close()
process.exit(0)
