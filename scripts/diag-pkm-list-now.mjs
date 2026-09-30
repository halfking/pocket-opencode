// diag-pkm-list-now — 只读：看此刻 PKM 列表页到底渲染出了什么（判据：条目数 + 标题）。
//
// 用途：BUG-AR 修复后，DB 里新行已落在 ws_user-admin，但 Maestro 仍报「列表看不到」。
// 这个脚本把「数据对不对」与「页面渲不渲染」分开：先读 DOM，再读同一时刻的 DB 行。
// 用法：node scripts/diag-pkm-list-now.mjs
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9417'
const adb = (a, t = 60000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

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
const ev = async (x, ms = 15000) => {
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
const show = async (label, expr) => {
  const { value, err } = await ev(expr)
  console.log(`\n### ${label}`)
  if (err) { console.log(`  !! ${err}`); return undefined }
  console.log(typeof value === 'string' ? value : JSON.stringify(value, null, 2))
  return value
}

await show('1. 当前路由', 'location.hash')
await show('2. 列表页 DOM 快照', `(() => {
  const el = document.querySelector('.pkm-today')
  if (!el) return { mounted: false, bodyHas: document.body.innerText.slice(0, 300) }
  const items = Array.from(el.querySelectorAll('.note-item')).map(n => ({
    title: (n.querySelector('.n-title')||{}).innerText,
    snippet: (n.querySelector('.n-snippet')||{}).innerText,
  }))
  return { mounted: true, count: items.length, items, emptyShown: !!el.querySelector('.empty-state, .empty-action') }
})()`)

await show('3. 用 App 自己的查询路径复算（listNotes 的 SQL 形状）', `(async () => {
  const app = document.querySelector('#app').__vue_app__
  const pinia = app.config.globalProperties.$pinia
  const conn = pinia._s.get('connectivity')
  const db = conn.runtime.deps.db()
  if (!db) return 'DB_NOT_READY'
  const wsId = pinia._s.get('auth').workspaceId || 'default'
  const a = await db.all("SELECT id, title, workspace_id, deleted_at FROM local_assets WHERE workspace_id = ? AND deleted_at IS NULL AND kind = 'note' ORDER BY updated_at DESC LIMIT 50", [wsId])
  const b = await db.all("SELECT COUNT(*) n FROM local_assets WHERE workspace_id = ? AND deleted_at IS NULL AND kind = 'note'", [wsId])
  return JSON.stringify({ readWorkspace: wsId, sqlWouldReturn: a.length, rows: a, countRow: b[0].n })
})()`)
ws.close()
process.exit(0)
