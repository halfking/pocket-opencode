// diag-pkm-sync.mjs — PKM 笔记在 UI 里可见，但全 schema 搜不到：是没同步，还是同步失败？
//
// 事实（2026-10-01 08:44）：notes-crud.yaml 全绿、列表里看得到新笔记，
// 但 find-note-in-pg.mjs 在 opencode_pocket 的 418 个文本列里实查 0 失败 0 命中。
// ⇒ 要么笔记只存在本地 SQLCipher、outbox 没 flush，要么 flush 了但被拒。
// 这里读活体 store 的 outbox / 同步状态来定性。
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
const ev = async (x, ms = 20000) => {
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
  console.log(err ? '  !! ' + err : String(value))
}

await show('0. 当前位置', 'location.hash')
await show('1. pinia 里所有 store 名 + 笔记相关 store 的键', `(() => {
  const p = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia
  const names = Object.keys(p._s.getters ? {} : {})
  const stores = Object.keys(p._s)
  const out = { stores }
  for (const n of ['notes', 'pkm', 'pkmStore', 'notesStore', 'connectivity', 'assets']) {
    const s = p._s.get(n)
    if (s) out[n + '_keys'] = Object.keys(s).slice(0, 40)
  }
  return JSON.stringify(out, null, 1)
})()`)
await show('2. notes store 里的笔记与待同步状态', `(() => {
  const p = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia
  const s = p._s.get('notes') || p._s.get('pkm')
  if (!s) return 'NO_NOTES_STORE'
  const pick = (o, k) => (o && typeof o === 'object' ? o[k] : undefined)
  return JSON.stringify({
    notes: (s.notes || []).map((n) => ({ id: n.id, title: (n.title || '').slice(0, 20), workspaceId: n.workspaceId, dirty: n.dirty, synced: n.synced, pending: n.pending, updatedAt: n.updatedAt })),
    outboxLen: Array.isArray(s.outbox) ? s.outbox.length : pick(s, 'outboxLen'),
    online: s.online, syncing: s.syncing, lastSyncedAt: s.lastSyncedAt, error: s.error,
  }, null, 1)
})()`)
await show('3. 服务端到底认不认这条笔记（带 token 直查）', `(async () => {
  const base = localStorage.getItem('pocket_api_base') || 'http://127.0.0.1:8088'
  const token = localStorage.getItem('pocket_token') || ''
  const r = await fetch(base + '/api/notes?limit=5', { headers: { Authorization: 'Bearer ' + token } })
  const t = await r.text()
  return JSON.stringify({ status: r.status, hasToken: !!token, body: t.slice(0, 400) })
})()`)
ws.close()
process.exit(0)
