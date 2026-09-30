// diag-pkm-db — 直接读真机本地库，拿到 PKM 笔记行真实的 workspace_id（硬证据）。
//
// 为什么不用插件：window.Capacitor.Plugins.CapacitorSQLite.retrieveConnection() 在
// Android 上是 "not implemented"（实测），静态拉库文件也不行（SQLCipher，表头不是
// "SQLite format 3"）。唯一能读的办法是借 App 自己的句柄：
//   document.querySelector('#app').__vue_app__  →  $pinia  →  _s.get('connectivity')
//   → .runtime.deps.db()  →  { all, run }
// 这里只用 all（只读），不碰 run。
//
// 自证：先列 sqlite_master 的表，再对**已知非空**的表取样。若连表都列不出来 /
// 样表 0 行，说明探针本身没生效，结论作废（判据必须在有数据一侧成功过）。
//
// 用法：node scripts/diag-pkm-db.mjs
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9415'
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
const ev = async (x, ms = 20000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { value: undefined, err: '__frozen__' }
  if (v?.exceptionDetails) return { value: undefined, err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 400) }
  return { value: v?.result?.value, err: '' }
}
const show = async (label, expr) => {
  const { value, err } = await ev(expr)
  console.log(`\n### ${label}`)
  if (err) { console.log(`  !! ${err}`); return undefined }
  console.log(typeof value === 'string' ? value : JSON.stringify(value, null, 2))
  return value
}

await show('0. 路由 / 登录态', `[location.hash, !!document.querySelector('#app')?.__vue_app__ ? 'vue_app_ok' : 'no_vue_app'].join(' | ')`)

// 取句柄的公共前缀，先验证拿得到再查表
const HANDLE = `(function(){
  const app = document.querySelector('#app').__vue_app__
  const pinia = app.config.globalProperties.$pinia
  if (!pinia) return 'NO_PINIA'
  const conn = pinia._s.get('connectivity')
  if (!conn || !conn.runtime) return 'NO_RUNTIME'
  const db = conn.runtime.deps.db()
  if (!db) return 'DB_NOT_READY'
  window.__diagDb = db
  window.__diagAuth = pinia._s.get('auth')
  return 'HANDLE_OK'
})()`

console.log(`\n### 1. 取数据库句柄\n  ${(await show('handle', HANDLE)).value}`)

await show('2. auth store 的 workspaceId（读侧真值）', `window.__diagAuth ? window.__diagAuth.workspaceId : 'N/A'`)
await show('3. sqlite_master 里的表（探针自证 1）', `(async()=>{
  const r = await window.__diagDb.all("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
  return JSON.stringify(r.map(x=>x.name))
})()`)
await show('4. 各表行数（探针自证 2：必须能看到非零行数）', `(async()=>{
  const r = await window.__diagDb.all("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
  const out = {}
  for (const t of r) {
    const n = String(t.name).replace(/[^A-Za-z0-9_]/g,'')
    if (!n || n.startsWith('sqlite_')) continue
    try { const c = await window.__diagDb.all('SELECT COUNT(*) n FROM "' + n + '"'); out[n] = c[0].n } catch(e) { out[n] = 'ERR' }
  }
  return JSON.stringify(out, null, 2)
})()`)
await show('5. local_assets 全表（判据本体）', `(async()=>{
  const r = await window.__diagDb.all("SELECT id, workspace_id, kind, title, deleted_at, client_rev, dirty, updated_at FROM local_assets ORDER BY updated_at DESC LIMIT 40")
  return JSON.stringify(r, null, 2)
})()`)
await show('6. local_assets 按 workspace_id 聚合', `(async()=>{
  const r = await window.__diagDb.all("SELECT workspace_id, kind, COUNT(*) n FROM local_assets GROUP BY workspace_id, kind ORDER BY n DESC")
  return JSON.stringify(r, null, 2)
})()`)
ws.close()
process.exit(0)
