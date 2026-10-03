// diag-fc-duecount — 查「开始复习」为什么还是 disabled：把 store 的实时状态全部读出来。
//
// 已知事实（2026-10-01）：闪卡写路径已打通 —— PG 里 decks=1 notes=1 cards=1，
// 卡组页也**看得见**「回归正面」（BUG-O 的核心判据已达成），
// 但「开始复习」仍 enabled=false。
// 判据在 dueByDeck（stores/flashcards.ts:187，由 cards 派生的 computed）上。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9427'
const adb = (a, t = 60000) => execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

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

await show('0. 当前位置', `location.hash`)
await show('1. store 实时状态', `(() => {
  const pinia = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia
  const s = pinia._s.get('flashcards')
  if (!s) return 'NO_STORE'
  const routeDeck = (location.hash.match(/decks\\/([^?/]+)/) || [])[1] || ''
  return JSON.stringify({
    deviceNowSec: Math.floor(Date.now() / 1000),
    routeDeck,
    cards: s.cards.map(c => ({ id: c.id, deckId: c.deckId, state: c.state, due: c.due, deletedAt: c.deletedAt || 0 })),
    notes: s.notes.map(n => ({ id: n.id, deckId: n.deckId, front: (n.front || '').slice(0, 12) })),
    decks: s.deckConfigs.map(d => d.deckId),
    dueByDeck: Array.from(s.dueByDeck.entries()),
  }, null, 1)
})()`)
await show('2. 页面上「开始复习」按钮的实时 disabled', `(() => {
  const bs = Array.from(document.querySelectorAll('button')).filter(b => b.innerText.trim() === '开始复习')
  return JSON.stringify(bs.map(b => ({ cls: b.className, disabled: b.disabled, rect: (r => [Math.round(r.x), Math.round(r.y), Math.round(r.width), Math.round(r.height)])(b.getBoundingClientRect()) })))
})()`)
await show('3. 服务端 dueCount（同一 deckId）', `(async () => {
  const base = localStorage.getItem('pocket_api_base') || 'http://127.0.0.1:8088'
  const token = localStorage.getItem('pocket_token') || ''
  const deck = (location.hash.match(/decks\\/([^?/]+)/) || [])[1] || ''
  if (!deck) return 'NO_DECK_IN_HASH'
  const now = Math.floor(Date.now() / 1000)
  const r = await fetch(base + '/api/flashcards/decks/' + encodeURIComponent(deck) + '/due?now=' + now, { headers: { Authorization: 'Bearer ' + token } })
  const j = await r.json()
  return JSON.stringify({ status: r.status, now, body: j })
})()`)
ws.close()
process.exit(0)
