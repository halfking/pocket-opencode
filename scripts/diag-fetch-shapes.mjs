// diag-fetch-shapes — 决定性实验：真机上 window.fetch 到底对哪些请求返回 undefined。
//
// 背景：闪卡「新建卡组」失败的 store 报错是
//   "Cannot read properties of undefined (reading 'ok')"
// 也就是说 http() 里 `await fetch(...)` **拿到了 undefined**，随后读 res.ok 就炸。
// 页面环境：fetchName="" 且 Capacitor.isNativePlatform()=true
// ⇒ CapacitorHttp 插件把 window.fetch 换成了原生实现。
//
// 这一版把各种请求形态各打一遍，逐一记录「返回 undefined / 抛错 / 正常」，
// 才能判断是「所有 fetch 都坏」还是「某类请求坏」——这两者的处置完全不同。
//
// 用法：node scripts/diag-fetch-shapes.mjs
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9423'
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
  if (v?.exceptionDetails) return { value: undefined, err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 300) }
  return { value: v?.result?.value, err: '' }
}
const show = async (label, expr) => {
  const { value, err } = await ev(expr)
  console.log(`\n### ${label}`)
  if (err) { console.log(`  !! ${err}`); return }
  console.log(String(value))
}

await show('0. 环境', `(() => {
  const cap = window.Capacitor || {}
  return JSON.stringify({
    origin: location.origin,
    hash: location.hash,
    fetchName: window.fetch && window.fetch.name,
    fetchIsNative: String(window.fetch).includes('[native code]'),
    isNative: cap.isNativePlatform ? cap.isNativePlatform() : null,
    plugins: Object.keys(cap.Plugins || {}).filter(k => /Http|Capacitor/i.test(k)),
    apiBase: localStorage.getItem('pocket_api_base'),
    hasToken: !!localStorage.getItem('pocket_token'),
  })
})()`)

// 逐个请求形态：包一层 try，任何一步炸了都记下来而不是中断整段
await show('1. 各种 fetch 形态', `(async () => {
  const base = localStorage.getItem('pocket_api_base') || 'http://127.0.0.1:8088'
  const token = localStorage.getItem('pocket_token') || ''
  const H = { 'Content-Type': 'application/json', Authorization: 'Bearer ' + token }
  const out = []
  const probe = async (label, fn) => {
    try {
      const r = await fn()
      if (r === undefined) { out.push(label + ' => UNDEFINED'); return }
      let body = ''
      try { body = String(await r.text()).slice(0, 120) } catch (e) { body = 'TEXT_THREW:' + e.message }
      out.push(label + ' => ok=' + r.ok + ' status=' + r.status + ' body=' + body)
    } catch (e) { out.push(label + ' => THREW ' + String(e).slice(0, 160)) }
  }
  await probe('A GET healthz(绝对)', () => fetch(base + '/healthz'))
  await probe('B GET /api/flashcards/decks(带 token)', () => fetch(base + '/api/flashcards/decks', { headers: H }))
  await probe('C GET /api/flashcards/decks(无 token)', () => fetch(base + '/api/flashcards/decks'))
  await probe('D POST decks(带 token+body)', () => fetch(base + '/api/flashcards/decks', { method: 'POST', headers: H, body: JSON.stringify({ name: 'probeA' }) }))
  await probe('E GET 相对路径 /healthz', () => fetch('/healthz'))
  await probe('F GET 不存在的路径', () => fetch(base + '/__definitely_not_here__'))
  return out.join('\\n')
})()`)

// store 自己那条路
await show('2. 走 store.createDeck', `(async () => {
  const pinia = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia
  const s = pinia._s.get('flashcards')
  try { const d = await s.createDeck('probeB'); return 'OK ' + JSON.stringify(d && d.deckId) }
  catch (e) { return 'THREW ' + String(e && e.message || e) }
})()`)
await show('3. 事后 store 状态', `(() => {
  const pinia = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia
  const s = pinia._s.get('flashcards')
  return JSON.stringify({ decks: s.deckConfigs.length, error: s.error || '' })
})()`)
ws.close()
process.exit(0)
