// 查 App 侧闪卡缓存的真实内容：PG 已清零，但列表页仍显示「回归卡组」。
// 用 CDP 直接读 localStorage + 看 store 内存态，判断这份数据来自哪。
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
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(3) }
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
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
  if (v?.exceptionDetails) return { value: undefined, err: String(v.exceptionDetails.exception?.description || '').slice(0, 200) }
  return { value: v?.result?.value, err: '' }
}

const { value, err } = await ev(`(() => {
  const out = { hash: location.hash, keys: [] }
  for (let i = 0; i < localStorage.length; i++) {
    const k = localStorage.key(i)
    if (/flash|deck|card/i.test(k)) {
      const raw = localStorage.getItem(k) || ''
      out.keys.push({ key: k, len: raw.length, head: raw.slice(0, 400) })
    }
  }
  // 页面上实际渲染出的卡组名
  out.domDeckNames = Array.from(document.querySelectorAll('*'))
    .map(e => (e.textContent || '').trim())
    .filter(t => t && t.length < 24 && /回归|卡组|Deck/.test(t))
    .slice(0, 12)
  return out
})()`)

if (err) { console.log('EVAL_ERR', err); process.exit(4) }
console.log(JSON.stringify(value, null, 2))
ws.close()
