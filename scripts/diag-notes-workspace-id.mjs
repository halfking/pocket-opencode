// 判定 NoteEditView 保存用的 workspaceId 与 NoteListView 列表查的 workspaceId 是否一致。
// 背景：NoteEditView.onSave 传 workspaceId = auth.workspaceId（后端 EnsureDefaultWorkspace 下发），
//       NoteListView.load() 调 listNotes() 不传 workspaceId → notes-persist 回退 'default'。
// 本脚本从真机运行时读 localStorage 的 WS_KEY，与「列表实际查询值」对照。
// 注意：CDP Runtime.evaluate 的表达式必须单行——多行以 '(' 开头会被当成续行解析而报 is not a function。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9321'

const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) throw new Error(`app ${PKG} not running on ${SERIAL}`)

const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
const sock = socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${sock}`])

const targets = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()
const page = targets.find((t) => t.type === 'page')
if (!page) throw new Error('no page target')

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
await new Promise((r) => ws.addEventListener('open', r))
const send = (method, params = {}) =>
  new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })) })
const evaluate = async (expression) => {
  const r = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true })
  if (r?.exceptionDetails) throw new Error(JSON.stringify(r.exceptionDetails))
  return r?.result?.value
}

await send('Runtime.enable')

// 1) 保存路径用的 workspaceId（auth.workspaceId 的持久化来源）
const lsDump = await evaluate('JSON.stringify(localStorage)')
const ls = JSON.parse(lsDump || '{}')
const wsKey = Object.keys(ls).find((k) => /workspace|(^|_)ws(_|$)/i.test(k))
const stored = wsKey ? ls[wsKey] : null

// 2) 当前路由 + 列表渲染出的卡片数
const route = await evaluate('location.hash')
const cardSel = await evaluate(
  "(() => { const sels = ['.note-card','.notes-card','.note-item','article']; for (const s of sels) { const n = document.querySelectorAll(s).length; if (n) return s + '=' + n; } return 'none=0'; })()",
)

// 3) 页面里有没有报错横幅 / 空态文案
const err = await evaluate(
  "((document.querySelector('.form-error')||{}).textContent || (document.querySelector('.state')||{}).textContent || '').trim()",
)

console.log(JSON.stringify({
  appPid: pid,
  localStorageKeys: Object.keys(ls),
  workspaceKeyName: wsKey || null,
  authWorkspaceId: stored,
  listQueryValue: 'default',
  MISMATCH: stored !== 'default',
  route,
  noteCards: cardSel,
  pageErrorOrEmptyText: (err || '').slice(0, 200),
}, null, 2))
ws.close()
process.exit(0)
