// 复现真机「打开方式」选择框：分别触发 ①PDF iframe 预览 ②downloadFile→Share 分享路径，
// 每次触发后回读 topResumedActivity + logcat，判断到底是哪条链路弹的系统选择框。
import { execFileSync } from 'node:child_process'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = '9233'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const all = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${all.find((s) => s.endsWith(`_${pid}`)) || all[all.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
console.log('page =', page?.url)
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

const topActivity = () => {
  const d = adb(['-s', SERIAL, 'shell', "dumpsys activity activities | grep topResumedActivity"])
  const line = d.split(/\r?\n/).map((l) => l.trim()).filter((l) => l.startsWith('topResumedActivity='))[0] || ''
  // 形如 topResumedActivity=ActivityRecord{7818063 u0 com.kaixuan.opencode.pocket/.MainActivity t397}
  const m = line.match(/topResumedActivity=\S+\s+\S+\s+(\S+)/)
  return m ? m[1] : (line || '(unknown)')
}
const chooserIntent = () => {
  const d = adb(['-s', SERIAL, 'shell', "dumpsys activity activities | grep -E 'act=android.intent.action.VIEW|act=android.intent.action.SEND'"])
  return (d.split(/\r?\n/).map((l) => l.trim()).filter(Boolean)[0]) || '(none)'
}
function logcatMark(tag) { adb(['-s', SERIAL, 'logcat', '-c']); return tag }
function logcatTail() {
  return adb(['-s', SERIAL, 'logcat', '-d', '-v', 'brief', '*:W'])
    .split(/\r?\n/).filter((l) => /Resolver|Chooser|Intent|FileProvider|Download|pdf|ActivityTaskManager: START|ActivityManager: START/i.test(l))
    .slice(-12).join('\n')
}

async function probe(name, expr) {
  console.log(`\n================ ${name} ================`)
  // 回到 App，确保基线干净
  adb(['-s', SERIAL, 'shell', 'input', 'keyevent', 'KEYCODE_BACK'])
  await sleep(1200)
  console.log('baseline top =', topActivity())
  logcatMark(name)
  const out = await ev(expr)
  console.log('js result:', JSON.stringify(out)?.slice(0, 400))
  await sleep(4000)
  const top = topActivity()
  console.log('after top  =', top)
  console.log('launched   =', chooserIntent())
  const isChooser = /ResolverActivity|Resolver|ChooserActivity/.test(top)
  console.log(isChooser ? '>>> VERDICT: 系统选择框被拉起' : '>>> VERDICT: 无系统选择框')
  const lc = logcatTail()
  if (lc) console.log('logcat:\n' + lc)
  adb(['-s', SERIAL, 'shell', 'input', 'keyevent', 'KEYCODE_BACK'])
  await sleep(800)
  return isChooser
}

// 最小合法 PDF（1 页，含 /Type /Page），够 WebView 判定为 application/pdf
const MIN_PDF_B64 = 'JVBERi0xLjQKJcTl8uXrCg=='

// ① 复刻 InvoicePreviewSheet.vue:13 的 <iframe :src="blob:...">
const IFRAME_EXPR = `(async () => {
  const bin = atob('${MIN_PDF_B64}')
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  const real = new Blob([bytes], { type: 'application/pdf' })
  const url = URL.createObjectURL(real)
  let el = document.getElementById('__probe_frame')
  if (!el) { el = document.createElement('iframe'); el.id = '__probe_frame'; document.body.appendChild(el) }
  el.style.cssText = 'position:fixed;inset:0;z-index:99999;width:100%;height:100%;border:0;background:#fff'
  el.src = url
  return { url, size: real.size, type: real.type }
})()`

// ② 复刻 utils/download.ts 的 downloadFile()：Filesystem 写 Cache → Share.share
const SHARE_EXPR = `(async () => {
  const C = window.Capacitor
  const FS = C.Plugins.Filesystem, SH = C.Plugins.Share
  const b64 = '${MIN_PDF_B64}'
  const r = await FS.writeFile({ path: 'probe-doc.pdf', data: b64, directory: 'CACHE', recursive: true })
  const can = await SH.canShare()
  await SH.share({ title: 'probe-doc.pdf', url: r.uri, dialogTitle: '保存或分享文件' })
  return { uri: r.uri, canShare: can }
})()`

const which = process.env.PROBE_WHICH || 'both'
if (which === 'iframe' || which === 'both') await probe('IFRAME-PDF', IFRAME_EXPR)
if (which === 'share' || which === 'both') await probe('SHARE-PDF', SHARE_EXPR)

ws.close(); process.exit(0)
