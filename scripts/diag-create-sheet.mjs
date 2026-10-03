// diag-create-sheet.mjs — 判定「点 + 新任务 后创建弹窗没出现」到底卡在哪一环。
//
// 为什么要做这个分离：2026-10-01 12:20 实测，Maestro 点「+ 新任务」报 COMPLETED
// （加不加 retryTapIfNoChange 都一样），随后 20s 内「创建任务」始终不出现。
// 两个完全不同的解释，必须用单变量实验分开：
//
//   (a) 合成 tap 根本没送达按钮 → WebView 没把它当 click（闪卡建卡组踩过同类坑）
//   (b) 弹窗**渲染在可访问性树之外** → BottomSheet 用了 <Teleport to="body">，
//       节点不在 #app 子树里，Maestro 读 Android 无障碍树时压根看不到
//
// 做法：用 DOM .click()（已知在这台设备上是好的，verify-task-writepath.mjs 靠它）
// 打开弹窗，再检查弹窗里的元素在无障碍树里能不能被 Maestro 匹配。
// 若 (a) 成立：.click() 之后 DOM 里有弹窗。
// 若 (b) 也成立：DOM 里有弹窗，但 Maestro 仍然看不见 —— 那就得换断言策略。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9600'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 30000) => execFileSync(ADB, ['-s', SERIAL, ...a], { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const adbSoft = (a) => { try { return adb(a, 10000) } catch { return '' } }

const pid = adbSoft(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
const sock = socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]
adbSoft(['forward', `tcp:${PORT}`, `localabstract:${sock}`])

let list = null
for (let i = 0; i < 4; i++) {
  try { list = await (await fetch(`http://127.0.0.1:${PORT}/json/list`, { signal: AbortSignal.timeout(8000) })).json(); break } catch { await sleep(2000) }
}
const page = list?.find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE_TARGET / CDP_UNREACHABLE'); process.exit(4) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  // ⚠️ 传 m.result 而不是整条 m：Runtime.evaluate 的响应是
  //    {id, result:{result:{value}}}，传整条再读 .result.value 恒为 undefined，
  //    会被误判成「CDP 断了」。踩过。
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
if (!(await Promise.race([
  new Promise((r) => ws.addEventListener('open', () => r(true), { once: true })),
  new Promise((r) => setTimeout(() => r(false), 10000)),
]))) { console.log('CDP_OPEN_TIMEOUT'); process.exit(5) }
const ev = async (x, ms = 12000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return { err: 'TIMEOUT' }
  if (v?.exceptionDetails) return { err: String(v.exceptionDetails.exception?.description || v.exceptionDetails.text).slice(0, 200) }
  return { val: v?.result?.value }
}

const show = (n, r) => console.log(`${n}: ${r.err ? 'ERR ' + r.err : JSON.stringify(r.val)}`)

console.log(`pid=${pid} hash=${(await ev('location.hash')).val}`)

const before = await ev(`JSON.stringify({
  form: document.querySelectorAll('.create-task-form').length,
  sheet: document.querySelectorAll('.bottom-sheet').length,
  sheetInApp: document.querySelectorAll('#app .bottom-sheet').length,
  teleportTarget: !!document.querySelector('body > .bottom-sheet-overlay'),
  btn: (function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).filter(function(x){return (x.textContent||'').trim()==='+ 新任务'});return b.length})(),
})`)
show('BEFORE', before)

const clicked = await ev(`(function(){
  var b=Array.prototype.slice.call(document.querySelectorAll('button')).filter(function(x){return (x.textContent||'').trim()==='+ 新任务'})[0];
  if(!b) return 'NO_BTN';
  var r=b.getBoundingClientRect();
  b.click();
  return 'clicked at '+Math.round(r.left+r.width/2)+','+Math.round(r.top+r.height/2);
})()`)
show('CLICK', clicked)
await sleep(2500)

const after = await ev(`JSON.stringify({
  form: document.querySelectorAll('.create-task-form').length,
  sheet: document.querySelectorAll('.bottom-sheet').length,
  title: (function(){var h=document.querySelector('.bottom-sheet .sheet-title');return h?h.textContent.trim():''})(),
  overlayInBody: !!document.querySelector('body > .bottom-sheet-overlay'),
  overlayInApp: !!document.querySelector('#app .bottom-sheet-overlay'),
  // 弹窗里所有可交互控件的 css 矩形 —— 用来算 Maestro 的 point 百分比
  ctrls: Array.prototype.slice.call(document.querySelectorAll('.bottom-sheet input,.bottom-sheet textarea,.bottom-sheet select,.bottom-sheet button,.bottom-sheet .sheet-title')).map(function(e){
    var r=e.getBoundingClientRect();
    return {tag:e.tagName.toLowerCase(), ph:e.getAttribute('placeholder')||'', txt:(e.textContent||'').trim().slice(0,12),
            dis: e.disabled===true, rect:[Math.round(r.left),Math.round(r.top),Math.round(r.right),Math.round(r.bottom)]};
  }),
  vw: [window.innerWidth, window.innerHeight],
})`)
const a = after.val ? JSON.parse(after.val) : null
if (a) {
  console.log(`AFTER: form=${a.form} sheet=${a.sheet} title=${JSON.stringify(a.title)} overlayInBody=${a.overlayInBody} overlayInApp=${a.overlayInApp} vw=${a.vw}`)
  console.log('控件（css 矩形 → Maestro point 百分比）：')
  for (const c of a.ctrls) {
    const cx = Math.round((c.rect[0] + c.rect[2]) / 2)
    const cy = Math.round((c.rect[1] + c.rect[3]) / 2)
    console.log(`  ${c.tag.padEnd(8)} ph=${JSON.stringify(c.ph).padEnd(22)} txt=${JSON.stringify(c.txt).padEnd(14)} dis=${c.dis ? 1 : 0} rect=${JSON.stringify(c.rect).padEnd(26)} → ${Math.round(cx / a.vw[0] * 100)}%,${Math.round(cy / a.vw[1] * 100)}%`)
  }
} else {
  show('AFTER', after)
}
ws.close()
process.exit(0)
