// pkm-test-fixture — 清掉 notes-crud.yaml 产生的测试残留，让每次 run 的断言都真的在验当次结果。
//
// 为什么必须清：flow 的收尾断言是「列表里能看到 MaestroPKM笔记」。如果不清理，
// 上一轮跑出来的同名笔记会一直躺在列表里，于是**即使功能彻底坏掉，断言照样通过**——
// 判据失去区分能力。清理后，「功能坏」= 列表为空 = 断言必红，这才有意义。
//
// 只删 notes-crud.yaml 会产生的两类行（标题「无标题」或以 Maestro 开头），
// 不碰其它数据；每次删完打印被删的 id，便于核对。
//
// 用法：node scripts/pkm-test-fixture.mjs [--dry]
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9418'
const DRY = process.argv.includes('--dry')
const adb = (a, t = 60000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

const pid = adb(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING（先跑 _login.yaml 解锁后再清）'); process.exit(2) }
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

const { value, err } = await ev(`(async () => {
  const app = document.querySelector('#app').__vue_app__
  const pinia = app.config.globalProperties.$pinia
  const db = pinia._s.get('connectivity').runtime.deps.db()
  if (!db) return 'DB_NOT_READY'
  const before = await db.all("SELECT id, workspace_id, title FROM local_assets WHERE kind='note' AND (title = '无标题' OR title LIKE 'Maestro%')")
  if (${DRY ? 'true' : 'false'}) return JSON.stringify({ dry: true, matched: before })
  for (const r of before) {
    await db.run("DELETE FROM local_assets WHERE id = ?", [r.id])
  }
  const after = await db.all("SELECT id FROM local_assets WHERE kind='note' AND (title = '无标题' OR title LIKE 'Maestro%')")
  return JSON.stringify({ deleted: before.map(r => ({ id: r.id, ws: r.workspace_id, title: r.title })), remaining: after.length })
})()`)

if (err) { console.log('ERR: ' + err); process.exit(1) }
console.log(String(value))
ws.close()
process.exit(0)
