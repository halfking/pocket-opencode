// 定时任务（自动化）写路径真机验证 —— 该模块此前**零**写路径验证。
// 覆盖：创建(POST) -> 列表回显 -> 启用/停用(PATCH) -> 编辑(保存修改) -> 删除(DELETE)
// 判据：每步都用 PG 直查 opencode_pocket.scheduled_tasks 兜底，不以 toast 作为唯一依据。
// 用本次唯一 NAME 做锚点，避免与既有数据混淆。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9395'
const MASTER = process.env.POCKET_MASTER || 'PocketTest2026'
const PSQL = process.env.POCKET_PSQL || 'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe'
// PG schema：跟随后端配置（backend/internal/config/config.go 的 POCKET_PG_SCHEMA，默认值相同）。
// 写死 opencode_pocket 会让本脚本只能对着共享库跑 —— 失败时 SEED 就留在别人的库里。
const SCHEMA = process.env.POCKET_PG_SCHEMA || 'opencode_pocket';
if (SCHEMA !== 'opencode_pocket') console.log(`PG schema = ${SCHEMA}（非共享库）`);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
// psql 串必须纯 ASCII——中文经 ANSI 码页会报 invalid byte sequence
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
  new Promise((r) => {
    const i = ++id
    // 每个 CDP 命令都要有超时。渲染进程一旦卡死（JS 线程停摆），
    // 任何 evaluate 都永远不会返回——没有超时的话脚本会静默挂到天荒地老，
    // 而且因为 stdout 走管道全缓冲，连卡在哪一步都看不到。
    // 实测踩过：编辑→删除那段会把渲染进程卡住，所有 CDP 命令超时。
    const timer = setTimeout(() => {
      pending.delete(i)
      r({ __timeout: method })
    }, 15000)
    pending.set(i, (v) => { clearTimeout(timer); r(v) })
    ws.send(JSON.stringify({ id: i, method, params }))
  })
const ev = async (x) => {
  const r = await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true })
  if (r?.__timeout) throw new Error(`CDP 超时(${r.__timeout})：渲染进程可能已卡死`)
  return r?.result?.value
}
await send('Runtime.enable')

const checks = []
const check = (n, pass, d) => { checks.push({ n, pass }); console.log(`${pass ? 'PASS' : 'FAIL'}  ${n}${d ? '  — ' + d : ''}`) }

