// harvest-route-ids.mjs — 为带 :param 的路由采真实 id。
//
// 为什么必须采真的：sweep-routes.mjs 只扫静态路由，30 条带 :param 的路由
// （详情页/编辑页）一条都没验过。直接填假 id 会全部落在「未找到」分支，
// 测不到任何真实渲染；而它们恰恰是最容易「功能没做、界面照常展示」的地方。
//
// 做法：在**页面上下文**里用 App 自己的 token 打各资源列表端点，
// 拿到真实 id。这样不必在本机复刻鉴权，也不会因为 token 过期而失真。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9466'
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
const ev = async (x, ms = 25000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { value: null, err: 'TIMEOUT' }
  if (v?.exceptionDetails) return { value: null, err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 160) }
  return { value: v?.result?.value, err: '' }
}

// 候选列表端点：覆盖 30 条 :param 路由背后的资源。
// 路径来自 frontend/src/services + api 下的**真实**调用，不是猜的——
// 猜过一次就翻车了（/api/flashcards/decks 是 404，可卡组明明存在；
// 卡组列表其实是 `/api/flashcards`，不带 /decks）。
const CANDIDATES = [
  'flashcards', 'flashcards/notes', 'notes', 'tasks', 'emails',
  'agents', 'meetings', 'sessions', 'contacts', 'scheduled-tasks',
  'email/summaries', 'email/accounts', 'rss/items', 'accTasks',
]

const expr = `(async () => {
  const withTimeout = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
  const base = (window.__apiBase || 'http://127.0.0.1:8088')
  // token 键名不猜：从 localStorage 里找看起来像 JWT 的值
  let token = ''
  for (let i = 0; i < localStorage.length; i++) {
    const k = localStorage.key(i); const v = localStorage.getItem(k) || ''
    if (/^[A-Za-z0-9_-]{20,}\\.[A-Za-z0-9_-]{20,}\\./.test(v)) { token = v; break }
  }
  const out = { base, hasToken: !!token, api: {} }
  const eps = ${JSON.stringify(CANDIDATES)}
  for (const ep of eps) {
    try {
      const res = await withTimeout(fetch(base + '/api/' + ep, {
        headers: token ? { Authorization: 'Bearer ' + token } : {},
      }), 8000)
      let body = null
      try { body = await res.json() } catch (e) { /* 非 JSON */ }
      let arr = null
      if (Array.isArray(body)) arr = body
      else if (body && typeof body === 'object') {
        // 实测这些端点多包一层：{emails:[…]} / {agents:[…]} / {meetings:[…]} /
        // {sessions:[…]} / {summaries:[…]} / {accounts:[…]}
        for (const k of ['items', 'data', 'results', 'list', 'rows', 'nodes', 'decks', 'cards', 'notes', 'tasks',
          'emails', 'agents', 'meetings', 'sessions', 'summaries', 'accounts', 'messages', 'items']) {
          if (Array.isArray(body[k])) { arr = body[k]; break }
        }
      }
      out.api[ep] = {
        status: res.status,
        count: arr ? arr.length : null,
        // 闪卡卡组的标识符是 deckId，不是 name —— 取 name 会拿到「回归卡组」这种显示名
        firstId: arr && arr.length ? (arr[0].id ?? arr[0].deckId ?? arr[0].nodeId ?? arr[0].date ?? arr[0].agentId ?? arr[0].key ?? null) : null,
        shape: arr ? null : (body && typeof body === 'object' ? Object.keys(body).slice(0, 10) : typeof body),
        firstKeys: arr && arr.length ? Object.keys(arr[0]).slice(0, 8) : null,
        raw: arr ? null : (typeof body === 'string' ? body.slice(0, 80) : null),
      }
    } catch (e) { out.api[ep] = { err: String(e && e.message || e).slice(0, 80) } }
  }
  return JSON.stringify(out)
})()`

const { value, err } = await ev(expr, 60000)
if (err) { console.log('EVAL_FAIL: ' + err); process.exit(6) }
let out
try { out = JSON.parse(value) } catch { console.log('PARSE_FAIL: ' + String(value).slice(0, 300)); process.exit(7) }
console.log(`apiBase=${out.base}  hasToken=${out.hasToken}`)
console.log('')
console.log('端点'.padEnd(24) + 'status count firstId           shape/firstKeys')
for (const [ep, v] of Object.entries(out.api)) {
  if (v.err) { console.log(`/api/${ep}`.padEnd(24) + `ERR   ${v.err}`); continue }
  const extra = v.shape ? JSON.stringify(v.shape) : (v.firstKeys ? JSON.stringify(v.firstKeys) : (v.raw || ''))
  console.log(`/api/${ep}`.padEnd(24) + String(v.status).padEnd(7) + String(v.count ?? '-').padEnd(6) + String(v.firstId ?? '-').padEnd(18) + String(extra).slice(0, 60))
}
ws.close()
process.exit(0)
