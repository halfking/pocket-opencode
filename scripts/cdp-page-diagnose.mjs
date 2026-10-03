// cdp-page-diagnose.mjs — 诊断指定路由在真机上的渲染失败原因。
// 用法：POCKET_SERIAL=... node scripts/cdp-page-diagnose.mjs '#/flashcards/browser' '#/flashcards/stats'
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9247'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

const ROUTES = process.argv.slice(2)
if (ROUTES.length === 0) { console.error('用法: node cdp-page-diagnose.mjs <hash> [<hash>...]'); process.exit(2) }

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
const consoleMsgs = []
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  if (m.method === 'Runtime.consoleAPICalled') {
    consoleMsgs.push({ type: m.params.type, text: (m.params.args || []).map((a) => a.value ?? a.description ?? a.type).join(' ').slice(0, 300) })
  }
  if (m.method === 'Runtime.exceptionThrown') {
    const d = m.params.exceptionDetails
    consoleMsgs.push({ type: 'EXCEPTION', text: (d.exception?.description || d.text || '').slice(0, 400) })
  }
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
const ev = async (x, t = 12000) => {
  const res = await Promise.race([send('Runtime.evaluate', { expression: x, returnByValue: true }), sleep(t).then(() => ({ __t: 1 }))])
  return res?.__t ? undefined : res?.result?.value
}

for (const route of ROUTES) {
  consoleMsgs.length = 0
  await ev(`location.hash = ${JSON.stringify(route)}`)
  await sleep(3200)
  const info = await ev(`JSON.stringify({
    hash: location.hash,
    appHTMLLen: (document.querySelector('#app')||{}).innerHTML ? document.querySelector('#app').innerHTML.length : -1,
    mainChildren: document.querySelector('main') ? document.querySelector('main').children.length : -1,
    visibleText: (document.body.innerText||'').replace(/\\s+/g,' ').trim().slice(0,200),
    hasVueErr: !!document.querySelector('.error, [role=alert]'),
    alertText: (document.querySelector('[role=alert]')||{}).innerText || '',
  })`)
  console.log(`\n=== ${route} ===`)
  console.log(info)
  const errs = consoleMsgs.filter((m) => /error|EXCEPTION/i.test(m.type) || /error/i.test(m.text))
  if (errs.length) {
    console.log('--- console 错误 ---')
    for (const e of errs.slice(0, 6)) console.log(`  [${e.type}] ${e.text}`)
  } else {
    console.log('  (无 console 错误)')
  }
}
