// PG 已清零、App 已 force-stop、localStorage 也清过，但 `flashcards:v1`
// 又带着**同一个** createdAt/updatedAt（1791044095）出现 ⇒ 它不是被重写的，
// 是从**另一处持久化**恢复的。
//
// 本脚本列出：
//   1. CDP 页面 target 的**个数**（多 WebView 时 removeItem 可能打到了另一个）；
//   2. 每个 target 的 origin 与 flashcards:v1 现状；
//   3. IndexedDB 里所有 database / objectStore（Capacitor 应用常把数据放这）。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9426'
const adb = (a, t = 60000) =>
  execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], {
    encoding: 'utf8', timeout: t, maxBuffer: 33554432,
  })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const pid = adb(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])

const targets = await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()
const pages = targets.filter((t) => t.type === 'page')
console.log(`=== CDP targets: 共 ${targets.length}，其中 page ${pages.length} 个 ===`)
for (const t of targets) console.log(`  [${t.type}] ${t.url}`)

const connect = async (page) => {
  const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
  let id = 0
  const pending = new Map()
  ws.addEventListener('message', (e) => {
    const m = JSON.parse(e.data)
    if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
  })
  await new Promise((r) => ws.addEventListener('open', r))
  const ev = (x, ms = 15000) =>
    new Promise((res) => {
      const i = ++id
      const t = setTimeout(() => { pending.delete(i); res({ __t: 1 }) }, ms)
      pending.set(i, (y) => { clearTimeout(t); res(y) })
      ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
    })
  return { ws, ev }
}

for (const [i, page] of pages.entries()) {
  const { ws, ev } = await connect(page)
  const v = await ev(`(async () => {
    const out = { origin: location.origin, hash: location.hash, ls: null, idb: [] }
    const raw = localStorage.getItem('flashcards:v1')
    out.ls = raw ? raw.slice(0, 160) : null
    try {
      const dbs = await indexedDB.databases()
      for (const d of dbs) {
        out.idb.push({ name: d.name, version: d.version })
        if (!d.name) continue
        // 列出 objectStore 里的记录条数
        await new Promise((resolve) => {
          const req = indexedDB.open(d.name)
          req.onsuccess = () => {
            const db = req.result
            const names = Array.from(db.objectStoreNames)
            out.idb[out.idb.length - 1].stores = names
            let pending = names.length
            if (!pending) return resolve()
            for (const n of names) {
              try {
                const tx = db.transaction(n, 'readonly')
                const c = tx.objectStore(n).count()
                c.onsuccess = () => { out.idb[out.idb.length - 1]['count_' + n] = c.result; if (--pending === 0) resolve() }
                c.onerror = () => { if (--pending === 0) resolve() }
              } catch { if (--pending === 0) resolve() }
            }
          }
          req.onerror = () => resolve()
        })
      }
    } catch (e) { out.idbErr = String(e).slice(0, 120) }
    return out
  })()`)
  console.log(`\n--- page[${i}] ---`)
  console.log(v?.__t ? '  (frozen)' : JSON.stringify(v?.result?.value, null, 2))
  ws.close()
}
