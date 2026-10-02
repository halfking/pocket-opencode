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
// PG schema：跟随后端配置（backend/internal/config/config.go 的 POCKET_PG_SCHEMA，默认值相同）。
// 写死 opencode_pocket 会让本脚本只能对着共享库跑 —— 失败时 SEED 就留在别人的库里。
const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket';
if (SCHEMA !== 'opencode_pocket') console.log(`PG schema = ${SCHEMA}（非共享库）`);
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

// 证伪模式：26/9 全绿本身不能证明判据有区分力，必须能证明「判据该红时真的会红」。
// --sabotage=skip-confirm 故意不点确认弹层的按钮 ⇒ 删除根本不会发生。
const SABOTAGE = (process.argv.find((a) => a.startsWith('--sabotage=')) || '').split('=')[1] || ''
if (SABOTAGE) console.log(`\n⚠️ 证伪模式：${SABOTAGE} —— 判据**应该**失败，失败才算这个模式跑对\n`)

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

// ⚠️ 2026-10-03 修正 BUG-V20 的**误报**根因：
//    原来是 `SUB = 'SUB-' + TITLE` / `CMT = 'CMT-' + TITLE`，
//    而「删除后列表不再回显」判据用 `indexOf(TITLE) >= 0` 搜 `.task-card` 文本。
//    子任务标题 `SUB-TW-xxx` **包含**父任务标题 `TW-xxx` ⇒
//    父任务删干净之后，**子任务那张卡照样命中**，于是判据报「列表没刷新」。
//
//    实际产品行为是对的：diag-task-refresh-observability 实测
//      确认点击后 +222ms 发出 GET /api/tasks，DOM 里那张卡 hitText=null（已消失），PG=0。
//    刷新链路（onActivated → consumeListDirty → handleRefresh → loadTasks）完全正常。
//
//    修法：子任务/评论用**独立**标识，不再内嵌 TITLE，从根上消除子串碰撞。
const RUN = String(Date.now()).slice(-7)
const TITLE = 'TW-' + RUN
const SUB = 'SB' + RUN      // 不含 TITLE
const CMT = 'CM' + RUN      // 不含 TITLE
console.log('task title =', TITLE)

// 「这张卡还在不在」的表达式，删除前基线和删除后判定**必须用同一个**，
// 否则前后两次量的是两把不同的尺子，before/after 对不起来。
// ⚠️ 只搜 TITLE，不要搜任何包含 TITLE 的串（BUG-V20 的误报就出在这儿）。
const stillThereExpr = `(function(){var cs=document.querySelectorAll('.task-card');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(TITLE)})>=0)return true}return false})()`
const cardCountExpr = `document.querySelectorAll('.task-card').length`

const pgTaskId = () => psql(`select id from ${SCHEMA}.tasks where title = '${TITLE}' limit 1`)
const pgSubCount = (tid) => psql(`select count(*) from ${SCHEMA}.tasks where title = '${SUB}' and parent_id = '${tid}'`)
// 评论正文在 work_item_events.payload (jsonb) 里，没有独立 comment 列
const pgEventCount = (tid) => psql(`select count(*) from ${SCHEMA}.work_item_events where task_id = '${tid}' and payload::text like '%${CMT}%'`)
const pgStatus = (tid) => psql(`select status from ${SCHEMA}.tasks where id = '${tid}'`)

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
// ⚠️ 2026-10-03 补「同视图删除前基线」。上一版只看删除后的 found=false，
//    那个判据有个 vacuity 逃逸口：**列表整个空掉 / 渲染坏了，found 同样是 false**，
//    会被记成「刷新正常」。before/after 必须成对：同一个列表视图上，
//    删前确实看得到这张卡且列表是活的，删后它不见了且列表仍然活着。
//    BUG-V20 的误报和这个逃逸口是同一句教训：先证明「它本来在」，再去说「它没了」。
await goto('#/ai')
let preHit = false
let preCards = 0
{
  const dl = Date.now() + 12000
  while (Date.now() < dl) {
    preCards = await ev(cardCountExpr)
    preHit = !!await ev(stillThereExpr)
    if (preCards > 0 && preHit) break
    await sleep(400)
  }
}
check('删除前基线：同一列表视图里这张卡可见、列表是活的', preHit === true,
  `cards=${preCards} found=${preHit}（cards=0 说明列表没渲染出来，删除判据无法自证）`)
