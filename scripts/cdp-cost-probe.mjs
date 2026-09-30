// 定位 /cost -> /ai-chat 跳转：抓路由变化 + 控制台 + 失败的网络请求
import { execFileSync } from 'node:child_process'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = '9234'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const sk = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`]).split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${sk.find((s) => s.endsWith(`_${pid}`)) || sk[sk.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map(); const logs = []
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  const p = m.params || {}
  if (m.method === 'Runtime.consoleAPICalled') logs.push(`console.${m.params.type}: ` + m.params.args.map((a) => a.value ?? a.description ?? '').join(' ').slice(0, 240))
  if (m.method === 'Runtime.exceptionThrown') logs.push('EXCEPTION: ' + (p.exceptionDetails?.exception?.description || p.exceptionDetails?.text || '').slice(0, 300))
  if (m.method === 'Log.entryAdded' && p.entry.level !== 'verbose') logs.push(`log.${p.entry.level}: ${p.entry.text.slice(0, 240)}`)
  if (m.method === 'Network.loadingFailed') logs.push(`netfail: ${p.errorText} blocked=${p.blockedReason ?? '-'} type=${p.type}`)
  if (m.method === 'Network.responseReceived' && p.response.status >= 400) logs.push(`http${p.response.status}: ${p.response.url.slice(0, 120)}`)
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable'); await send('Log.enable'); await send('Network.enable')
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true }))?.result?.value

console.log('router exists =', await ev(`!!(window.__vueRouter || document.querySelector('#app').__vue_app__)`))
console.log('app __vue_app__ =', await ev(`!!document.querySelector('#app').__vue_app__`))
console.log('start hash =', await ev(`location.hash`))

// 逐次导航并观察
for (const route of ['#/cost', '#/gateway', '#/instances', '#/cost']) {
  logs.length = 0
  await ev(`location.hash = ${JSON.stringify(route)}`)
  const samples = []
  for (let i = 0; i < 5; i++) { await sleep(900); samples.push(await ev(`location.hash`)) }
  console.log(`\n### navigate ${route}`)
  console.log('  hash timeline:', samples.join(' -> '))
  console.log('  final title  :', await ev(`(document.querySelector('h1,h2')||{}).textContent || '(none)'`))
  console.log('  body head    :', ((await ev(`document.body.innerText.replace(/\\s+/g,' ')`)) || '').slice(0, 120))
  const interesting = [...new Set(logs)].filter(l => !/Mixed Content|deprecated/i.test(l))
  for (const l of interesting.slice(0, 8)) console.log('   !', l)
}
ws.close(); process.exit(0)
