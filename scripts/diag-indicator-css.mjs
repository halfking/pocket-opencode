// diag-indicator-css.mjs — 读设备上**真正生效**的 .refresh-indicator 样式。
//
// 为什么必须读设备而不是仓库（2026-10-01 13:10）：
//   仓库源码与 frontend/dist 的构建产物里，.refresh-indicator 只有一条规则，
//   明确写着 `pointer-events:none; height:56px`。
//   但设备上 getComputedStyle 读出来是 `pe=auto zi=1 pos=absolute h=0px`
//   —— position/z-index 对上了，pointer-events/height ��对不上。
//   这说明设备上跑的那份 CSS 与仓库当前产物**不是同一份**（APK 是更早构建的），
//   或者有第三条规则在覆盖。不读清楚就动手改源码，等于改了一份没在设备上跑的文件。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9612'
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
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
if (!(await Promise.race([
  new Promise((r) => ws.addEventListener('open', () => r(true), { once: true })),
  new Promise((r) => setTimeout(() => r(false), 10000)),
]))) { console.log('CDP_OPEN_TIMEOUT'); process.exit(5) }
const ev = async (x, ms = 20000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { err: 'TIMEOUT' }
  if (v?.exceptionDetails) return { err: String(v.exceptionDetails.exception?.description || '').slice(0, 200) }
  return { val: v?.result?.value }
}

const r = await ev(`(function(){
  var out = { rules: [], sheets: 0, el: null, inlineStyle: null, cls: null };
  for (var i=0;i<document.styleSheets.length;i++){
    var sh=document.styleSheets[i]; out.sheets++;
    var rules; try { rules = sh.cssRules } catch(e) { continue }
    if (!rules) continue;
    for (var j=0;j<rules.length;j++){
      var t=rules[j];
      if (t.selectorText && /refresh-indicator|refresh-text/.test(t.selectorText)) {
        out.rules.push((t.selectorText)+' {'+t.style.cssText+'}');
      }
    }
  }
  var e=document.querySelector('.refresh-indicator');
  if (e) {
    out.cls = e.className;
    out.inlineStyle = e.getAttribute('style') || '';
    var s=getComputedStyle(e);
    out.computed = { pe:s.pointerEvents, h:s.height, z:s.zIndex, pos:s.position, of:s.overflow };
  }
  var t=document.querySelector('.refresh-text');
  if (t) { var ts=getComputedStyle(t); out.textComputed={ pe:ts.pointerEvents, pos:ts.position }; }
  return JSON.stringify(out);
})()`)
console.log(r.err || r.val)
ws.close()
process.exit(0)
