// diag-sheet-footer-hit.mjs — 弹窗底部「创建/取消」按钮的点命中元素是谁？
//
// 背景（2026-10-01 13:15）：新 APK 上 tasks-crud 已经能打开创建弹窗、
// 填进标题、「创建」按钮也解禁了（enabled=1），可点下去弹窗不关。
// 现场量到按钮 bounds [136,1490][234,1568]，**中心 (185,1529) 落在底部主导航
// （主导航 View bounds [0,1496][720,1640]）之内**。
// flashcards-write.yaml 的注释里记过同类现象：「底部 .primary 在 100% 处，
// 会被主导航压住」——如果这里是同一个成因，那么它不是 tasks 独有的偶发，
// 而是一类布局缺陷。
//
// 判据：elementFromPoint 落在 sheet 之外 ⇒ 触摸被主导航吃掉（产品缺陷）；
// 落在 sheet 之内 ⇒ 布局没问题，失败另有原因（合成点击没被 WebView 当 click）。
// 两种结论对应的处置完全不同，所以必须先分开。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9613'
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
const send = (method, params = {}, ms = 12000) => new Promise((r) => {
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

const PROBE = `(function(){
  var sheet=document.querySelector('.bottom-sheet');
  if(!sheet) return JSON.stringify({open:false});
  var btns=Array.prototype.slice.call(sheet.querySelectorAll('button'));
  var pick=btns.filter(function(b){return (b.textContent||'').trim()==='创建' || (b.textContent||'').trim()==='取消'});
  var nav=document.querySelector('.bottom-nav') || document.querySelector('[aria-label="主导航"]');
  var navRect=nav?nav.getBoundingClientRect():null;
  var sr=sheet.getBoundingClientRect();
  var out={open:true, sheetRect:[Math.round(sr.left),Math.round(sr.top),Math.round(sr.right),Math.round(sr.bottom)],
           navRect:navRect?[Math.round(navRect.left),Math.round(navRect.top),Math.round(navRect.right),Math.round(navRect.bottom)]:null,
           vw:[window.innerWidth,window.innerHeight], btns:[]};
  pick.forEach(function(b){
    var r=b.getBoundingClientRect();
    var cx=Math.round(r.left+r.width/2), cy=Math.round(r.top+r.height/2);
    var hit=document.elementFromPoint(cx,cy);
    var inSheet=false;
    try { inSheet = !!(hit && sheet.contains(hit)); } catch(e){}
    out.btns.push({txt:(b.textContent||'').trim(), rect:[Math.round(r.left),Math.round(r.top),Math.round(r.right),Math.round(r.bottom)],
      center:[cx,cy], disabled:b.disabled===true,
      hit: hit ? (hit.tagName.toLowerCase()+'.'+String(hit.className||'').slice(0,50)) : 'null',
      hitInSheet: inSheet,
      sheetZ: getComputedStyle(sheet).zIndex, navZ: nav?getComputedStyle(nav).zIndex:'n/a'});
  });
  return JSON.stringify(out);
})()`

let p = await ev(PROBE)
if (p.err || !p.val || JSON.parse(p.val).open === false) {
  console.log('弹窗当前没开，先用 DOM 打开：', (await ev(`(function(){var o=document.querySelector('.bottom-sheet-overlay');if(o){o.remove();return 'removed'};var b=Array.prototype.slice.call(document.querySelectorAll('button')).filter(function(x){return (x.textContent||'').trim()==='+ 新任务'})[0];if(b){b.click();return 'opened'}return 'NO_BTN'})()`)).val)
  await sleep(1800)
  p = await ev(PROBE)
}
console.log(p.err || JSON.stringify(JSON.parse(p.val), null, 1))
ws.close()
process.exit(0)
