// 读当前视口尺寸与解锁页 EditText 的真实 bounds——用来校验 flow 里的百分比坐标
import { execFileSync } from 'node:child_process'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = '9418'
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
const vw = await ev('window.innerWidth'); const vh = await ev('window.innerHeight')
console.log(`CSS 视口: ${vw} x ${vh}   dpr=${await ev('window.devicePixelRatio')}`)
console.log('设备:', adb(['shell', 'wm', 'size']).trim().replace(/\r?\n/g, ' | '))
console.log('hash =', await ev('location.hash'))
console.log('正文 =', (await ev(`(document.body.innerText||'').replace(/\\n+/g,' | ').slice(0,160)`)))
console.log('\n输入类元素与其百分比坐标:')
console.log(await ev(`(function(){
  var W=${vw}, H=${vh};
  return JSON.stringify(Array.prototype.slice.call(document.querySelectorAll('input,textarea,[contenteditable=true]'))
    .map(function(e){var r=e.getBoundingClientRect();
      return {tag:e.tagName, type:e.type||'', ph:e.placeholder||'', cls:(e.className||'').toString().slice(0,30),
        rect:[Math.round(r.x),Math.round(r.y),Math.round(r.width),Math.round(r.height)],
        pct:Math.round((r.x+r.width/2)/W*100)+','+Math.round((r.y+r.height/2)/H*100)};}), null, 1);
})()`))
adb(['forward', '--remove', `tcp:${PORT}`])
process.exit(0)
