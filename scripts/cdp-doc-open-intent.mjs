// 精确取证：注入 PDF blob → iframe 前后，各抓「当前可见 Task 的 Hist#0 记录」，
// 拿到真正拉起 MiuiResolverActivity 的那条 Intent（含发起方 uid/包名）。
import { execFileSync } from 'node:child_process'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = '9234'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

function visibleTask() {
  const d = adb(['-s', SERIAL, 'shell', 'dumpsys activity activities'])
  // 取 visible=true 且 A= 指向 App/系统 UI 的第一个 Task 块
  const blocks = d.split(/\n\s*\*\s*Task\{/)
  for (const b of blocks) {
    if (!/visible=true/.test(b)) continue
    const top = b.match(/topResumedActivity=ActivityRecord\{\S+\s+\S+\s+(\S+)/)?.[1]
    if (!top) continue
    const launchedFrom = b.match(/launchedFromPackage=(\S+)/)?.[1]
    const uid = b.match(/launchedFromUid=(\S+)/)?.[1]
    const taskId = b.match(/^(\S+)/)?.[1]
    const intent = b.match(/\n\s*Intent \{ ([^\n]*)\n/)?.[1]
    return { top, launchedFrom, uid, taskId, intent }
  }
  return { top: '(none)' }
}

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
const all = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${all.find((s) => s.endsWith(`_${pid}`)) || all[all.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map()
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m); pending.delete(m.id) } })
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
const ev = async (x) => {
  const r = await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true })
  if (r.exceptionDetails) return 'EXC ' + (r.exceptionDetails.exception?.description || r.exceptionDetails.text)
  return r.result?.result?.value
}

const MIN_PDF_B64 = 'JVBERi0xLjQKJcTl8uXrCg=='
const IFRAME_EXPR = `(() => {
  const bin = atob('${MIN_PDF_B64}')
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  const blob = new Blob([bytes], { type: 'application/pdf' })
  const url = URL.createObjectURL(blob)
  let el = document.getElementById('__probe_frame')
  if (!el) { el = document.createElement('iframe'); el.id = '__probe_frame'; document.body.appendChild(el) }
  el.style.cssText = 'position:fixed;inset:0;z-index:99999;width:100%;height:100%;border:0;background:#fff'
  el.src = url
  return url
})()`

// 回到前台：只 BACK 一次（若有选择框弹层），然后用 am start 明确拉起 App，
// 避免把 App 退到 Launcher 导致 WebView 挂起、CDP evaluate 永不返回。
adb(['-s', SERIAL, 'shell', 'input', 'keyevent', 'KEYCODE_BACK'])
await sleep(800)
adb(['-s', SERIAL, 'shell', 'am', 'start', '-n', `${PKG}/.MainActivity`])
await sleep(2500)
await sleep(1000)
console.log('BEFORE:', JSON.stringify(visibleTask(), null, 1))
adb(['-s', SERIAL, 'logcat', '-c'])
console.log('injected url =', await ev(IFRAME_EXPR))
await sleep(5000)
console.log('AFTER :', JSON.stringify(visibleTask(), null, 1))
const lc = adb(['-s', SERIAL, 'logcat', '-d', '-v', 'brief'])
  .split(/\r?\n/).filter((l) => /START u0|Resolver|Download|pdf|fileprovider|chromium/i.test(l)).slice(-20)
console.log('logcat:\n' + lc.join('\n'))
ws.close(); process.exit(0)
