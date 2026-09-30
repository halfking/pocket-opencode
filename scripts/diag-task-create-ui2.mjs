// 第二轮诊断：用正确的选择器（button.link-btn）打开创建弹窗，并摸清任务卡在哪一段。
// 上一轮点的是匹配到空 DIV 的错误元素，所以 create-task-form 从未出现——那是夹具错，不是产品错。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9361'
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

// 先摸清「任务区」在哪：滚动看 .task-card 是否只是没渲染出来
const before = await ev(`JSON.stringify({hash:location.hash, taskCards:document.querySelectorAll('.task-card').length, scrollH:document.body.scrollHeight, groups:Array.prototype.slice.call(document.querySelectorAll('[data-testid^="task-group-"]')).map(function(e){return e.getAttribute('data-testid')})})`)
console.log('滚动前 =', before)
await ev(`window.scrollTo(0, document.body.scrollHeight)`)
await sleep(1500)
const afterScroll = await ev(`JSON.stringify({taskCards:document.querySelectorAll('.task-card').length, scrollH:document.body.scrollHeight})`)
console.log('滚动后 =', afterScroll)
await ev(`window.scrollTo(0,0)`)
await sleep(800)

// 用正确选择器点「+ 新任务」
const clicked = await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='+ 新任务'});if(!b)return 'NO_BTN';b.click();return 'CLICKED'})()`)
console.log('点击 + 新任务 =', clicked)
await sleep(2000)

const modal = await ev(`JSON.stringify({
  createForms: document.querySelectorAll('.create-task-form').length,
  titleInputs: Array.prototype.slice.call(document.querySelectorAll('input[placeholder*="任务标题"]')).map(function(e){return {ph:e.placeholder, vis:!!e.offsetParent}}),
  visibleInputs: Array.prototype.slice.call(document.querySelectorAll('input')).filter(function(e){return !!e.offsetParent}).map(function(e){return e.placeholder||e.type}),
  visibleTextareas: Array.prototype.slice.call(document.querySelectorAll('textarea')).filter(function(e){return !!e.offsetParent}).map(function(e){return e.placeholder}),
  visibleButtons: Array.prototype.slice.call(document.querySelectorAll('button')).filter(function(e){return !!e.offsetParent}).map(function(b){return (b.textContent||'').trim()}).filter(Boolean).slice(-12)
})`)
console.log('弹窗态 =', JSON.stringify(JSON.parse(modal || '{}'), null, 2))
ws.close()
process.exit(0)
