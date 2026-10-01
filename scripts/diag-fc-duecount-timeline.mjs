// diag-fc-duecount-timeline — 判断「卡组页到期数不刷新」是**时序**还是**真缺陷**。
//
// 背景：flashcards-write.yaml 最后一步等「今日待复习 1 张」等满 30s 仍未出现，
// 而同一页上卡片行「回归正面」是可见的（store.cards 有这条）。
// 两次读数互相矛盾：
//   flow 刚失败时读 dueByDeck = []        （但那次 App 恰好停在解锁页，不可信）
//   解锁后重进卡组页读 dueByDeck = [[deck,1]]
//
// 所以这里在**不重载、不导航**的前提下连续采样 dueByDeck：
//   一路自己变 1  → 时序问题，flow 给足等待即可
//   一直保持 0    → 真缺陷（BUG-AS：卡组页到期数算不出来）
//
// 用法：node scripts/diag-fc-duecount-timeline.mjs [采样次数=6] [间隔ms=5000]
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9428'
const N = Number(process.argv[2] || 6)
const GAP = Number(process.argv[3] || 5000)
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
const ev = async (x, ms = 12000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { value: undefined, err: '__frozen__' }
  if (v?.exceptionDetails) return { value: undefined, err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 200) }
  return { value: v?.result?.value, err: '' }
}

const EXPR = `(() => {
  const pinia = document.querySelector('#app') && document.querySelector('#app').__vue_app__
  if (!pinia) return 'NO_APP'
  const s = pinia.config.globalProperties.$pinia._s.get('flashcards')
  if (!s) return 'NO_STORE'
  const btns = Array.from(document.querySelectorAll('button')).filter(b => b.innerText.trim() === '开始复习')
  return JSON.stringify({
    hash: location.hash.replace(/\\?.*/, ''),
    locked: location.hash.includes('unlock=1'),
    now: Math.floor(Date.now() / 1000),
    cards: s.cards.length,
    dueByDeck: Array.from(s.dueByDeck.entries()),
    reviewDisabled: btns.map(b => b.disabled),
  })
})()`

console.log(`连采 ${N} 次，间隔 ${GAP}ms（不重载、不导航）\n`)
const seen = []
for (let i = 1; i <= N; i++) {
  const { value, err } = await ev(EXPR)
  const line = err ? '!! ' + err : String(value)
  console.log(`  #${i} ${line}`)
  seen.push(line)
  if (i < N) await sleep(GAP)
}
const uniq = new Set(seen)
console.log(`\n不同取值数 = ${uniq.size}`)
if (uniq.size > 1) console.log('⇒ 取值在变 ⇒ **时序问题**，flow 给足等待即可')
else console.log('⇒ 取值恒定 ⇒ 需要看恒定的那一侧是什么（0 = 真缺陷 / 1 = 早已正常）')
ws.close()
process.exit(0)
