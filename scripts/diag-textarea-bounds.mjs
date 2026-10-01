// diag-textarea-bounds.mjs — 取卡片编辑页两个 textarea 的**设备像素坐标**。
//
// 为什么需要坐标：WebView 里 placeholder 不一定暴露成可访问性文本
// （闪卡建卡组那个 input 实测 t="" cd=""），而「正面」这个字符串又会先匹配到
// 模板切换按钮「compare_arrows 正面 · 反面」。两条路都不可靠，只剩按坐标点。
// 用法：node scripts/diag-textarea-bounds.mjs
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9426'
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

await ev(`location.hash = '#/flashcards/new'`)
await sleep(3000)
const { value, err } = await ev(`(() => {
  const dpr = window.devicePixelRatio
  const vw = window.innerWidth, vh = window.innerHeight
  const tas = Array.from(document.querySelectorAll('textarea'))
  const info = tas.map((t) => {
    const r = t.getBoundingClientRect()
    return {
      placeholder: t.getAttribute('placeholder'),
      cssRect: [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)],
      centerPct: [Math.round((r.x + r.width / 2) / vw * 100), Math.round((r.y + r.height / 2) / vh * 100)],
      value: t.value,
    }
  })
  const saves = Array.from(document.querySelectorAll('button')).filter(b => b.innerText.trim() === '保存').map(b => {
    const r = b.getBoundingClientRect()
    return { cls: b.className, disabled: b.disabled, centerPct: [Math.round((r.x + r.width / 2) / vw * 100), Math.round((r.y + r.height / 2) / vh * 100)] }
  })
  return JSON.stringify({ dpr, vw, vh, textareas: info, saveButtons: saves }, null, 1)
})()`)
if (err) { console.log('ERR ' + err) } else { console.log(String(value)) }
ws.close()
process.exit(0)