async function ensureUnlocked() {
  if (!(await ev('!!document.querySelector(\'input[placeholder*="主密码"]\')'))) return
  await ev(`(function(){var e=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(e),'value').set;s.call(e,${JSON.stringify(MASTER)});e.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`)
  await sleep(700)
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='解锁'});if(b)b.click();return 1})()`)
  await sleep(5000)
}
async function goto(hash, ms = 15000) {
  await ev(`location.hash=${JSON.stringify(hash)}`)
  const d = Date.now() + ms
  while (Date.now() < d && (await ev('location.hash')) !== hash) await sleep(300)
  await ensureUnlocked()
  await sleep(1800)
}
async function typeInto(sel, text) {
  const box = await ev(`(function(){var e=document.querySelector(${JSON.stringify(sel)});if(!e)return null;var r=e.getBoundingClientRect();return JSON.stringify({x:r.left+r.width/2,y:r.top+r.height/2})})()`)
  if (!box) return 'NOT_FOUND'
  const { x, y } = JSON.parse(box)
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', clickCount: 1 })
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', clickCount: 1 })
  await sleep(250)
  await send('Input.insertText', { text })
  await sleep(500)
  return 'typed'
}
const visibleClickText = (re) =>
  ev(`(function(){var bs=Array.prototype.slice.call(document.querySelectorAll('button')).filter(function(b){return !!b.offsetParent && new RegExp(${JSON.stringify(re.source)}).test(b.textContent||'')});if(!bs.length)return 0;bs[0].click();return 1})()`)

const NAME = 'ST-' + String(Date.now()).slice(-7)
const NAME2 = 'ST2-' + String(Date.now()).slice(-7)
console.log('task name =', NAME)

const pgRow = () => psql(`select id || '|' || enabled::int from ${SCHEMA}.scheduled_tasks where name = '${NAME}' limit 1`)
const pgCount = () => psql(`select count(*) from ${SCHEMA}.scheduled_tasks where name = '${NAME}'`)

await ensureUnlocked()
await goto('#/settings/scheduled-tasks')

// ---------- 1. 创建 ----------
const opened = await ev(`(function(){var b=document.querySelector('button[aria-label="创建自动化"]');if(b){b.click();return 'HEADER_BTN'}var p=Array.prototype.slice.call(document.querySelectorAll('button')).filter(function(x){return !!x.offsetParent && (x.textContent||'').trim()==='创建自动化'});if(p.length){p[0].click();return 'EMPTY_BTN'}return 'NONE'})()`)
console.log('打开创建入口 =', opened)
await sleep(2500)
await goto('#/settings/scheduled-tasks/new')

await typeInto('input[placeholder*="工作日晨报"]', NAME)
// 必须填「任务内容」提示词，不是 payloadText。
// 默认 kind=redclaw_chat 时 showPrompt 为真，save() 会在 prompt 为空时**早退**
// 并显示「请填写任务提示词」，**根本不发请求**——所以只填 payloadText 会得到
// 「没落库」的假象。（上一版就是这样踩的，看起来像产品 bug。）
await typeInto('textarea[placeholder*="到点时"]', 'noop-' + NAME)
await sleep(400)
const nameVal = await ev(`(function(){var i=document.querySelector('input[placeholder*="工作日晨报"]');return i?i.value:'NO_INPUT'})()`)
const promptVal = await ev(`(function(){var t=document.querySelector('textarea[placeholder*="到点时"]');return t?t.value:'NO_PROMPT'})()`)
check('名称输入成功', nameVal === NAME, `value=${nameVal}`)
check('提示词输入成功', promptVal === `noop-${NAME}`, `value=${promptVal}`)

const created = await visibleClickText(/^创建任务$/)
console.log('点「创建任务」=', created)
await sleep(3500)
// 保存失败时页面会给出明确 error 文案，必须读出来而不是只看 PG
const saveErr = await ev(`(function(){var es=Array.prototype.slice.call(document.querySelectorAll('.error,[role="alert"]'));return es.map(function(e){return (e.textContent||'').trim().slice(0,100)}).join(' ; ')})()`)
if (saveErr) console.log('页面错误文案 =', saveErr)
await goto('#/settings/scheduled-tasks')

let row = pgRow()
check('创建：PG 落库', !!row, `scheduled_tasks -> ${row || 'NONE'}`)
const inList = await ev(`(function(){var cs=document.querySelectorAll('article.card, .card');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(NAME)})>=0)return true}return false})()`)
check('创建：列表回显', !!inList, `found=${!!inList}`)

if (!row) {
  const passed = checks.filter((c) => c.pass).length
  console.log(`\n=== 中止（未落库）===\n${passed}/${checks.length}`)
  ws.close(); process.exit(1)
}
const tid = row.split('|')[0]

// ---------- 2. 启用/停用 (PATCH) ----------
const beforeEnabled = row.split('|')[1]
const toggled = await ev(`(function(){var cs=document.querySelectorAll('article.card, .card');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(NAME)})>=0){var bs=cs[i].querySelectorAll('button');for(var j=0;j<bs.length;j++){var t=(bs[j].textContent||'').trim();if(t==='停用'||t==='启用'){bs[j].click();return 1}}return 0}}return 0})()`)
await sleep(3000)
row = pgRow()
const afterEnabled = row.split('|')[1]
check('启用/停用 落库', afterEnabled !== beforeEnabled, `enabled ${beforeEnabled} -> ${afterEnabled}`)

// ---------- 3. 编辑 ----------
await ev(`location.hash='#/settings/scheduled-tasks/${tid}/edit'`)
await sleep(3000)
await ensureUnlocked()
const editName = await ev(`(function(){var i=document.querySelector('input[placeholder*="工作日晨报"]');return i?i.value:''})()`)
console.log('编辑页回填名称 =', editName)
check('编辑页回填原值', editName === NAME, `value=${editName}`)
await ev(`(function(){var i=document.querySelector('input[placeholder*="工作日晨报"]');if(!i)return 0;i.focus();i.setSelectionRange(0,i.value.length);return 1})()`)
await sleep(300)
await send('Input.insertText', { text: '' })
await typeInto('input[placeholder*="工作日晨报"]', NAME2)
await sleep(500)
const newVal = await ev(`(function(){var i=document.querySelector('input[placeholder*="工作日晨报"]');return i?i.value:''})()`)
console.log('改名后输入框 =', newVal)
const saved = await visibleClickText(/^保存修改$/)
console.log('点「保存修改」=', saved)
await sleep(3000)
const renamed = psql(`select count(*) from ${SCHEMA}.scheduled_tasks where id = '${tid}' and name = '${NAME2}'`)
check('编辑改名落库', String(renamed) === '1', `name=${NAME2} count=${renamed}`)

// ---------- 4. 删除 ----------
await goto('#/settings/scheduled-tasks')
const del = await ev(`(function(){var cs=document.querySelectorAll('article.card, .card');for(var i=0;i<cs.length;i++){if((cs[i].textContent||'').indexOf(${JSON.stringify(NAME2)})>=0){var bs=cs[i].querySelectorAll('button');for(var j=0;j<bs.length;j++){if((bs[j].textContent||'').trim()==='删除'){bs[j].click();return 1}}}}return 0})()`)
console.log('点「删除」=', del)
await sleep(1500)
await ev(`(function(){var bs=Array.prototype.slice.call(document.querySelectorAll('button')).filter(function(b){return !!b.offsetParent && /^(删除|确认|确定)$/.test((b.textContent||'').trim())});if(!bs.length)return 0;bs[bs.length-1].click();return 1})()`)
await sleep(3000)
const gone = psql(`select count(*) from ${SCHEMA}.scheduled_tasks where id = '${tid}'`)
check('删除后 PG 无该行', String(gone) === '0', `count=${gone}`)

const passed = checks.filter((c) => c.pass).length
console.log(`\n=== 汇总 ===\n${passed}/${checks.length} 通过`)
ws.close()
process.exit(0)
