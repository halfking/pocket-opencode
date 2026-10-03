// BUG-AQ 真机验证：Android WebView 里原生 confirm/alert 会同步阻塞渲染进程。
//
// 本脚本刻意做成「探针自证」结构，分三段：
//   A. 主动制造冻结：在页面里调用 window.confirm()。原生对话框会停摆 JS 线程，
//      此时任何 Runtime.evaluate 都必须超时。探针抓不到 = 探针坏了，不等于没冻结。
//   B. 恢复：用 Page.handleJavaScriptDialog 关掉原生对话框，验证渲染进程能回来。
//   C. 验修复：点真实的「删除」按钮，断言出现的是 Vue ConfirmDialog（不是原生对话框），
//      且从点击到确认，全程渲染进程保持可响应。
//
// 判据要点：绝不用 toast/状态条这类反馈类信号单独成立，一律以「CDP 是否能及时返回」为准。
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9397'
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
let nativeDialogSeen = null
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  // 原生对话框开启事件——这是「真·阻塞式弹窗」的铁证
  if (m.method === 'Page.javascriptDialogOpening') nativeDialogSeen = m.params
  if (m.method === 'Inspector.targetCrashed') console.log('!!! TARGET CRASHED')
})
await new Promise((r) => ws.addEventListener('open', r))

const send = (method, params = {}, timeoutMs = 8000) =>
  new Promise((r) => {
    const i = ++id
    const timer = setTimeout(() => { pending.delete(i); r({ __timeout: method }) }, timeoutMs)
    pending.set(i, (v) => { clearTimeout(timer); r(v) })
    ws.send(JSON.stringify({ id: i, method, params }))
  })

// 探针活性自检：能返回说明渲染进程是活的
const ev = async (x, timeoutMs = 8000) => {
  const r = await send('Runtime.evaluate', { expression: x, returnByValue: true, awaitPromise: true }, timeoutMs)
  if (r?.__timeout) return { __frozen: true }
  return r?.result?.value
}
await send('Page.enable')
await send('Runtime.enable')

const checks = []
const check = (n, pass, d) => { checks.push({ n, pass }); console.log(`${pass ? 'PASS' : 'FAIL'}  ${n}${d ? '  — ' + d : ''}`) }
const step = (s) => console.log(`\n===== ${s} =====`)

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

// ---------------------------------------------------------------- 0. 基线
step('0. 基线：渲染进程存活')
{
  const v = await ev('1+1')
  check('基线 evaluate 可返回（渲染进程活着）', v === 2, `1+1 => ${JSON.stringify(v)}`)
}

// ------------------------------------------- A. 探针自证：主动制造原生冻结
step('A. 探针自证：用 window.confirm 主动制造冻结，探针必须抓得到')
{
  nativeDialogSeen = null
  // 故意不 await：confirm 会停摆线程，evaluate 永挂，只能靠超时判定
  ev('window.confirm("SELFTEST_PROBE"); 1', 4000).catch(() => {})
  await sleep(1500)
  check('A1 收到 Page.javascriptDialogOpening（原生对话框确实打开了）', !!nativeDialogSeen,
    nativeDialogSeen ? `type=${nativeDialogSeen.type} message=${JSON.stringify(nativeDialogSeen.message)}` : '未收到事件')
  const during = await ev('1+1', 4000)
  check('A2 对话框打开期间 evaluate 超时（探针能识别冻结态）', !!during?.__frozen,
    during?.__frozen ? '返回 __frozen：符合预期，JS 线程被停摆' : `意外返回 ${JSON.stringify(during)}`)
}

// ------------------------------------------------------------- B. 恢复
step('B. 关闭原生对话框，验证渲染进程能恢复')
{
  await send('Page.handleJavaScriptDialog', { accept: false })
  await sleep(1200)
  const v = await ev('1+1')
  check('B1 关掉原生对话框后 evaluate 恢复', v === 2, `1+1 => ${JSON.stringify(v)}`)
}

