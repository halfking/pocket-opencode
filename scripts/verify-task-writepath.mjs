// 任务写路径真机验证（补 verifier 缺口：任务/会话的编辑、删除此前未验证）。
// 覆盖：创建(POST) -> 状态变更(PATCH) -> 子任务(POST) -> 评论(POST) -> 删除(DELETE)
// 判据原则：
//  1) 状态读优先——每步都直接查 PostgreSQL 行，不以 toast/提示条作为唯一依据。
//  2) 每步都用本次唯一 TITLE 做锚点，避免与既有任务混淆。
//  3) 失败即停，不把半成品当通过。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9355'
const MASTER = process.env.POCKET_MASTER || 'PocketTest2026'
const PSQL = process.env.POCKET_PSQL || 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
// psql 兜底串必须是纯 ASCII——中文经 ANSI 码页会报 invalid byte sequence
const psql = (sql) => execFileSync(PSQL, ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-t', '-A', '-c', sql], { encoding: 'utf8' }).trim()

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
async function goto(hash, ms = 15000) {
  await ev(`location.hash=${JSON.stringify(hash)}`)
  const d = Date.now() + ms
  while (Date.now() < d && (await ev('location.hash')) !== hash) await sleep(300)
  await sleep(2200)
}
async function typeInto(sel, text) {
  const box = await ev(`(function(){var e=document.querySelector(${JSON.stringify(sel)});if(!e)return null;var r=e.getBoundingClientRect();return JSON.stringify({x:r.left+r.width/2,y:r.top+r.height/2})})()`)
  if (!box) return 'NOT_FOUND'
  const { x, y } = JSON.parse(box)
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', clickCount: 1 })
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', clickCount: 1 })
  await sleep(300)
  await send('Input.insertText', { text })
  await sleep(600)
  return 'typed'
}
const clickText = (re) =>
  ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(x=>new RegExp(${JSON.stringify(re.source)}).test(x.textContent||''));if(b){b.click();return 1}return 0})()`)

const TITLE = 'TW-' + String(Date.now()).slice(-7)
const SUB = 'SUB-' + TITLE
const CMT = 'CMT-' + TITLE
console.log('task title =', TITLE)

const pgTaskId = () => psql(`select id from opencode_pocket.tasks where title = '${TITLE}' limit 1`)
const pgSubCount = (tid) => psql(`select count(*) from opencode_pocket.tasks where title = '${SUB}' and parent_id = '${tid}'`)
// 评论正文在 work_item_events.payload (jsonb) 里，没有独立 comment 列
const pgEventCount = (tid) => psql(`select count(*) from opencode_pocket.work_item_events where task_id = '${tid}' and payload::text like '%${CMT}%'`)
const pgStatus = (tid) => psql(`select status from opencode_pocket.tasks where id = '${tid}'`)

await ensureUnlocked()
await goto('#/ai')

// ---------- 1. 创建 ----------
// 注意：必须用 button.link-btn + 文案精确匹配「+ 新任务」。
// 早先用 /\\+\\s*新任务/ 去 find(button,div) 会匹配到无 class 的空 DIV（祖先节点），
// 点它不会打开弹窗——那是夹具错，不是产品 bug（route 稳定性已单独验过 4/4）。
const opened = await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='+ 新任务'});if(!b)return 'NO_BTN';b.click();return 'CLICKED'})()`)
console.log('打开创建弹窗 =', opened)
await sleep(2000)
check('创建弹窗已打开', (await ev(`document.querySelectorAll('.create-task-form').length`)) === 1,
  `forms=${await ev(`document.querySelectorAll('.create-task-form').length`)}`)
await typeInto('.create-task-form input[placeholder="输入任务标题"]', TITLE)
await typeInto('.create-task-form textarea[placeholder="输入任务描述"]', 'desc-' + TITLE)
await sleep(400)
// 「创建」按钮：只在可见按钮里找，避免点到隐藏的
const created = await ev(`(function(){var bs=Array.prototype.slice.call(document.querySelectorAll('button')).filter(function(b){return !!b.offsetParent && (b.textContent||'').trim()==='创建'});if(!bs.length)return 0;bs[0].click();return 1})()`)
console.log('点「创建」=', created)
await sleep(3000)
await goto('#/ai')

