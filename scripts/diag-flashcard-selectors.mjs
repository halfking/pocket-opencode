// 一次性诊断：查闪卡编辑页两个 textarea 的真实 DOM 属性与 bounds。
//
// 为什么不再用坐标：2026-10-03 23:49/23:54 两次真机实测都证伪了
// 「按百分比点」——软键盘开合会压缩布局、页面还能滚动，
// 同一份百分比在不同时刻落在不同控件上（第二次跑，"回归背面"进了标签框）。
// 先看有没有可用 id / data-testid / aria-label，有就用选择器。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9426'
const adb = (a, t = 60000) =>
  execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], {
    encoding: 'utf8',
    timeout: t,
    maxBuffer: 33554432,
  })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const pid = adb(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) {
  console.log('APP_NOT_RUNNING')
  process.exit(2)
}
const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/)
  .map((l) => l.trim().replace('@', ''))
  .filter(Boolean)
adb(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) {
    pending.get(m.id)(m.result)
    pending.delete(m.id)
  }
})
await new Promise((r) => ws.addEventListener('open', r))
const ev = async (x, ms = 20000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => {
      pending.delete(i)
      r({ __t: 1 })
    }, ms)
    pending.set(i, (y) => {
      clearTimeout(t)
      r(y)
    })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { value: undefined, err: '__frozen__' }
  if (v?.exceptionDetails) return { value: undefined, err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 300) }
  return { value: v?.result?.value, err: '' }
}

const { value, err } = await ev(`(() => {
  const dpr = window.devicePixelRatio
  const out = { dpr, innerW: window.innerWidth, innerH: window.innerHeight,
                scrollY: window.scrollY, hash: location.hash }
  const desc = (el) => ({
    tag: el.tagName,
    id: el.id || '',
    name: el.getAttribute('name') || '',
    testid: el.getAttribute('data-testid') || '',
    aria: el.getAttribute('aria-label') || '',
    ph: el.getAttribute('placeholder') || '',
    cls: el.className && el.className.toString().slice(0, 70) || '',
    rect: (() => { const r = el.getBoundingClientRect()
      return [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)] })(),
    devPct: (() => { const r = el.getBoundingClientRect()
      return [Math.round((r.x + r.width / 2) / window.innerWidth * 100),
              Math.round((r.y + r.height / 2) / window.innerHeight * 100)] })(),
  })
  out.textareas = Array.from(document.querySelectorAll('textarea')).map(desc)
  out.inputs = Array.from(document.querySelectorAll('input')).map(desc)
  out.buttons = Array.from(document.querySelectorAll('button'))
    .map((b) => ({ ...desc(b), text: (b.textContent || '').trim().slice(0, 20), disabled: b.disabled }))
    .filter((b) => /保存|Save|卡组/.test(b.text) || b.cls.includes('save'))
  return out
})()`)

if (err) {
  console.log('EVAL_ERR:', err)
  process.exit(3)
}
console.log(JSON.stringify(value, null, 2))
