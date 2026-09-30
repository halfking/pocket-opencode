// 决定性实验：不跑 Maestro，只把 App 放到 #/ai 静置，看它会不会自己跳到 #/settings。
// 用来区分「App 自己会跳」和「是 Maestro 的操作触发了跳转」。
import { execFileSync } from 'node:child_process'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = '9416'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const pid = adb(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`]).split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map()
const navs = []
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  if (m.method === 'Runtime.consoleAPICalled' && ['error', 'warning'].includes(m.params.type)) {
    navs.push('[' + m.params.type + '] ' + m.params.args.map((a) => a.value ?? a.description ?? '').join(' ').slice(0, 180))
  }
})
await new Promise((r) => ws.addEventListener('open', r))
const ev = async (x, ms = 10000) => {
  const i = ++id
  const v = await new Promise((r) => { const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms); pending.set(i, (y) => { clearTimeout(t); r(y) }); ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } })) })
  return v?.__t ? { __frozen: true } : v?.result?.value
}
await send_enable()
async function send_enable() {
  const i = ++id
  pending.set(i, () => {})
  ws.send(JSON.stringify({ id: i, method: 'Runtime.enable' }))
  await sleep(300)
}

await ev(`location.hash='#/ai'`)
await sleep(2500)
console.log('放到 #/ai 后，静置观察 14 秒（无任何其它操作）:')
let last = null
for (let i = 0; i < 14; i++) {
  const h = await ev('location.hash')
  if (h !== last) { console.log(`  +${i + 1}s  hash=${h}`); last = h }
  await sleep(1000)
}
console.log(`\n最终 hash = ${await ev('location.hash')}`)
console.log('期间 console 报错/告警:')
console.log(navs.length ? navs.slice(0, 10).join('\n') : '  （无）')
adb(['forward', '--remove', `tcp:${PORT}`])
process.exit(0)