let tid = pgTaskId()
check('创建：PG 落库', !!tid, `tasks.id=${tid || 'NONE'}`)
const inList = await ev(`(function(){var cs=document.querySelectorAll('.task-card');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(TITLE)})>=0)return true}return false})()`)
const cardN = await ev(`document.querySelectorAll('.task-card').length`)
check('创建：列表回显', !!inList, `cards=${cardN} found=${!!inList}`)

if (!tid) { console.log('ABORT: 任务未落库，后续步骤不执行'); process.exit(1) }
console.log('status after create =', await pgStatus(tid))

// 判据自证：后面「删除后不再回显」只有在「先显示过」的前提下才有意义。
// 否则它会空过（因为从没显示过），给出假绿。显示不了就直接停。
if (!inList) {
  console.log('ABORT: 任务已落库但列表不显示，后续 status/delete 判据无法自证，不执行')
  const passed = checks.filter((c) => c.pass).length
  console.log(`\n=== 汇总 ===\n${passed}/${checks.length} 通过（已提前中止）`)
  ws.close()
  process.exit(1)
}

// ---------- 2. 状态变更 (PATCH) ----------
await ev(`(function(){var cs=document.querySelectorAll('.task-card');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(TITLE)})>=0){cs[i].click();return 1}}return 0})()`)
await sleep(3000)
const onDetail = (await ev('location.hash')) || ''
check('进入任务详情', onDetail.indexOf('#/tasks/') === 0, onDetail)

const completeBtn = await ev(`(function(){var b=document.querySelector('.action-btn.complete');if(b){b.click();return 1}return 0})()`)
console.log('点「完成」= ', completeBtn)
await sleep(3000)
const st1 = await pgStatus(tid)
check('状态变更 PATCH 落库', st1 === 'completed', `status=${st1} (期望 completed)`)

// ---------- 3. 子任务 (POST) ----------
const subInput = await ev(`(function(){var es=document.querySelectorAll('input');for(var i=0;i<es.length;i++){if(/子任务|新子任务/.test(es[i].placeholder||''))return es[i].placeholder}return ''})()`)
console.log('子任务输入框 placeholder =', JSON.stringify(subInput))
let subOk = false
if (subInput) {
  await typeInto(`input[placeholder=${JSON.stringify(subInput)}]`, SUB)
  await sleep(400)
  await clickText(/添加|新增|创建/)
  await sleep(3000)
  subOk = String(await pgSubCount(tid)) === '1'
}
check('子任务创建落库', subOk, `pg count=${await pgSubCount(tid)}`)

// ---------- 4. 评论 (POST) ----------
const cmtInput = await ev(`(function(){var es=document.querySelectorAll('textarea,input');for(var i=0;i<es.length;i++){if(/评论|说点什么|留言/.test(es[i].placeholder||''))return es[i].placeholder}return ''})()`)
console.log('评论输入框 placeholder =', JSON.stringify(cmtInput))
let cmtOk = false
if (cmtInput) {
  await typeInto(`[placeholder=${JSON.stringify(cmtInput)}]`, CMT)
  await sleep(400)
  await clickText(/发送|提交|评论/)
  await sleep(3000)
  cmtOk = String(await pgEventCount(tid)) === '1'
}
check('评论落库', cmtOk, `pg count=${await pgEventCount(tid)}`)

// ---------- 5. 删除 (DELETE) ----------
const del = await ev(`(function(){var b=document.querySelector('.action-btn.delete');if(b){b.click();return 1}return 0})()`)
console.log('点「删除」= ', del)
await sleep(1500)
await ev(`(function(){var bs=Array.prototype.slice.call(document.querySelectorAll('button'));for(var i=0;i<bs.length;i++){var t=(bs[i].textContent||'').trim();if(t==='删除'||t==='确认删除'||/确定删除/.test(t)){bs[i].click();return 1}}return 0})()`)
await sleep(3000)
const pgGone = await psql(`select count(*) from opencode_pocket.tasks where id = '${tid}'`)
check('删除后 PG 无该行', String(pgGone) === '0', `count=${pgGone}`)
await goto('#/ai')
const stillThere = await ev(`(function(){var cs=document.querySelectorAll('.task-card');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(TITLE)})>=0)return true}return false})()`)
check('删除后列表不再回显', !stillThere, `found=${!!stillThere}`)

const passed = checks.filter((c) => c.pass).length
console.log(`\n=== 汇总 ===\n${passed}/${checks.length} 通过`)
ws.close()
process.exit(0)
