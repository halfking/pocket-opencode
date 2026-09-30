// 通用体检：找「后端有数据、但列表页显示 0 项」的视图。
// BUG-AL（任务看板恒空）就是这么挖出来的——当时的线索正是
// 「PG 有 14 条 active，但 .task-card 恒为 0」。把这个检查推广到所有列表视图。
//
// 做法：对每个列表路由，导出该页所有「可数的内容容器」的计数 + 页面文本摘要。
// 只报读数不下结论——0 项可能是真空数据，也可能是选择器不对，必须人工判读。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9370'
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
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
await new Promise((r) => ws.addEventListener('open', r))
const send = (method, params = {}) =>
  new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })) })
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }))?.result?.value
await send('Runtime.enable')

if (await ev('!!document.querySelector(\'input[placeholder*="主密码"]\')')) {
  await ev(`(function(){var e=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set;s.call(e,${JSON.stringify(MASTER)});e.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`)
  await sleep(700)
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(x=>(x.textContent||'').trim()==='解锁');if(b)b.click();return 1})()`)
  await sleep(5000)
}

// 视图 -> 该页的「条目容器」候选 class（-card / -item / -row / li）
// 注意：路由必须是 router-mobile.ts 里真实存在的 path。
// 首版误写了 /pkm 与 /scheduled-tasks（真实是 /pkm/today、/settings/scheduled-tasks），
// 结果这两项回退到导航页，差点被当成「列表空」——**审计脚本自己写错路由 = 假阳性**。
const VIEWS = [
  ['#/ai', ['task-card']],
  ['#/notes', ['note-card']],
  ['#/finance', ['finance-card', 'txn-item', 'ledger-item']],
  ['#/flashcards', ['deck-card', 'card-item']],
  ['#/vault', ['vault-card', 'vault-item']],
  ['#/pkm/today', ['pkm-card', 'pkm-item', 'note-card']],
  ['#/meetings', ['meeting-card', 'meeting-item']],
  ['#/email', ['email-card', 'mail-item', 'email-item', 'message-item']],
  ['#/contacts', ['contact-card', 'contact-item']],
  ['#/study', ['deck-card', 'item-card', 'learn-card']],
  ['#/instances', ['instance-card', 'instance-item']],
  ['#/settings/scheduled-tasks', ['scheduled-card', 'st-card']],
]

const probe = `(function(){
  var sels = ${JSON.stringify(VIEWS[0][1])};
  var out = { text: (document.querySelector('.app-layout')||document.body).textContent.replace(/\\s+/g,' ').trim().slice(0,150) };
  out.counts = {};
  var all = ['task-card','note-card','finance-card','txn-item','ledger-item','deck-card','vault-card','vault-item','pkm-card','pkm-item','meeting-card','meeting-item','email-card','mail-item','email-item','contact-card','contact-item','instance-card','instance-item','scheduled-card','st-card','list-item','row-item','card','item'];
  all.forEach(function(c){ var n = document.querySelectorAll('.'+c).length; if (n) out.counts[c] = n; });
  out.li = document.querySelectorAll('li').length;
  out.empty = Array.prototype.slice.call(document.querySelectorAll('.empty-state,.empty-inline,.empty-hint,.empty-title')).map(function(e){return (e.textContent||'').trim().slice(0,40)}).slice(0,3);
  return JSON.stringify(out);
})()`

console.log('route      | 可见条目 class 计数 | 空态文案')
console.log('-'.repeat(100))
// 解锁后 App 自己会重定向一次（曾把 #/ai 顶成 #/email），先静置再开始，
// 否则第一个路由的读数取到的是重定向途中的画面。
await sleep(4000)
for (const [route, _] of VIEWS) {
  await ev(`location.hash=${JSON.stringify(route)}`)
  const d = Date.now() + 14000
  while (Date.now() < d && (await ev('location.hash')) !== route) await sleep(300)
  await sleep(2500)
  const raw = await ev(probe)
  let o
  try { o = JSON.parse(raw || '{}') } catch { o = { text: 'PARSE_FAIL' } }
  const counts = Object.entries(o.counts || {}).map(([k, v]) => `${k}=${v}`).join(' ') || '(无条目容器)'
  console.log(`${route.padEnd(10)} | ${counts.padEnd(38)} | ${(o.empty || []).join(' ; ') || o.text?.slice(0, 60) || ''}`)
}
ws.close()
process.exit(0)
