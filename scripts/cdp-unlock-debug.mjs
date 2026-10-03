// 排查本地库解锁为何不生效
import { execFileSync } from 'node:child_process'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = '9230'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const MASTER = process.env.POCKET_MASTER || ''
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const all = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`]).split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${all.find((s) => s.endsWith(`_${pid}`)) || all[all.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map(); const logs = []
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  if (m.method === 'Runtime.consoleAPICalled') logs.push(`${m.params.type}: ` + m.params.args.map((a) => a.value ?? a.description ?? '').join(' ').slice(0, 200))
  if (m.method === 'Log.entryAdded') logs.push(`log.${m.params.entry.level}: ${m.params.entry.text.slice(0, 200)}`)
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable'); await send('Log.enable')
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true }))?.result?.value

console.log('local vault state =', await ev(`JSON.stringify({
  crypto_cfg: localStorage.getItem('pocket_crypto_cfg'),
  salt: (localStorage.getItem('pocket_crypto_salt')||'').slice(0,24),
  keys: Object.keys(localStorage).filter(k=>/crypto|vault|master|unlock/i.test(k))
})`))

await ev(`location.hash='#/login'`); await sleep(2000)
logs.length = 0
console.log('click ->', await ev(`(function(){
  var el = document.querySelector('input[placeholder*="主密码"]');
  if (!el) return 'NOT_GATED';
  Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set.call(el, ${JSON.stringify(MASTER)});
  el.dispatchEvent(new Event('input',{bubbles:true}));
  var b = Array.from(document.querySelectorAll('button')).find(x => (x.textContent||'').trim() === '解锁');
  if (!b) return 'NO_BTN';
  b.click(); return 'clicked(disabled=' + b.disabled + ')';
})()`))
for (const w of [2000, 3000, 5000]) {
  await sleep(w)
  console.log(`+${w}ms hash=${await ev('location.hash')}`,
    '| pageText=', (await ev(`document.body.innerText.replace(/\\s+/g,' ').slice(0,180)`)) || '')
}
console.log('console/log:')
for (const l of [...new Set(logs)].slice(0, 12)) console.log('  ', l)
ws.close(); process.exit(0)
