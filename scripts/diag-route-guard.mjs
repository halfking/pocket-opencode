// 冒烟 6 个路由可复现地 landed=false，且 textLen 恒为 121。
// 先判定：单独导航到 /notes 到底发生什么？是被守卫弹回、还是渲染崩了。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9382'
const MASTER = process.env.POCKET_MASTER || 'PocketTest2026'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const errors = []
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  if (m.method === 'Runtime.consoleAPICalled' && m.params?.type === 'error') {
    errors.push(m.params.args.map((a) => a.value ?? a.description ?? '').join(' ').slice(0, 200))
  }
  if (m.method === 'Runtime.exceptionThrown') {
    errors.push('UNCAUGHT: ' + (m.params?.exceptionDetails?.exception?.description || '').slice(0, 200))
  }
})
await new Promise((r) => ws.addEventListener('open', r))
const send = (method, params = {}) =>
  new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })) })
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }))?.result?.value
await send('Runtime.enable')

const snap = async (label) => {
  const r = await ev(`JSON.stringify({
    hash: location.hash,
    textLen: ((document.querySelector('.app-layout')||document.body).textContent||'').trim().length,
    text: ((document.querySelector('.app-layout')||document.body).textContent||'').trim().replace(/\\s+/g,' ').slice(0,120),
    master: !!document.querySelector('input[placeholder*="主密码"]')
  })`)
  console.log(`[${label}]`, r)
}

await snap('起点')

for (const target of ['#/settings', '#/notes', '#/notes', '#/email']) {
  errors.length = 0
  await ev(`location.hash=${JSON.stringify(target)}`)
  await sleep(4000)
  await snap(`设 ${target}`)
  if (errors.length) console.log('   errors:', errors.slice(0, 2))
}
ws.close()
process.exit(0)
