// 诊断：任务创建流程里到底是哪一步没生效。
// 上一轮 verify-task-writepath 报「PG 无落库」，但那既可能是产品 bug，
// 也可能是夹具没点对/没输入上。判据必须先自证是哪一种。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9360'
const MASTER = process.env.POCKET_MASTER || 'PocketTest2026'
const PSQL = process.env.POCKET_PSQL || 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const psql = (sql) => execFileSync(PSQL, ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-t', '-A', '-c', sql], { encoding: 'utf8' }).trim()

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
await ev(`location.hash='#/tasks'`)
await sleep(4000)

const dump = await ev(`JSON.stringify({
  hash: location.hash,
  titleInputs: Array.prototype.slice.call(document.querySelectorAll('input[placeholder*="任务标题"]')).map(function(e){return {ph:e.placeholder, val:e.value, vis:!!e.offsetParent}}),
  createForms: document.querySelectorAll('.create-task-form').length,
  modals: document.querySelectorAll('.modal, .modal-overlay, [role="dialog"]').length,
  buttonsWithCreate: Array.prototype.slice.call(document.querySelectorAll('button')).map(function(b){return (b.textContent||'').trim()}).filter(function(t){return /创建|新任务/.test(t)}),
  newTaskBtn: Array.prototype.slice.call(document.querySelectorAll('button,div,span')).filter(function(e){return /\\+\\s*新任务/.test(e.textContent||'')}).map(function(e){return e.tagName+'.'+(e.className||'').toString().slice(0,40)}),
  taskCards: document.querySelectorAll('.task-card').length
})`)

console.log('=== 页面快照 ===')
console.log(JSON.stringify(JSON.parse(dump || '{}'), null, 2))

console.log('\n=== PG 最近 8 条任务 ===')
console.log(psql(`select id || ' | ' || status || ' | ' || left(title,44) from opencode_pocket.tasks order by created_at desc limit 8`))
ws.close()
process.exit(0)
