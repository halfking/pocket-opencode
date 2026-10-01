// diag-pkm-sync2.mjs — 读 connectivity store 的同步状态，回答「这条笔记是排队了还是丢了」。
//
// 第一版 diag-pkm-sync.mjs 的两个判据缺陷（都已记在这里，别重犯）：
//   1. `Object.keys(pinia._s)` 返回 [] —— Pinia 的 _s 是 **Map**，不是普通对象。
//      枚举 store 必须用 `Array.from(pinia._s.keys())`。
//   2. 只按 'notes'/'pkm' 这几个名字试，命中不了真正的 store 名。
// 本版直接枚举全部 store 名，并把非函数的键原样打出来。
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

await show('1. 全部 store 名（_s 是 Map，必须用 keys()）', `(() => {
  const p = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia
  return JSON.stringify(Array.from(p._s.keys()))
})()`)
await show('2. connectivity 同步状态', `(() => {
  const p = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia
  const s = p._s.get('connectivity')
  if (!s) return 'NO_CONNECTIVITY'
  return JSON.stringify({
    online: s.online, syncing: s.syncing,
    pendingCount: s.pendingCount, deadLetterCount: s.deadLetterCount,
    lastSyncAt: s.lastSyncAt, lastError: s.lastError, statusLabel: s.statusLabel,
  }, null, 1)
})()`)
await show('3. 含笔记的 store：本地笔记与 workspaceId', `(() => {
  const p = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia
  const out = {}
  for (const name of Array.from(p._s.keys())) {
    const s = p._s.get(name)
    if (!s) continue
    const arr = s.notes || s.items || s.list
    if (Array.isArray(arr) && arr.length && arr[0] && (arr[0].title !== undefined || arr[0].body !== undefined)) {
      out[name] = arr.slice(0, 6).map((n) => ({ title: (n.title || '').slice(0, 18), workspaceId: n.workspaceId, dirty: n.dirty, pending: n.pending, synced: n.synced, remoteRev: n.remoteRev, clientRev: n.clientRev }))
    }
  }
  return JSON.stringify(out, null, 1)
})()`)
ws.close()
process.exit(0)
