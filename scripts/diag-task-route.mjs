// 诊断三：#/tasks 导航到底稳不稳定？与 #/ai 对比。
// 前两轮观察到同样地设 hash='#/tasks'，一次渲染 ai-view、一次落到 #/email。
// 如果「/tasks 不可靠」能复现，它就是真缺陷（路由守卫/重定向），不是夹具问题。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9362'
const MASTER = process.env.POCKET_MASTER || 'PocketTest2026'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
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
console.log('解锁后 hash =', await ev('location.hash'))

const probe = `JSON.stringify({hash:location.hash, cards:document.querySelectorAll('.task-card').length, newTaskBtn:document.querySelectorAll('button.link-btn').length, view:(document.querySelector('[class*="-view"]')||{}).className||''})`

for (const target of ['#/ai', '#/tasks', '#/ai', '#/tasks']) {
  await ev(`location.hash=${JSON.stringify(target)}`)
  await sleep(3500)
  console.log(`设 ${target.padEnd(8)} ->`, await ev(probe))
}

// 停在 #/ai 上试创建按钮
await ev(`location.hash='#/ai'`)
await sleep(3500)
const clicked = await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='+ 新任务'});if(!b)return 'NO_BTN';b.click();return 'CLICKED'})()`)
console.log('点「+ 新任务」=', clicked)
await sleep(2000)
console.log('弹窗 =', await ev(`JSON.stringify({createForms:document.querySelectorAll('.create-task-form').length, titleInput:document.querySelectorAll('input[placeholder*="任务标题"]').length, visibleCreateBtns:Array.prototype.slice.call(document.querySelectorAll('button')).filter(function(b){return !!b.offsetParent && /创建/.test(b.textContent||'')}).map(function(b){return (b.textContent||'').trim()})})`))
ws.close()
process.exit(0)
