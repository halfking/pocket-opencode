// diag-app-network.mjs — 重载页面，记录 App 真实发出的所有 XHR/fetch。
//
// 为什么这么做（2026-10-01 13:22）：我用裸 fetch 探 /api/tasks 得到 401，
// 但 App 自己既没跳登录也没报错、列表还是空的。两者矛盾，说明裸 fetch
// 走的不是 App 实际那条路。最省事也最不预设的做法：**别猜，直接看它发了什么**。
// 尤其要确认 App 到底打的是 127.0.0.1:18099 还是被兜底到了生产入口。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9616'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 30000) => execFileSync(ADB, ['-s', SERIAL, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (a) => { try { return adb(a, 10000) } catch { return '' } }

const pid = adbSoft(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adbSoft(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
let list = null
for (let i = 0; i < 4 && !list; i++) {
  try { list = await (await fetch(`http://127.0.0.1:${PORT}/json/list`, { signal: AbortSignal.timeout(8000) })).json() } catch { await sleep(2000) }
}
const page = list?.find((t) => t.type === 'page')
if (!page) { console.log('CDP_UNREACHABLE'); process.exit(4) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const reqs = []
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  if (m.method === 'Network.responseReceived') {
    const r = m.params.response
    const t = r.request?.method || ''
    if (/XHR|Fetch/i.test(m.params.type) || /api\//.test(r.url)) {
      reqs.push({ s: r.status, m: t, u: r.url.replace(/([?&](token|authorization)=)[^&]*/gi, '$1…') })
    }
  }
  if (m.method === 'Network.loadingFailed') reqs.push({ s: 'FAIL', m: m.params.type, u: m.params.errorText })
})
if (!(await Promise.race([
  new Promise((r) => ws.addEventListener('open', () => r(true), { once: true })),
  new Promise((r) => setTimeout(() => r(false), 10000)),
]))) { console.log('CDP_OPEN_TIMEOUT'); process.exit(5) }
const send = (method, params = {}, ms = 20000) => new Promise((r) => {
  const i = ++id
  const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
  pending.set(i, (y) => { clearTimeout(t); r(y) })
  ws.send(JSON.stringify({ id: i, method, params }))
})

await send('Network.enable')
await send('Page.enable')
console.log('重载页面，采集 14s …')
await send('Page.reload', { ignoreCache: true })
await sleep(14000)

// 顺带把 localStorage 里和「打哪个后端」有关的键打出来
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }))?.result?.value
const cfg = await ev(`JSON.stringify({
  hash: location.hash,
  selected_server: localStorage.getItem('selected_server'),
  selected_instance: (localStorage.getItem('selected_instance')||'').slice(0,60),
  workspace: localStorage.getItem('pocket_workspace_id'),
  tokenLen: (localStorage.getItem('pocket_token')||'').length,
  keys: (function(){var o=[];for(var i=0;i<localStorage.length;i++){var k=localStorage.key(i);if(/api|base|server|instance|workspace/i.test(k))o.push(k)}return o})(),
})`)
console.log('\n本地配置:', cfg)
console.log(`\nApp 发出的请求（${reqs.length} 条）:`)
const byHost = {}
for (const r of reqs) {
  let host = '?'
  try { host = new URL(r.u).host } catch { host = r.u.slice(0, 40) }
  byHost[host] = (byHost[host] || 0) + 1
}
console.log('  按 host 汇总:', JSON.stringify(byHost))
for (const r of reqs.slice(0, 30)) console.log(`  ${String(r.s).padEnd(5)} ${r.m.padEnd(5)} ${r.u.slice(0, 110)}`)
ws.close()
process.exit(0)
