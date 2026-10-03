// 给 hashchange 装探针，抓自主跳转时的**调用栈**，区分是 Vue Router 推的还是裸改 hash。
import { execFileSync } from 'node:child_process'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = '9417'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const pid = adb(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`]).split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map()
ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) } })
await new Promise((r) => ws.addEventListener('open', r))
const ev = async (x, ms = 10000) => {
  const i = ++id
  const v = await new Promise((r) => { const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms); pending.set(i, (y) => { clearTimeout(t); r(y) }); ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } })) })
  return v?.__t ? { __frozen: true } : v?.result?.value
}

// 装探针：记录每次 hashchange 的时间、旧值、新值与调用栈
await ev(`window.__navLog = [];
window.addEventListener('hashchange', function () {
  var st = '';
  try { st = new Error().stack || ''; } catch (e) { st = 'stack unavailable: ' + e; }
  window.__navLog.push({ t: Date.now(), from: window.__lastHash, to: location.hash, stack: st.slice(0, 900) });
  window.__lastHash = location.hash;
}, false);
window.__lastHash = location.hash;
'installed'`)

console.log('探针已装，复位到 #/ai 后静置观察 15 秒…')
await ev(`location.hash='#/ai'`)
await sleep(15000)

const logs = await ev('JSON.stringify(window.__navLog || [], null, 1)')
try {
  const arr = JSON.parse(String(logs))
  if (!arr.length) { console.log('期间没有任何 hashchange —— 说明跳转不是改 hash 发生的') }
  for (const n of arr) {
    console.log(`\n=== hashchange: ${n.from} -> ${n.to} ===`)
    console.log(n.stack)
  }
} catch { console.log('读取失败: ' + logs) }
console.log('\n最终 hash =', await ev('location.hash'))
adb(['forward', '--remove', `tcp:${PORT}`])
process.exit(0)
