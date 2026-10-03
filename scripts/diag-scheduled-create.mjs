// 定时任务创建未落库——先定性：夹具没填必填，还是产品 bug。
// save() 里有两条早退分支：
//   1) payloadText 非法 JSON -> "任务参数必须是合法 JSON"
//   2) showPrompt 为真且 prompt 为空 -> "请填写任务提示词"（**不发请求**）
// 上一轮只填了 payloadText 没填 prompt，很可能撞的是第 2 条。
// 这里直接把页面上真实的 error 文案和 showPrompt 状态读出来，不靠推断。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9397'
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

async function ensureUnlocked() {
  if (!(await ev('!!document.querySelector(\'input[placeholder*="主密码"]\')'))) return
  await ev(`(function(){var e=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set;s.call(e,${JSON.stringify(MASTER)});e.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`)
  await sleep(700)
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='解锁'});if(b)b.click();return 1})()`)
  await sleep(5000)
}
async function typeInto(sel, text) {
  const box = await ev(`(function(){var e=document.querySelector(${JSON.stringify(sel)});if(!e)return null;var r=e.getBoundingClientRect();return JSON.stringify({x:r.left+r.width/2,y:r.top+r.height/2})})()`)
  if (!box) return 'NOT_FOUND'
  const { x, y } = JSON.parse(box)
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', clickCount: 1 })
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', clickCount: 1 })
  await sleep(250)
  await send('Input.insertText', { text })
  await sleep(500)
  return 'typed'
}

await ensureUnlocked()
await ev(`location.hash='#/settings/scheduled-tasks/new'`)
await sleep(3500)
await ensureUnlocked()

// 表单结构快照
console.log('=== 表单结构 ===')
console.log(await ev(`JSON.stringify({
  selects: Array.prototype.slice.call(document.querySelectorAll('select')).map(function(s){return {opts:Array.prototype.slice.call(s.options).map(function(o){return o.value+'='+o.selected})}}),
  visibleTextareas: Array.prototype.slice.call(document.querySelectorAll('textarea')).filter(function(t){return !!t.offsetParent}).map(function(t){return t.placeholder}),
  visibleInputs: Array.prototype.slice.call(document.querySelectorAll('input')).filter(function(i){return !!i.offsetParent}).map(function(i){return (i.type||'text')+':'+(i.placeholder||'')})
}, null, 1)`))

// 按当前表单填全：名称 + 提示词（不碰 payloadText，避免与 prompt 分支冲突）
console.log('=== 填表 ===')
console.log('name  =', await typeInto('input[placeholder*="工作日晨报"]', 'STX-' + String(Date.now()).slice(-6)))
console.log('prompt=', await typeInto('textarea[placeholder*="到点时"]', 'probe-only-noop'))
await sleep(400)

const before = await ev(`JSON.stringify({
  name: (document.querySelector('input[placeholder*="工作日晨报"]')||{}).value,
  prompt: (document.querySelector('textarea[placeholder*="到点时"]')||{}).value,
  payload: (document.querySelector('textarea[placeholder*="example.com"]')||{}).value,
  kind: (function(){var s=document.querySelectorAll('select')[0];return s?s.value:''})(),
  err: (function(){var e=document.querySelector('.error,[role="alert"]');return e?(e.textContent||'').trim().slice(0,80):''})()
})`)
console.log('点创建前 =', before)

await ev(`(function(){var bs=Array.prototype.slice.call(document.querySelectorAll('button')).filter(function(b){return !!b.offsetParent && (b.textContent||'').trim()==='创建任务'});if(bs.length){bs[0].click();return 1}return 0})()`)
await sleep(3500)

const after = await ev(`JSON.stringify({
  hash: location.hash,
  err: Array.prototype.slice.call(document.querySelectorAll('.error,[role="alert"]')).map(function(e){return (e.textContent||'').trim().slice(0,120)}),
  text: (document.querySelector('.app-layout')||document.body).textContent.replace(/\\s+/g,' ').trim().slice(0,160)
})`)
console.log('点创建后 =', after)
ws.close()
process.exit(0)
