// 用**系统级真实输入**（adb input tap/text）走完「创建主密码」弹窗。
//
// 为什么不用 CDP 设值：MasterPasswordDialog.vue 用标准 v-model，
// 但实测连续设值两个密码框后组件仍报「两次输入的主密码不一致」——
// DOM 的 value 对了，Vue 组件内部状态没同步上。
// 系统级输入走的是真实事件通路，v-model 一定收得到。
// 限制：adb input text 只吃 ASCII，所以主密码用 ASCII 串。
import { execFileSync } from 'node:child_process'

const PKG = 'com.kaixuan.opencode.pocket'
const S = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PORT = process.env.POCKET_CDP_PORT || '9426'
const MASTER_ASCII = process.env.POCKET_MASTER_ASCII || 'testpass123'
const adb = (a, t = 60000) =>
  execFileSync('C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe', ['-s', S, ...a], {
    encoding: 'utf8', timeout: t, maxBuffer: 33554432,
  })
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const pid = adb(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
await new Promise((r) => ws.addEventListener('open', r))
const ev = async (x, ms = 15000) => {
  const i = ++id
  const v = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r({ __t: 1 }) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: x, returnByValue: true, awaitPromise: true } }))
  })
  if (v?.__t) return '(frozen)'
  return v?.result?.value
}

const dialog = async () => await ev(`(document.body.innerText||'').indexOf('创建主密码') >= 0`)
console.log('弹窗在:', await dialog())

// ⚠️ 实测坑：按 DOM 顺序取「第 1、2 个 password 框」拿到的是
// [[360,1050],[360,728]] —— 第 2 个在第 1 个**上方**（DOM 顺序 ≠ 视觉顺序），
// 拿这个顺序去点会点错框，表现为「两次输入的主密码不一致」。
// ⇒ 改成按 **placeholder 精确取每个框自己的坐标**，不靠顺序。
const boxes = JSON.parse(await ev(`(function(){
  var ins = Array.from(document.querySelectorAll('input'))
  function boxOf(re){
    var el = ins.filter(function(i){ return re.test(i.placeholder||'') })[0]
    if (!el) return null
    var r = el.getBoundingClientRect(), dpr = window.devicePixelRatio
    return [Math.round((r.x + r.width/2) * dpr), Math.round((r.y + r.height/2) * dpr), el.placeholder]
  }
  var p1 = boxOf(/至少\\s*8\\s*位|^主密码$|至少/)
  var p2 = boxOf(/再次|确认主密码|重复/)
  return JSON.stringify([p1, p2])
})()`))
const list = Array.isArray(boxes) ? boxes : []
if (list.length < 2 || !list[0] || !list[1]) {
  console.log('按 placeholder 取坐标失败：', JSON.stringify(boxes))
  // 退化：按视觉 y 升序取两个 password 框
  const fallback = JSON.parse(await ev(`(function(){
    var ins = Array.from(document.querySelectorAll('input')).filter(function(i){ return (i.type||'')==='password' })
    if (ins.length < 2) return '[]'
    var dpr = window.devicePixelRatio
    return JSON.stringify(ins.map(function(el){
      var r = el.getBoundingClientRect()
      return [Math.round((r.x + r.width/2) * dpr), Math.round((r.y + r.height/2) * dpr), el.placeholder]
    }).sort(function(a,b){ return a[1] - b[1] }))
  })()`))
  if (!Array.isArray(fallback) || fallback.length < 2) { console.log('退化取坐标也失败:', fallback); process.exit(3) }
  console.log('⚠️ 用按 y 升序的退化方案:', JSON.stringify(fallback))
  list.length = 0
  list.push(...fallback)
}
console.log('密码框坐标(设备像素, 已按视觉顺序):', JSON.stringify(list))

// ⚠️ 实测：坐标点下去**没聚焦到目标框** —— 输入后 length 一直是 [8,11,0]，
// 说明两次点击都落在同一个框上。坐标换算在这个滚动过的页面上不可靠。
// 改用「CDP 先 focus 到目标元素（这一步是确定性的，浏览器内部行为），
// 再用系统级 input text 敲键盘」：focus 保证对，input text 保证 v-model 收到。
for (const [i, [x, y]] of boxes.entries()) {
  // 1) CDP focus 目标框（按 placeholder 精确定位，不靠坐标）
  const focused = await ev(`(function(){
    try {
      var ins = Array.from(document.querySelectorAll('input'))
      var el = ins.filter(function(e){ return (e.placeholder||'') === ${JSON.stringify(boxes[i][2])} })[0]
      if (!el) return 'no-el'
      el.focus()
      return document.activeElement === el ? 'focused' : 'focus-failed'
    } catch (e) { return 'err:' + String(e).slice(0,80) }
  })()`)
  if (focused !== 'focused') { console.log(`  框${i + 1} focus 失败：${focused}`); continue }
  await sleep(500)
  // 2) 清空 + 系统级敲键盘
  adb(['shell', 'input', 'keyevent', '123'])
  for (let k = 0; k < 40; k++) adb(['shell', 'input', 'keyevent', '67'])
  await sleep(400)
  adb(['shell', 'input', 'text', MASTER_ASCII])
  await sleep(800)
  const lens = await ev(`JSON.stringify(Array.from(document.querySelectorAll('input'))
      .filter(function(i){ return (i.type||'')==='password' })
      .map(function(e){ return e.value.length }))`)
  console.log(`  框${i + 1}（${boxes[i][2]}）focus+输入后，长度=${lens}`)
}
await sleep(500)
// 收起软键盘，避免它盖住「确认」按钮
adb(['shell', 'input', 'keyevent', '111'])
await sleep(800)

const err = await ev(`(function(){
  var b=(document.body.innerText||'').replace(/\\s+/g,' ')
  var m=b.match(/(主密码至少需要|两次输入的主密码不一致|不一致|至少需要)/)
  return m ? m[0] : 'no-error'
})()`)
console.log('当前校验提示:', err)

// 点确认
const btn = JSON.parse(await ev(`(function(){
  var b = Array.from(document.querySelectorAll('button'))
            .filter(function(x){ return /^(确认|确定)$/.test((x.textContent||'').trim()) })[0]
  if (!b) return 'null'
  var r = b.getBoundingClientRect(), dpr = window.devicePixelRatio
  return JSON.stringify([Math.round((r.x + r.width/2) * dpr), Math.round((r.y + r.height/2) * dpr)])
})()`))
if (!btn) { console.log('找不到确认按钮'); process.exit(4) }
console.log('确认按钮坐标:', btn)
adb(['shell', 'input', 'tap', String(btn[0]), String(btn[1])])
await sleep(3000)
console.log('弹窗还在吗:', await dialog())
console.log('hash:', await ev('location.hash'))
ws.close()
