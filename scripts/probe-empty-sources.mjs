// probe-empty-sources.mjs — 看清「采不到 id」的几条：到底是没有数据，还是形状没认出来。
//
// sweep-param-routes 有 13 条 :param 模板实扫不到 id，原因分三类，本脚本把
// 「能靠造数据解锁」和「必须改后端」分开：
//   A. 有数据但形状没认出来（例：/api/agents 的 agents 可能不是数组而是对象映射）
//   B. 列表为空，可通过 API 造一条数据解锁
//   C. 端点根本不存在（/api/contacts 404），造数据也没用，属于后端缺功能
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9475'
const adb = (a, t = 25000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (a) => { try { return adb(a, 8000) } catch { return '' } }
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const pid = adbSoft(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adbSoft(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const pages = await (await fetch(`http://127.0.0.1:${PORT}/json/list`, { signal: AbortSignal.timeout(10000) })).json()
const page = pages.find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE_TARGET'); process.exit(4) }
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
const opened = await Promise.race([
  new Promise((r) => ws.addEventListener('open', () => r(true))),
  new Promise((r) => setTimeout(() => r(false), 10000)),
])
if (!opened) { console.log('CDP_OPEN_TIMEOUT'); process.exit(5) }
const ev = async (x, ms = 40000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { value: null, err: 'TIMEOUT' }
  if (v?.exceptionDetails) return { value: null, err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 200) }
  return { value: v?.result?.value, err: '' }
}

const r = await ev(`(async () => {
  const to = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
  const base = 'http://127.0.0.1:8088'
  let token = ''
  for (let i = 0; i < localStorage.length; i++) {
    const v = localStorage.getItem(localStorage.key(i)) || ''
    if (/^[A-Za-z0-9_-]{20,}\\.[A-Za-z0-9_-]{20,}\\./.test(v)) { token = v; break }
  }
  const out = {}
  const get = async (ep) => {
    try {
      const r = await to(fetch(base + ep, { headers: token ? { Authorization: 'Bearer ' + token } : {} }), 10000)
      const t = await to(r.text(), 8000)
      return { status: r.status, body: t.slice(0, 600) }
    } catch (e) { return { err: String(e && e.message || e).slice(0, 80) } }
  }
  out.agents = await get('/api/agents')
  out.agentsRaw = await get('/api/chat-agents')
  out.sessions = await get('/api/sessions')
  out.sched = await get('/api/scheduled-tasks')
  out.rss = await get('/api/rss/items')
  out.rssFeeds = await get('/api/rss/feeds')
  out.summaries = await get('/api/email/summaries')
  out.contacts = await get('/api/contacts')
  return JSON.stringify(out)
})()`)
if (r.err) { console.log('探针失败: ' + r.err); process.exit(6) }
const d = JSON.parse(r.value)
for (const [k, v] of Object.entries(d)) {
  console.log(`\n--- /api/${k} ---`)
  if (v.err) { console.log('  ERR ' + v.err); continue }
  console.log(`  status=${v.status}`)
  console.log('  ' + (v.body || '').replace(/\n/g, ' ').slice(0, 560))
}
ws.close()
process.exit(0)
