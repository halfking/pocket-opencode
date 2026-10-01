// diag-fc-dirty-test.mjs — 区分两种根因：
//   (a) dueByDeck **从不重算**（BUG-AS 原诊断：Date.now() 非响应式，无人推进）
//   (b) dueByDeck **会重算**，但它内部的 now 是冻结的旧值
//
// 判别方法：给 cards 换一个新数组（内容完全相同）——这必然让 computed 失效。
//   重算后得 1 => 是 (a)，BUG-AS 诊断成立
//   重算后仍得 0 => 是 (b)，说明设备 bundle 里的 nowSec 另有实现，
//                  工作区源码的结论在设备上不成立，必须去 bundle 里看
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
const ev = async (x, ms = 30000) => {
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

const expr = `(() => {
  const s = document.querySelector('#app').__vue_app__.config.globalProperties.$pinia._s.get('flashcards')
  const dump = () => JSON.stringify(Array.from(s.dueByDeck.entries()))
  const out = []
  out.push('now=' + Math.floor(Date.now()/1000) + '  card.due=' + (s.cards[0]||{}).due)
  out.push('0) 初始      ' + dump())
  // 强制失效：同内容、新数组。Pinia store 上直接赋值即可触发响应式。
  s.cards = [...s.cards]
  out.push('1) 换新数组后 ' + dump())
  // 再强制一次，确认不是「第一次恰好重算」
  s.cards = [...s.cards]
  out.push('2) 再换一次   ' + dump())
  // 判据真值（就地算，不经 computed）
  const now = Math.floor(Date.now()/1000)
  out.push('3) 就地判据   should-be=[[deck,1]]  due=' + (s.cards[0]||{}).due + ' now=' + now)
  return out.join('\\n')
})()`

const { value, err } = await ev(expr)
console.log(err ? '!! ' + err : value)
ws.close()
process.exit(0)
