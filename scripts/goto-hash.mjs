// goto-hash.mjs — 把 App 导航到指定 hash 并回读落地结果。
//
// 用途：重装 APK 后 App 停在 #/ai（外壳可见但本地库其实锁着），
// 此时 _login.yaml 看不到「解锁本地数据」就**跳过了解锁**，
// 后面一导航到数据路由就被守卫弹回 #/login?…&unlock=1。
// 解锁办法是**先导航到受保护路由把解锁页逼出来**，再跑解锁子流程。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9465'
const TARGET = process.argv[2] || '#/meetings/new'
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
const ev = async (x, ms = 15000) => {
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
await ev(`(async () => {
  const to = (p, ms) => Promise.race([p, new Promise((r) => setTimeout(() => r('__TO__'), ms))])
  location.hash = ${JSON.stringify(TARGET)}
  await to(new Promise((r) => setTimeout(r, 2500)), 6000)
  return 'ok'
})()`)
const r = await ev(`JSON.stringify({ hash: location.hash, txt: ((document.querySelector('#app')||{}).innerText||'').replace(/\\n+/g,' | ').slice(0, 120) })`)
console.log(r.value || r.err)
ws.close()
process.exit(0)
