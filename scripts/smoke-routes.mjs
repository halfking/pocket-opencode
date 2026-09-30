// 冒烟：依次访问关键路由，收集 console error / uncaught exception。
// 目的：验证本次给 NoteListView / EmailDetailView / MeetingDetailView 三处新增
// useAuthStore 之后，全站没有因运行时错误而白屏的页面。
// 注意：先解锁全局 crypto（key 只在内存），否则 route guard 会把页面导到 #/login。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9333'
const MASTER = process.env.POCKET_MASTER || 'PocketTest2026'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])

const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const consoleErrors = []
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  if (m.method === 'Runtime.consoleAPICalled' && m.params?.type === 'error') {
    consoleErrors.push(m.params.args.map((a) => a.value ?? a.description ?? '').join(' ').slice(0, 200))
  }
  if (m.method === 'Runtime.exceptionThrown') {
    consoleErrors.push('UNCAUGHT: ' + (m.params?.exceptionDetails?.exception?.description || '').slice(0, 200))
  }
})
await new Promise((r) => ws.addEventListener('open', r))
const send = (method, params = {}) =>
  new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })) })
const ev = async (expression) =>
  (await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true }))?.result?.value

await send('Runtime.enable')

// 解锁：抽成函数，因为守卫可能在**导航途中**才要求解锁（见下方循环内注释）
async function ensureUnlocked() {
  if (!(await ev('!!document.querySelector(\'input[placeholder*="主密码"]\')'))) return false
  await ev(`(function(){var e=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set;s.call(e,${JSON.stringify(MASTER)});e.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`)
  await sleep(700)
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(x=>(x.textContent||'').trim()==='解锁');if(b)b.click();return 1})()`)
  await sleep(5000)
  return true
}

const ROUTES = [
  '/notes', '/vault', '/flashcards', '/finance', '/contacts',
  '/meetings', '/pkm', '/study', '/email', '/instances', '/settings',
]

const rows = []
for (const route of ROUTES) {
  // 每个路由前都要重新检查解锁状态，不能只在开头解锁一次。
  // 原因：路由守卫是**导航途中**才判定本地加密库是否解锁的——
  // 起点可能是 #/settings 这种不依赖本地库的路由，一开始看不到主密码输入框，
  // 于是第一次 ensureUnlocked 什么也没做；等导航到 /notes 才被守卫弹到
  // #/login?returnTo=/notes&unlock=1，后续所有依赖本地库的路由全被弹飞。
  // 症状极像「6 个路由同时回归」（textLen 恒为 121），其实是夹具的解锁时机错了。
  await ensureUnlocked()
  consoleErrors.length = 0
  await ev(`location.hash=${JSON.stringify('#' + route)}`)
  // 确定性等待：轮询 hash 实际变成目标值
  const deadline = Date.now() + 12000
  while (Date.now() < deadline && (await ev('location.hash')) !== '#' + route) await sleep(300)
  // 导航途中可能又被守卫弹走，补一次解锁再测
  await ensureUnlocked()
  await sleep(1500)
  const landed = (await ev('location.hash')) === '#' + route
  if (!landed) console.log(`  提示：${route} 被守卫改写成 ${await ev('location.hash')}`)
  const state = await ev(
    "JSON.stringify({txt:((document.querySelector('.view-root,main,#app>div')||document.body).textContent||'').trim().length, cards:document.querySelectorAll('.note-card,.vault-card,.deck-card,.card,.list-item,li').length})",
  )
  const s = JSON.parse(state || '{}')
  rows.push({ route, landed, textLen: s.txt, cards: s.cards, errors: [...consoleErrors] })
}

let bad = 0
for (const r of rows) {
  const errs = r.errors.filter((e) => !/favicon|Failed to load resource.*40[13]|net::ERR/i.test(e))
  const ok = r.landed && r.textLen > 40 && errs.length === 0
  if (!ok) bad++
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${r.route.padEnd(11)} landed=${r.landed} textLen=${r.textLen} cards=${r.cards}${errs.length ? '  ERR: ' + errs.slice(0, 2).join(' | ') : ''}`)
}
console.log(`\n=== 冒烟汇总 ===\n${rows.length - bad}/${rows.length} 通过`)
ws.close()
process.exit(0)
