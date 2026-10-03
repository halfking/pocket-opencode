// 抓「点新建笔记」之后的路由时序：是先到编辑页再被弹走，还是直接跳到别处。
// 同时挂 Page/Runtime 事件与 console 报错，避免只看到结果看不到过程。
import { execFileSync } from 'node:child_process'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = '9414'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, ['-s', S, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const pid = adb(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`]).split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0; const pending = new Map()
const logs = []
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  if (m.method === 'Runtime.consoleAPICalled' && ['error', 'warning'].includes(m.params.type)) {
    logs.push(`[console.${m.params.type}] ` + m.params.args.map((a) => a.value ?? a.description ?? a.type).join(' ').slice(0, 200))
  }
  if (m.method === 'Runtime.exceptionThrown') {
    logs.push('[exception] ' + (m.params?.exceptionDetails?.exception?.description || '').slice(0, 240))
  }
})
await new Promise((r) => ws.addEventListener('open', r))
const send = (method, params = {}, ms = 10000) => new Promise((r) => {
  const i = ++id
  const t = setTimeout(() => { pending.delete(i); r(null) }, ms)
  pending.set(i, (v) => { clearTimeout(t); r(v) })
  ws.send(JSON.stringify({ id: i, method, params }))
})
const ev = async (x, ms = 10000) => (await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }, ms))?.result?.value
await send('Runtime.enable')
await send('Page.enable')

// 复位到笔记列表
await ev(`location.hash='#/pkm/today'`)
await sleep(3000)
console.log('起点 hash =', await ev('location.hash'))
console.log('页面上「新建笔记」元素：')
console.log(await ev(`JSON.stringify(Array.prototype.slice.call(document.querySelectorAll('button,a,[role=button]'))
  .filter(function(e){return /新建笔记/.test(e.textContent||e.getAttribute('aria-label')||'')})
  .map(function(e){var r=e.getBoundingClientRect();
    return {tag:e.tagName, txt:(e.textContent||'').trim().slice(0,12), aria:e.getAttribute('aria-label')||'',
            cls:(e.className||'').toString().slice(0,40), rect:[Math.round(r.x),Math.round(r.y),Math.round(r.width),Math.round(r.height)]};}), null, 1)`))

console.log('\n--- 点击后 8 秒内的 hash 变化 ---')
const before = await ev('location.hash')
await ev(`(function(){
  var h=Array.prototype.slice.call(document.querySelectorAll('button,a,[role=button]'))
    .find(function(e){return /新建笔记/.test((e.textContent||'')+(e.getAttribute('aria-label')||''))});
  if(!h) return 'NOT_FOUND';
  h.click(); return 'CLICKED';
})()`)
const seen = []
for (let i = 0; i < 16; i++) {
  await sleep(500)
  const h = await ev('location.hash')
  if (h !== seen[seen.length - 1]) {
    seen.push(h)
    console.log(`  +${((i + 1) * 0.5).toFixed(1)}s  hash=${h}`)
  }
}
if (seen.length === 0) console.log(`  hash 无变化（起点 ${before}）`)
console.log('\n--- console 报错/告警 ---')
console.log(logs.length ? logs.slice(0, 12).join('\n') : '  （无）')
console.log('\n--- 落地页正文 ---')
console.log((await ev(`(document.body.innerText||'').replace(/\\n+/g,' | ').slice(0,260)`)))
adb(['forward', '--remove', `tcp:${PORT}`])
process.exit(0)
