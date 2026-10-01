// diag-page-elements — 通用只读探针：把当前页面**真实可交互元素**倒出来。
//
// 为什么需要：WebView 的可访问性树会把同一容器里的多个 span 合并成一个节点，
// 且 CSS 隐藏的元素也可能进树。靠读 .vue 模板猜「面板显示没显示 / 按钮文案是什么」
// 很容易猜错（2026-10-01 连着两次猜错）。直接把 DOM 里带可见性的元素列出来。
//
// 判据：只取 offsetParent 非空 / getClientRects().length > 0 的元素（= 真的可见）。
// 用法：node scripts/diag-page-elements.mjs [hash路由 可选]
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9419'
const TARGET = process.argv[2] || ''
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
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
await new Promise((r) => ws.addEventListener('open', r))
const ev = async (x, ms = 15000) => {
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

if (TARGET) {
  await ev(`location.hash = ${JSON.stringify(TARGET)}`)
  for (let i = 0; i < 10; i++) {
    await sleep(1200)
    const { value } = await ev('location.hash')
    if (String(value || '').startsWith(TARGET.split('?')[0])) break
  }
  await sleep(1500)
}

const { value, err } = await ev(`(() => {
  const vis = (el) => { const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0 }
  const out = []
  document.querySelectorAll('button, a, input, select, textarea, [role="button"], [data-testid]').forEach((el) => {
    if (!vis(el)) return
    const t = (el.innerText || el.value || el.getAttribute('placeholder') || el.tagName === 'SELECT' ? (el.innerText || el.value || el.getAttribute('placeholder') || '') : '').replace(/\\s+/g, ' ').trim()
    out.push({
      tag: el.tagName.toLowerCase(),
      cls: (el.className && el.className.baseVal !== undefined ? el.className.baseVal : String(el.className || '')).slice(0, 40),
      testid: el.getAttribute('data-testid') || '',
      aria: el.getAttribute('aria-label') || '',
      role: el.getAttribute('role') || '',
      disabled: !!el.disabled,
      text: t.slice(0, 50),
    })
  })
  return JSON.stringify({ hash: location.hash, count: out.length, elements: out }, null, 1)
})()`)

if (err) { console.log('ERR: ' + err); process.exit(1) }
console.log(String(value))
ws.close()
process.exit(0)
