// diag-http-layers — 定位「fetch 全部返回 undefined」到底坏在哪一层。
//
// 已实测：真机上 window.fetch 对 6 种请求形态（绝对/相对、带/不带 token、
// GET/POST、存在/不存在的路径）**全部返回 undefined**，于是 http() 读 res.ok 报
// "Cannot read properties of undefined (reading 'ok')"。
// fetchName="" 且 fetchIsNative=false ⇒ 它是个 JS 包装（CapacitorHttp 打了补丁）。
//
// 这一版交叉验证三条路，把责任分清：
//   1) XMLHttpRequest  —— 绕过 fetch 补丁
//   2) CapacitorHttp.request —— 直接打原生层
//   3) navigator.sendBeacon —— 另一条独立通道
// 若 XHR 通而 fetch 不通 ⇒ 只是 fetch 补丁坏了（可绕）；全都不通 ⇒ 原生 HTTP 通道整体坏了。
//
// 用法：node scripts/diag-http-layers.mjs
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9424'
const adb = (a, t = 60000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const pid = adb(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
await new Promise((r) => ws.addEventListener('open', r))
const ev = async (x, ms = 30000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { value: undefined, err: '__frozen__' }
  if (v?.exceptionDetails) return { value: undefined, err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 300) }
  return { value: v?.result?.value, err: '' }
}
const show = async (label, expr) => {
  const { value, err } = await ev(expr)
  console.log(`\n### ${label}`)
  if (err) { console.log(`  !! ${err}`); return }
  console.log(String(value))
}

await show('0. fetch 补丁的来源（看函数源码特征）', `(() => {
  const s = String(window.fetch)
  return JSON.stringify({
    len: s.length,
    head: s.slice(0, 220),
    mentionsCapacitorHttp: s.includes('CapacitorHttp') || s.includes('nativePromise'),
  })
})()`)

await show('1. XMLHttpRequest（绕开 fetch 补丁）', `(() => new Promise((res) => {
  const base = localStorage.getItem('pocket_api_base') || 'http://127.0.0.1:8088'
  const x = new XMLHttpRequest()
  const t = setTimeout(() => res('XHR_TIMEOUT(8s)'), 8000)
  x.onload = () => { clearTimeout(t); res('status=' + x.status + ' body=' + String(x.responseText).slice(0, 120)) }
  x.onerror = () => { clearTimeout(t); res('XHR_ERROR') }
  x.open('GET', base + '/healthz', true)
  x.send()
}))()`)

await show('2. CapacitorHttp.request（直接打原生层）', `(async () => {
  try {
    const P = window.Capacitor && window.Capacitor.Plugins && window.Capacitor.Plugins.CapacitorHttp
    if (!P) return 'NO_PLUGIN'
    const r = await P.request({ url: 'http://127.0.0.1:8088/healthz', method: 'GET' })
    return JSON.stringify(r).slice(0, 300)
  } catch (e) { return 'THREW ' + String(e).slice(0, 200) }
})()`)

await show('3. sendBeacon（另一条通道）', `(() => {
  const base = localStorage.getItem('pocket_api_base') || 'http://127.0.0.1:8088'
  try {
    const ok = navigator.sendBeacon && navigator.sendBeacon(base + '/healthz', new Blob([], { type: 'text/plain' }))
    return 'sendBeacon=' + String(!!ok)
  } catch (e) { return 'THREW ' + String(e).slice(0, 160) }
})()`)

await show('4. fetch 的原型与实例检查', `(() => {
  const f = window.fetch
  return JSON.stringify({
    typeofFetch: typeof f,
    isPromise: f.constructor && f.constructor.name,
    proto: Object.getOwnPropertyNames(Object.getPrototypeOf(f)),
    hasOwnFetchOnWindow: Object.prototype.hasOwnProperty.call(window, 'fetch'),
  })
})()`)
ws.close()
process.exit(0)
