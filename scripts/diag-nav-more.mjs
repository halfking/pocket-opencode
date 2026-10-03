// diag-nav-more.mjs — 活体里「更多 / More」到底有几个、长什么样、点它会去哪。
//
// 背景：08:16 那轮 flow 在「点更多 → 找闪卡」之间失败（App 落在 AI 工具页），
// 而 08:08 同样的步骤是过的。先看清现场再改 flow，不要凭猜改选择器。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9427'
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
const ev = async (x, ms = 20000) => {
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
  console.log(err ? '  !! ' + err : String(value))
}

const RECT = `(r => [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)])`

await show('0. 当前位置', 'location.hash')
await show('1. 文本含「更多 / More」的元素', `(() => {
  const out = []
  for (const el of document.querySelectorAll('*')) {
    const t = (el.innerText || '').trim()
    if (!t || t.length > 12) continue
    if (!/更多|More/.test(t)) continue
    out.push({ tag: el.tagName, cls: String(el.className).slice(0, 40), text: t, rect: ${RECT}(el.getBoundingClientRect()), testid: el.getAttribute('data-testid') || '' })
  }
  return JSON.stringify(out, null, 1)
})()`)
await show('2. 屏幕最底部的可点元素（前 8 个，按 y 排序）', `(() => {
  const out = []
  for (const el of document.querySelectorAll('button, a, [role=button], [data-testid]')) {
    const r = el.getBoundingClientRect()
    if (r.width === 0 || r.height === 0) continue
    out.push({ y: Math.round(r.y), tag: el.tagName, cls: String(el.className).slice(0, 36), text: (el.innerText || el.getAttribute('aria-label') || '').trim().slice(0, 16), testid: el.getAttribute('data-testid') || '' })
  }
  out.sort((a, b) => b.y - a.y)
  return JSON.stringify(out.slice(0, 8), null, 1)
})()`)
ws.close()
process.exit(0)