// ------------------------------------------------- C. 验修复：真实删除按钮
step('C. 验修复：点真实「删除」按钮，Vue ConfirmDialog 应出现且渲染进程不冻结')
{
  await goto('/settings/scheduled-tasks')
  // 应用用 hash 路由（#/settings/scheduled-tasks），断言必须归一化，
  // 否则 C0 会在页面明明正确时误报——这正是「测试自身的 bug」冒充「产品缺陷」的典型。
  const hash = String(await ev('location.hash'))
  check('C0 已进入定时任务页', hash.replace(/^#/, '') === '/settings/scheduled-tasks', `hash=${hash}`)

  nativeDialogSeen = null
  // 找到第一条任务行的「删除」按钮
  const clicked = await ev(`(function(){
    var btns=Array.prototype.slice.call(document.querySelectorAll('button'));
    var b=btns.find(function(x){return (x.textContent||'').trim()==='删除'});
    if(!b) return 'NO_DELETE_BTN';
    b.click(); return 'CLICKED';
  })()`)
  check('C1 触发删除动作', clicked === 'CLICKED', `result=${clicked}`)
  await sleep(1200)

  check('C2 未打开原生对话框（本该已被 useConfirm 取代）', nativeDialogSeen === null,
    nativeDialogSeen ? `仍然弹了原生 ${nativeDialogSeen.type}` : '无 javascriptDialogOpening 事件')

  // 关键判据：点了删除之后渲染进程必须还能立刻响应
  const alive = await ev('1+1')
  check('C3 点击删除后渲染进程未冻结', alive === 2, `1+1 => ${JSON.stringify(alive)}`)

  // Vue ConfirmDialog 应已渲染。用 .confirm-message 精确定位，
  // 不用「页面上有没有删除按钮」——列表里本来就有一堆删除按钮，会假通过。
  const dlg = await ev(`(function(){
    var d=Array.prototype.slice.call(document.querySelectorAll('.dialog'))
      .find(function(x){return x.querySelector('.confirm-message')});
    if(!d) return JSON.stringify({found:false, confirmMsgs: document.querySelectorAll('.confirm-message').length});
    var btns=Array.prototype.slice.call(d.querySelectorAll('button')).map(function(x){return (x.textContent||'').trim();});
    return JSON.stringify({found:true, msg:d.querySelector('.confirm-message').textContent.trim(), buttons:btns});
  })()`)
  console.log('    对话框 DOM: ' + dlg)
  const parsed = (() => { try { return JSON.parse(String(dlg)) } catch { return {} } })()
  const btns = parsed.buttons || []
  check('C4 Vue ConfirmDialog 已渲染（.confirm-message 可见 + 取消/删除按钮齐全）',
    parsed.found === true && btns.includes('取消') && btns.includes('删除'),
    `msg=${JSON.stringify(parsed.msg)} buttons=${JSON.stringify(btns)}`)

  // 确认删除：必须点在对话框自己的 footer 上，不能用页面上第一个「删除」
  const confirmed = await ev(`(function(){
    var d=Array.prototype.slice.call(document.querySelectorAll('.dialog'))
      .find(function(x){return x.querySelector('.confirm-message')});
    if(!d) return 'NO_DIALOG';
    var b=Array.prototype.slice.call(d.querySelectorAll('button'))
      .find(function(x){return (x.textContent||'').trim()==='删除'});
    if(!b) return 'NO_CONFIRM_BTN';
    b.click(); return 'CONFIRMED';
  })()`)
  check('C5 点击对话框内的确认删除', confirmed === 'CONFIRMED', `result=${confirmed}`)
  await sleep(2500)
  const alive2 = await ev('1+1')
  check('C6 删除后渲染进程仍存活', alive2 === 2, `1+1 => ${JSON.stringify(alive2)}`)
  const stillOpen = await ev(`document.querySelectorAll('.confirm-message').length`)
  check('C7 对话框已关闭', stillOpen === 0, `.confirm-message 剩余 ${stillOpen} 个`)
}

step('汇总')
const pass = checks.filter((c) => c.pass).length
console.log(`${pass}/${checks.length} 通过`)
for (const c of checks) if (!c.pass) console.log(`  FAILED: ${c.n}`)
adb(['-s', SERIAL, 'forward', '--remove', `tcp:${PORT}`])
process.exit(pass === checks.length ? 0 : 1)
