// diag-pkm-local-assets.mjs — 从 App 自己的 db 句柄直读本地 assets 表。
//
// 为什么不用 PG 做 PKM 的独立取证：`pkm-store.saveNote` 走
// `assetStore.upsert({ ..., syncMode: 'e2ee_local_first' })`，写的是**本地 SQLCipher**
// 的 assets 表；PG 的 `notes` 表属于另一个模块（features/notes/notes-persist.ts）。
// 所以「PG 里搜不到这条笔记」**不是缺陷证据**——是我查错了地方。
// 上一版 find-note-in-pg.mjs 的判据本身没错（418 列实查 0 失败 0 命中），
// 但**问题问错了**。
//
// 正确取证：借 connectivity.runtime.deps.db() 拿原生句柄直查（BUG-AR 排查时用过这条路）。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9427'
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
const ev = async (x, ms = 25000) => {
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
  console.log(err ? '  !! ' + err : String(value))
}

const DB = `(async () => {
  const p = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia
  const c = p._s.get('connectivity')
  const db = c.runtime.deps.db
  const d = typeof db === 'function' ? await db() : db
  if (!d) return null
  if (d.all) return JSON.stringify(await d.all('select id, workspace_id, kind, title, client_rev, sync_mode, deleted_at from local_assets order by updated_at desc limit 6'))
  return 'UNKNOWN_DB_SHAPE:' + Object.keys(d).slice(0, 12).join(',')
})()`

await show('0. 本地库里有哪些表 + assets 的建表语句', `(async () => {
  const p = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia
  const c = p._s.get('connectivity')
  const db = c.runtime.deps.db
  const d = typeof db === 'function' ? await db() : db
  if (!d || !d.all) return 'NO_DB:' + Object.keys(d || {}).slice(0, 10).join(',')
  const tables = await d.all("select name from sqlite_master where type='table' order by name")
  let ddl = '(no assets table)'
  try {
    const r = await d.all("select sql from sqlite_master where type='table' and name like '%asset%'")
    ddl = JSON.stringify(r)
  } catch (e) { ddl = 'DDL_ERR:' + e }
  return JSON.stringify({ tables: tables && tables.values ? tables.values : tables, assetDdl: ddl })
})()`)
await show('1. 本地 assets 表最近 6 行', DB)
await show('2. 按 workspace 分组统计（BUG-AR 判据：不该有 default）', `(async () => {
  const p = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia
  const c = p._s.get('connectivity')
  const db = c.runtime.deps.db
  const d = typeof db === 'function' ? await db() : db
  const sql = 'select workspace_id, count(*) as n from local_assets group by 1 order by 2 desc'
  const r = d.all ? await d.all(sql) : null
  return JSON.stringify(r)
})()`)
ws.close()
process.exit(0)
