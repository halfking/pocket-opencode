// diag-tap-newtask.mjs — 「+ 新任务」在**空列表**下点不动，是坐标问题还是真触摸被吞？
//
// 观察到的事实（2026-10-01 13:00）：
//   列表有 7 条时分诊区展开，+ 新任务 在 y≈1368 → tap 成功，弹窗打开。
//   列表为空（运行中 0）时 + 新任务 落到 y≈208-256，**正好压在「下拉刷新」
//   提示文字 bounds [312,214][408,250] 的中心** → tap 报 COMPLETED 但弹窗不开。
//   PullToRefresh.vue 里 .refresh-indicator 写的是 pointer-events: none，
//   按理说触摸应该穿透，所以这里必须实测，不能靠推理下结论。
//
// 三种投递方式各试一次，用同一块按钮、同一个坐标：
//   A. Input.dispatchTouchEvent（最接近真实手指）
//   B. Input.dispatchMouseEvent（Maestro/CDP 合成点击走的就是这条）
//   C. DOM .click()（已知是好的，作对照）
// 每次都先复位（移除残留弹窗），再投递，再量 .create-task-form 是否出现。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9610'
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
  // 传 m.result：Runtime.evaluate 的响应是 {id, result:{result:{value}}}
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
if (!(await Promise.race([
  new Promise((r) => ws.addEventListener('open', () => r(true), { once: true })),
  new Promise((r) => setTimeout(() => r(false), 10000)),
]))) { console.log('CDP_OPEN_TIMEOUT'); process.exit(5) }
const send = (method, params = {}, ms = 10000) => new Promise((r) => {
  const i = ++id
  const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
  pending.set(i, (y) => { clearTimeout(t); r(y) })
  ws.send(JSON.stringify({ id: i, method, params }))
})
const ev = async (x, ms = 12000) => {
  const r = await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }, ms)
  if (r?.__t) return { err: 'TIMEOUT' }
  if (r?.exceptionDetails) return { err: String(r.exceptionDetails.exception?.description || '').slice(0, 140) }
  return { val: r?.result?.value }
}

const rect = async () => JSON.parse((await ev(`(function(){
  var b=Array.prototype.slice.call(document.querySelectorAll('button')).filter(function(x){return (x.textContent||'').trim()==='+ 新任务'})[0];
  if(!b) return '{}';
  var r=b.getBoundingClientRect();
  var hint=document.querySelector('.refresh-text');
  var hr=hint?hint.getBoundingClientRect():null;
  var cards=document.querySelectorAll('.task-card').length;
  return JSON.stringify({x:Math.round(r.left+r.width/2), y:Math.round(r.top+r.height/2),
    rect:[Math.round(r.left),Math.round(r.top),Math.round(r.right),Math.round(r.bottom)],
    hint:hr?[Math.round(hr.left),Math.round(hr.top),Math.round(hr.right),Math.round(hr.bottom)]:null,
    cards:cards, dpr:window.devicePixelRatio, vw:[window.innerWidth,window.innerHeight],
    indicatorPE:(function(){var e=document.querySelector('.refresh-indicator');return e?getComputedStyle(e).pointerEvents:'n/a'})(),
    contentTransform:(function(){var e=document.querySelector('.refresh-content');return e?getComputedStyle(e).transform:'n/a'})()});
})()`)).val || '{}')

const reset = async () => { await ev(`(function(){var o=document.querySelector('.bottom-sheet-overlay');if(o){o.remove();return 1}return 0})()`); await sleep(600) }
const opened = async () => (await ev(`document.querySelectorAll('.create-task-form').length`)).val

console.log('页面状态:', JSON.stringify(await rect()))

const g = await rect()
const { x, y } = g
if (!x) { console.log('找不到「+ 新任务」按钮'); process.exit(6) }
// CDP 的 Input 坐标是 **CSS 像素**（视口 360x820），不是设备物理像素
console.log(`\n目标按钮中心 CSS=(${x},${y})  dpr=${g.dpr}  视口=${JSON.stringify(g.vw)}`)

// ---- A. 真实触摸 ----
await reset()
await send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] })
await sleep(60)
await send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
await sleep(1500)
console.log(`A. dispatchTouchEvent  → create-task-form = ${await opened()}`)

// ---- B. 合成鼠标点击（Maestro 走的就是这条）----
await reset()
await send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', clickCount: 1 })
await sleep(60)
await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', clickCount: 1 })
await sleep(1500)
console.log(`B. dispatchMouseEvent → create-task-form = ${await opened()}`)

// ---- C. DOM .click() 对照 ----
await reset()
await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).filter(function(x){return (x.textContent||'').trim()==='+ 新任务'})[0];if(b){b.click();return 1}return 0})()`)
await sleep(1500)
console.log(`C. DOM .click()       → create-task-form = ${await opened()}   ← 对照组`)

// ---- D. 往下滚动后再点：验证「被下拉刷新提示区盖住」这个解释 ----
await reset()
await ev(`(function(){var c=document.querySelector('.refresh-content');if(c){c.scrollTop=260;return c.scrollTop}return -1})()`)
await sleep(900)
const g2 = await rect()
console.log(`\n滚动后按钮中心 CSS=(${g2.x},${g2.y})  cards=${g2.cards}`)
if (g2.x) {
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: g2.x, y: g2.y, button: 'left', clickCount: 1 })
  await sleep(60)
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: g2.x, y: g2.y, button: 'left', clickCount: 1 })
  await sleep(1500)
  console.log(`D. 滚动后 dispatchMouseEvent → create-task-form = ${await opened()}`)
}
ws.close()
process.exit(0)
