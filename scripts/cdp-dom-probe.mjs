// 探查真机各功能页的 DOM 结构（input/button/placeholder），为真机功能测试脚本打底
import { execFileSync } from 'node:child_process'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = '9231'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const all = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`]).split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${all.find((s) => s.endsWith(`_${pid}`)) || all[all.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
console.log('page =', page?.url)
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map()
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) } })
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true }))?.result?.value

const DUMP = `JSON.stringify({
  hash: location.hash,
  inputs: Array.from(document.querySelectorAll('input')).map(i => i.type + '|' + (i.placeholder||'') + '|' + (i.className||'')),
  textareas: Array.from(document.querySelectorAll('textarea')).map(t => (t.placeholder||'') + '|' + (t.className||'')),
  buttons: Array.from(document.querySelectorAll('button')).map(b => (b.textContent||'').trim().slice(0,24) + (b.disabled ? '[disabled]' : '')),
  sel: Array.from(document.querySelectorAll('select')).map(s => s.className),
  title: (document.querySelector('h1,h2')||{}).textContent || ''
})`

for (const route of ['#/notes', '#/notes/new', '#/finance', '#/local-agent/new', '#/meetings']) {
  await ev(`location.hash = ${JSON.stringify(route)}`)
  await sleep(2600)
  console.log('\n### ' + route)
  console.log(await ev(DUMP))
}
ws.close(); process.exit(0)
