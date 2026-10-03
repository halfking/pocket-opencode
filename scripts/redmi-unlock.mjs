#!/usr/bin/env node
// 解锁真机本地库（主密码从环境变量传入，不写进仓库/日志）
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9229'
const MASTER = process.env.POCKET_MASTER
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
if (!MASTER) { console.log('POCKET_MASTER required'); process.exit(3) }

const adb = (args, t = 60000) => execFileSync(ADB, args, { encoding: 'utf8', timeout: t, maxBuffer: 32 * 1024 * 1024 })
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const all = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
const sock = all.find((s) => s.endsWith(`_${pid}`)) || all[all.length - 1]
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${sock}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const send = (m, p = {}) => new Promise((res) => { const i = ++id; pending.set(i, res); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (ev) => {
  const m = JSON.parse(ev.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
const ev = async (expression) => (await send('Runtime.evaluate', { expression, returnByValue: true }))?.result?.value

await ev(`location.hash = '#/login'`)
await sleep(2500)
console.log('unlock ->', await ev(`(function(){
  var el = document.querySelector('input[placeholder*="主密码"]');
  if (!el) return 'NOT_GATED (already unlocked)';
  Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set.call(el, ${JSON.stringify(MASTER)});
  el.dispatchEvent(new Event('input',{bubbles:true}));
  var b = Array.from(document.querySelectorAll('button')).find(x => (x.textContent||'').trim() === '解锁');
  if (!b) return 'NO_UNLOCK_BTN';
  if (b.disabled) return 'UNLOCK_BTN_DISABLED';
  b.click(); return 'unlock-clicked';
})()`))
await sleep(4000)
console.log('inputs now =', await ev(
  `Array.from(document.querySelectorAll('input')).map(i=>i.type+'/'+(i.placeholder||'')).join(', ') || '(none)'`))
ws.close()
process.exit(0)
