// diag-fc-duecount-why.mjs — 把 dueByDeck 的判据在活体页面里逐条求值，定位是哪个子条件把卡片排除了。
//
// 已知（2026-10-01 08:14 真机）：服务端 /due 返回 totalDue:1，卡片 state=0、due 已过，
// 但客户端 store.dueByDeck 是空 Map。判据只有 4 个子条件，逐条求值即可定位。
// 同时把装机 bundle 里的判据原文也读出来——**设备上的代码可能不是工作区里的代码**，
// 这是「读源码」最容易骗人的地方。
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
  if (err) { console.log(`  !! ${err}`); return }
  console.log(String(value))
}

// 1) 逐条求值判据
await show('1. 判据逐条求值（对照工作区源码的四条）', `(() => {
  const s = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia._s.get('flashcards')
  const now = Math.floor(Date.now() / 1000)
  return JSON.stringify(s.cards.map(c => ({
    id: c.id.slice(0, 12), deckId: c.deckId.slice(0, 12),
    state: c.state, due: c.due, deletedAt: c.deletedAt || 0,
    cond_deleted_skipped: !!(c.deletedAt && c.deletedAt > 0),
    cond_isLearning_state0_1_3: c.state === 0 || c.state === 1 || c.state === 3,
    cond_due_le_now: c.due <= now,
    verdict_isDue: (c.state === 0 || c.state === 1 || c.state === 3) ? (c.due <= now) : true,
  })), null, 1) + '\\nnow=' + now
})()`)

// 2) 现读一次 dueByDeck（第一次访问才重算），再读一次看是否稳定
await show('2. 连续读两次 dueByDeck（若第一次空第二次非空 => 是缓存脏）', `(() => {
  const s = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia._s.get('flashcards')
  const a = JSON.stringify(Array.from(s.dueByDeck.entries()))
  const b = JSON.stringify(Array.from(s.dueByDeck.entries()))
  return 'first=' + a + '  second=' + b
})()`)

// 3) 在页面里就地重算一遍判据（绕开 computed，验证「数据本身能不能算出 1」）
await show('3. 就地重算（不经 computed）', `(() => {
  const s = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia._s.get('flashcards')
  const now = Math.floor(Date.now() / 1000)
  const map = new Map()
  for (const c of s.cards) {
    if (c.deletedAt && c.deletedAt > 0) continue
    const isLearning = c.state === 0 || c.state === 1 || c.state === 3
    const isDue = isLearning ? c.due <= now : true
    if (!isDue) continue
    map.set(c.deckId, (map.get(c.deckId) ?? 0) + 1)
  }
  return JSON.stringify(Array.from(map.entries()))
})()`)

// 4) 装机 bundle 里 dueByDeck 的判据原文——确认设备上的代码到底长什么样
await show('4. 装机 bundle 里 dueByDeck 判据原文', `(() => {
  const html = document.documentElement.outerHTML
  const scripts = Array.from(document.querySelectorAll('script[src]')).map(s => s.getAttribute('src'))
  return JSON.stringify(scripts)
})()`)

ws.close()
process.exit(0)
