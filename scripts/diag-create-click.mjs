// diag-create-click.mjs — 点「创建」之后到底发生了什么？抓 console + network。
//
// 背景（2026-10-01 13:16）：新 APK 上弹窗能开、标题能填、「创建」按钮
// 也解禁了（enabled=1），但 Maestro 点下去弹窗不关。后端日志里也**没有**
// 任何 POST /api/tasks 痕迹。可选的解释至少三个，处置完全不同：
//   (a) 合成点击没被 WebView 当成 click ⇒ 从没进 handleCreate
//   (b) 进了 handleCreate，但 api.createTask 抛错（401/网络）⇒ 被 catch 吞掉
//   (c) 请求发出且成功，但前端没关弹窗
// 分辨办法：开 Runtime.consoleAPICalled（TasksView 失败时打的是
// `console.error('Failed to create task:', e)`）与 Network 事件，
// 然后用 DOM .click() 触发（已知这条能真正进 handler），
// 再用一次合成鼠标点击做对照。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9614'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 30000) => execFileSync(ADB, ['-s', SERIAL, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (a) => { try { return adb(a, 10000) } catch { return '' } }

const pid = adbSoft(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adbSoft(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
let list = null
for (let i = 0; i < 4 && !list; i++) {
  try { list = await (await fetch(`http://127.0.0.1:${PORT}/json/list`, { signal: AbortSignal.timeout(8000) })).json() } catch { await sleep(2000) }
}
const page = list?.find((t) => t.type === 'page')
if (!page) { console.log('CDP_UNREACHABLE'); process.exit(4) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const logs = []
const nets = []
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  if (m.method === 'Runtime.consoleAPICalled') {
    logs.push(`[${m.params.type}] ` + (m.params.args || []).map((a) => a.value ?? a.description ?? a.unserializableValue ?? '?').join(' ').slice(0, 200))
  }
  if (m.method === 'Network.responseReceived') {
    const r = m.params.response
    if (/api\/tasks|api\/auth/.test(r.url)) nets.push(`${r.status} ${r.request?.method} ${r.url}`)
  }
  if (m.method === 'Network.loadingFailed') {
    nets.push(`FAILED ${m.params.errorText} ${m.params.type}`)
  }
})
if (!(await Promise.race([
  new Promise((r) => ws.addEventListener('open', () => r(true), { once: true })),
  new Promise((r) => setTimeout(() => r(false), 10000)),
]))) { console.log('CDP_OPEN_TIMEOUT'); process.exit(5) }
const send = (method, params = {}, ms = 15000) => new Promise((r) => {
  const i = ++id
  const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
  pending.set(i, (y) => { clearTimeout(t); r(y) })
  ws.send(JSON.stringify({ id: i, method, params }))
})
const ev = async (x, ms = 15000) => {
  const r = await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }, ms)
  if (r?.__t) return { err: 'TIMEOUT' }
  if (r?.exceptionDetails) return { err: String(r.exceptionDetails.exception?.description || '').slice(0, 180) }
  return { val: r?.result?.value }
}

await send('Runtime.enable')
await send('Network.enable')

const TITLE = 'Maestro任务'
const state = () => ev(`JSON.stringify({
  sheet: !!document.querySelector('.create-task-form'),
  hash: location.hash,
  cards: document.querySelectorAll('.task-card').length,
  tok: (function(){try{var t=localStorage.getItem('pocket_token');return t?t.slice(0,12)+'…('+t.length+')':''}catch(e){return 'ERR'}})(),
})`).then((r) => (r.err || r.val))

// 复位：确保干净起点
await ev(`(function(){var o=document.querySelector('.bottom-sheet-overlay');if(o){o.remove();return 1}return 0})()`)
await sleep(600)
console.log('起点     :', await state())

// 打开 + 填标题（用 DOM，和 maestro 走的是不同通道，这里只关心「请求有没有发出去」）
await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).filter(function(x){return (x.textContent||'').trim()==='+ 新任务'})[0];if(b){b.click();return 1}return 0})()`)
await sleep(1500)
await ev(`(function(){
  var f=document.querySelector('.create-task-form');
  var i=f&&f.querySelector('input[placeholder="输入任务标题"]');
  if(!i) return 'NO_INPUT';
  var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(i),'value').set;
  s.call(i,${JSON.stringify(TITLE)});
  i.dispatchEvent(new Event('input',{bubbles:true}));
  return 'typed';
})()`)
await sleep(800)
console.log('填完标题 :', await state())

// ---- 触发：DOM .click() ----
logs.length = 0; nets.length = 0
const clicked = await ev(`(function(){
  var b=Array.prototype.slice.call(document.querySelectorAll('.bottom-sheet button')).filter(function(x){return (x.textContent||'').trim()==='创建'})[0];
  if(!b) return 'NO_BTN';
  if(b.disabled) return 'DISABLED';
  b.click(); return 'clicked';
})()`)
console.log('DOM click:', clicked.val || clicked.err)
await sleep(4000)
console.log('点击后   :', await state())
console.log('  网络   :', nets.length ? nets.join(' | ') : '（无 /api/tasks 请求）')
console.log('  控制台 :', logs.length ? logs.slice(0, 4).join(' || ') : '（无）')
ws.close()
process.exit(0)