// 点回详情再删
await ev(`(function(){var cs=document.querySelectorAll('.task-card');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(TITLE)})>=0){cs[i].click();return 1}}return 0})()`)
await sleep(3000)
const preHash = (await ev('location.hash')) || ''
if (preHash.indexOf('#/tasks/') !== 0) {
  console.error(`❌ 没能从列表回到任务详情（hash=${preHash}）—— 删除步骤的判据不可信`)
  process.exitCode = 8
}
const del = await ev(`(function(){var b=document.querySelector('.action-btn.delete');if(b){b.click();return 1}return 0})()`)
console.log('点「删除」= ', del)
await sleep(1500)
// ⚠️ 2026-10-03 改：原来是**全页面文本匹配**「删除/确认删除/确定删除」。
//    实测（diag-task-delete-network.mjs）确认弹层是 `Dialog`：
//      .dialog > .dialog-footer 里 footerButtons = ["取消","删除"]
//    确认按钮是**最后一个**，而全页面匹配会扫到别的视图/旧渲染里同文案的按钮，
//    点空了就静悄悄跳过 —— 后面的「PG 无该行」也许是上一条删除的结果。
//    改成**按选择器点弹层 footer 里的最后一个按钮**，并回报点了什么。
let confirmHit
if (SABOTAGE === 'skip-confirm') {
  confirmHit = 'sabotage:故意不点确认'
  console.log('   [sabotage] 不点确认弹层的按钮 ⇒ 删除不会发生，下面两条删除判据**应该**转红')
} else {
  confirmHit = await ev(`(function(){
  var f=document.querySelector('.dialog .dialog-footer');
  if(!f) return 'NO_FOOTER';
  var bs=f.querySelectorAll('button');
  if(!bs.length) return 'NO_BUTTONS';
  var b=bs[bs.length-1];
  var t=(b.textContent||'').replace(/\\s+/g,' ').trim();
  b.click(); return 'clicked:'+t;
})()`)
  console.log('点确认按钮 =', confirmHit)
}
if (SABOTAGE !== 'skip-confirm' && !String(confirmHit).startsWith('clicked:')) {
  console.error('❌ 确认弹层没点中 —— 本轮的删除判据全部作废（不是产品缺陷，是探针没走到那一步）')
  process.exitCode = 8
}
await sleep(3000)
const pgGone = await psql(`select count(*) from ${SCHEMA}.tasks where id = '${tid}'`)
check('删除后 PG 无该行', String(pgGone) === '0', `count=${pgGone}`)
await goto('#/ai')
// 判「列表不再回显」必须**轮询到有截止时间**，不能读某一瞬间的快照。
// 头一版 goto 之后立刻读，2.2s 不够就报「还在」——把「慢」和「不刷新」混成一个结论。
// 现在轮询到 15s，并打印实际耗时：若在窗口内消失 ⇒ 是慢；若始终不消失 ⇒ 真不刷新。
let stillThere = true
let cardsAfter = -1
const goneDl = Date.now() + 15000
const t0 = Date.now()
while (Date.now() < goneDl) {
  stillThere = !!await ev(stillThereExpr)
  cardsAfter = await ev(cardCountExpr)
  if (!stillThere) break
  await sleep(500)
}
const goneMs = Date.now() - t0
// 歧义守卫：删前列表是活的（preCards>1）而删后整页 0 张卡 —— 那更像页面坏了，
// 不是「刷新成功」。这种情形不能给 PASS，否则又是一条空过的判据。
const blanked = !stillThere && cardsAfter === 0 && preCards > 1
check('删除后列表不再回显（轮询至多 15s）', !stillThere && !blanked,
  `found=${!!stillThere}  cardsBefore=${preCards} cardsAfter=${cardsAfter}  耗时=${goneMs}ms` +
  (stillThere ? '  ⇒ 15s 内始终不消失，指向「删除后列表不刷新」' : '') +
  (blanked ? '  ⇒ 列表整页空了，判据分不清「已刷新」与「页面坏掉」，不算通过' : ''))
if (blanked) process.exitCode = 8

// 证伪自检：--sabotage=skip-confirm 破坏的是「点确认」这一步，
// 判据必须能把它抓住。抓不住 = 这套判据对删除这条路是装饰性的。
if (SABOTAGE === 'skip-confirm') {
  const expectFail = ['删除后 PG 无该行', '删除后列表不再回显']
  const red = expectFail.filter((k) => checks.some((c) => c.n.indexOf(k) >= 0 && !c.pass))
  if (red.length === expectFail.length) {
    console.log(`✓ 证伪有效：${red.length}/${expectFail.length} 条如期转红 —— 判据确实在检查删除这件事`)
  } else {
    console.error(`✗ 证伪无效：不点确认本该让 ${expectFail.length} 条全红，实际只红了 ${red.length} 条 —— 判据没有区分力`)
    process.exitCode = 8
  }
  console.log(`（证伪模式任务不会被删除，残留行 tid=${tid}，留在隔离 schema ${SCHEMA} 里可手工清）`)
}

const passed = checks.filter((c) => c.pass).length
const failed = checks.length - passed
console.log(`\n=== 汇总 ===\n${passed}/${checks.length} 通过`)
if (failed) console.error(`✗ ${failed} 条不通过 —— 如实记录，不当作通过。`)
// BUG-V19：这里原本写死 `process.exit(0)`，于是哪怕 8/9 通过，退出码也是 0，
// 调用方（run-device-against-isolated、CI）无从分辨「跑过了」与「全绿」。
// 与 BUG-V15（邮件同步探针退出码恒 0）同一类。
// ⚠️ 2026-10-03 又补一个：原来这行是 `process.exitCode = failed ? 1 : 0`，
//    **无条件覆盖**。于是上面几处硬闸（confirmHit 没点中 exitCode=8、
//    列表整页空了 exitCode=8、证伪无效 exitCode=8）全被冲回 0 —— 硬闸形同虚设，
//    「探针没走到那一步」和「全绿」在退出码上长得一模一样。
//    改成：已经有非零（8）就保留，否则按 failed 决定。
// ⚠️ ws.close() 后仍可正常设 exitCode；不要用 process.exit()，那会跳过 close。
ws.close()
if (failed) process.exitCode = 1
else if (!process.exitCode) process.exitCode = 0
