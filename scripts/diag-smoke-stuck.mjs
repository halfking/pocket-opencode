// 冒烟出现 6 个 landed=false 且 textLen 完全相同(121)——先判定是「卡在同一个界面」
// 还是「真回归」。做法：读当前 hash + 页面文本 + 是否有主密码输入框。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9380'
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

const snap = async (label) => {
  const r = await ev(`JSON.stringify({
    hash: location.hash,
    hasMasterInput: !!document.querySelector('input[placeholder*="主密码"]'),
    hasLoginBtn: Array.prototype.slice.call(document.querySelectorAll('button')).some(function(b){return (b.textContent||'').trim()==='解锁'}),
    textLen: ((document.querySelector('.app-layout')||document.body).textContent||'').trim().length,
    text: ((document.querySelector('.app-layout')||document.body).textContent||'').trim().replace(/\\s+/g,' ').slice(0,140)
  })`)
  console.log(`[${label}]`, r)
}
await snap('当前状态')

// 尝试解锁
if (await ev('!!document.querySelector(\'input[placeholder*="主密码"]\')')) {
  console.log('=> 检测到主密码输入框，执行解锁')
  await ev(`(function(){var e=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set;s.call(e,${JSON.stringify(MASTER)});e.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`)
  await sleep(800)
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='解锁'});if(b){b.click();return 1}return 0})()`)
  await sleep(6000)
  await snap('解锁后')
}
ws.close()
process.exit(0)
