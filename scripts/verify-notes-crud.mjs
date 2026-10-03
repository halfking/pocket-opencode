// 笔记完整 CRUD 真机回归（BUG-AK 之后）。
// 为什么单独写：BUG-AK 修的正是「写进 ws_user-admin、查 default」的错分区，
// 之前 verify-notes-inputtext 只覆盖「新建→列表可见」。这次要确认
// 编辑（updateNote）与删除（deleteNote）也真的作用在同一个分区上——
// 修复前 deleteNote 的软删打在 default 分区，真行删不掉且无任何报错。
// 判据全部是状态读（列表 DOM / localStorage 行数），不使用 toast 等反馈类信号。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9345'
const MASTER = process.env.POCKET_MASTER || 'PocketTest2026'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
await new Promise((r) => ws.addEventListener('open', r))
const send = (method, params = {}) =>
  new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method, params })) })
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }))?.result?.value
await send('Runtime.enable')

const checks = []
const check = (n, pass, d) => { checks.push({ n, pass }); console.log(`${pass ? 'PASS' : 'FAIL'}  ${n}${d ? '  — ' + d : ''}`) }

async function ensureUnlocked() {
  if (await ev('!!document.querySelector(\'input[placeholder*="主密码"]\')')) {
    await ev(`(function(){var e=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set;s.call(e,${JSON.stringify(MASTER)});e.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`)
    await sleep(700)
    await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(x=>(x.textContent||'').trim()==='解锁');if(b)b.click();return 1})()`)
    await sleep(5000)
  }
}
async function goto(hash) {
  await ev(`location.hash=${JSON.stringify(hash)}`)
  const d = Date.now() + 15000
  while (Date.now() < d && (await ev('location.hash')) !== hash) await sleep(300)
  await sleep(2200)
}
async function typeInto(sel, text) {
  const box = await ev(`(function(){var e=document.querySelector(${JSON.stringify(sel)});if(!e)return null;var r=e.getBoundingClientRect();return JSON.stringify({x:r.left+r.width/2,y:r.top+r.height/2})})()`)
  if (!box) return 'NF'
  const { x, y } = JSON.parse(box)
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', clickCount: 1 })
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', clickCount: 1 })
  await sleep(300)
  await send('Input.insertText', { text })
  await sleep(600)
  return 'typed'
}
const clickByText = (re) =>
  ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(x=>new RegExp(${JSON.stringify(re.source)}).test(x.textContent||''));if(b){b.click();return 1}return 0})()`)
const cardCount = () => ev(`document.querySelectorAll('.note-card').length`)
const hasTitle = (t) =>
  ev(`(function(){var cs=document.querySelectorAll('.note-card');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(t)})>=0)return true}return false})()`)
const cardText = (t) =>
  ev(`(function(){var cs=document.querySelectorAll('.note-card');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(t)})>=0)return cs[i].textContent}return ''})()`)

await ensureUnlocked()

// ---- 0. 基线：列表里预先存在的笔记数（用于判断 +1 / -1 是本轮产生的） ----
await goto('#/notes')
const base = await cardCount()
console.log('基线列表笔记数 =', base)

// ---- 1. 新建 ----
const TITLE = 'CRUD-' + String(Date.now()).slice(-6)
const BODY1 = 'body1-' + TITLE
await ev(`(function(){var e=document.querySelector('.notes-action[aria-label="新建笔记"]');if(e)e.click();return 1})()`)
await sleep(2500)
await typeInto('textarea[placeholder*="一句话概括"]', TITLE)
await typeInto('textarea[placeholder*="全屏编辑"]', BODY1)
await clickByText(/创建|保存/)
await sleep(2000)
await goto('#/notes')
const afterCreate = await cardCount()
check('新建后列表可见', await hasTitle(TITLE), `cards ${base} -> ${afterCreate}`)
check('新建使笔记数 +1', afterCreate === base + 1, `${base} -> ${afterCreate}`)

// ---- 2. 编辑：改正文后应回显新正文 ----
const before = await cardText(TITLE)
console.log('编辑前卡片文本 =', (before || '').slice(0, 80))
await ev(`(function(){var cs=document.querySelectorAll('.note-card');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(TITLE)})>=0){cs[i].click();return 1}}return 0})()`)
await sleep(2500)
const onDetail = (await ev('location.hash')) || ''
check('点卡片进入详情页', onDetail.indexOf('#/notes/') === 0, onDetail)
const editBtn = await clickByText(/编辑/)
check('详情页有编辑入口', editBtn === 1, `clicked=${editBtn}`)
await sleep(2000)
const BODY2 = 'body2-' + TITLE
// 追加到正文末尾，验证 update 生效
await typeInto('textarea[placeholder*="全屏编辑"]', ' ' + BODY2)
await clickByText(/保存/)
await sleep(2000)
await goto('#/notes')
const after = await cardText(TITLE)
check('编辑后列表回显新正文', !!after && after.indexOf(BODY2) >= 0, (after || '').slice(0, 90))

// ---- 3. 删除：真行必须消失（BUG-AK 修复前软删打错分区、静默不生效） ----
await ev(`(function(){var cs=document.querySelectorAll('.note-card');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(TITLE)})>=0){cs[i].click();return 1}}return 0})()`)
await sleep(2500)
await clickByText(/删除/)
await sleep(1200)
// 二次确认：点确认按钮（文案含「删除」且在弹层内）
const confirmed = await ev(`(function(){var bs=Array.prototype.slice.call(document.querySelectorAll('button'));for(var i=0;i<bs.length;i++){var t=(bs[i].textContent||'').trim();if(t==='删除'||t==='确认删除'||/确认删除/.test(t)){bs[i].click();return 1}}return 0})()`)
console.log('二次确认 clicked =', confirmed)
await sleep(2500)
await goto('#/notes')
const afterDelete = await cardCount()
check('删除后列表不再回显', !(await hasTitle(TITLE)), `cards=${afterDelete}`)
check('删除使笔记数 -1', afterDelete === base, `${afterCreate} -> ${afterDelete} (基线 ${base})`)

const passed = checks.filter((c) => c.pass).length
console.log(`\n=== 汇总 ===\n${passed}/${checks.length} 通过`)
ws.close()
// BUG-V19：原本写死 process.exit(0)，哪怕判出一堆 FAIL 退出码也是 0，
// 调用方（CI / 批量 runner）无从分辨「跑过了」与「全绿」。
// 改用 exitCode 按判定取值；不要用 process.exit()，那会跳过上面的 ws.close()。
process.exitCode = checks.some((c) => !c.pass) ? 1 : 0
