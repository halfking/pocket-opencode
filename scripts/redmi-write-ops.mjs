#!/usr/bin/env node
// 真机功能性测试：驱动真实 UI 完成写操作并回读校验
//
// 与 verify-modules.mjs 的区别：那个只断言「可达 + 渲染」（reachability），
// 这个真的点按钮、填表单、落库、再回读比对（behavioral）。
//
// 用法：
//   $env:POCKET_SERIAL='192.168.31.19:5555'
//   node scripts/redmi-write-ops.mjs
//
// 前置：已登录 + 本地库已解锁 + adb reverse tcp:8088 tcp:8088
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9232'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const stamp = Date.now().toString().slice(-8)
const TITLE = `E2E笔记${stamp}`

const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }

let ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) } })
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
const ev = async (x) => {
  const r = await send('Runtime.evaluate', { expression: x, returnByValue: true })
  return r?.exceptionDetails ? { __err: r.exceptionDetails.text } : r?.result?.value
}
const bodyText = () => ev(`document.body.innerText.replace(/\\s+/g,' ')`)
const results = []
const record = (name, pass, detail) => {
  results.push({ name, pass, detail })
  console.log(`${pass ? 'PASS' : 'FAIL'}  ${name}${detail ? '  — ' + detail : ''}`)
}
const closeDialogs = () => ev(`(function(){
  var n = 0;
  Array.from(document.querySelectorAll('.dialog-close, [class*="close"]')).forEach(b => { try { b.click(); n++ } catch(e){} });
  return n;
})()`)

const MASTER = process.env.POCKET_MASTER || ''
/**
 * 进程重启后 App 会回到「已登出 + 本地库锁定」态（SQLCipher 库必须主密码才能打开），
 * 此时任何 #/xxx 都会被路由守卫弹到 #/login?returnTo=…&unlock=1，列表本来就是空的。
 * 不先恢复就会把「被 gate 挡住」误判成「数据丢了 / 删干净了」——本轮就踩了。
 */
async function ensureSession(label) {
  await ev(`location.hash = '#/login'`)
  await sleep(2500)
  const state = await ev(`JSON.stringify({
    hash: location.hash,
    hasToken: !!localStorage.getItem('pocket_token'),
    hasUnlockInput: !!document.querySelector('input[placeholder*="主密码"]'),
    hasUserInput: !!document.querySelector('input[placeholder*="用户名"]')
  })`)
  console.log(`  [${label}] state =`, state)

  // 本地库解锁
  if (MASTER) {
    const filled = await ev(`(function(){
      var el = document.querySelector('input[placeholder*="主密码"]');
      if (!el) return 'NOT_GATED';
      Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set.call(el, ${JSON.stringify(MASTER)});
      el.dispatchEvent(new Event('input', { bubbles: true }));
      return 'filled';
    })()`)
    if (filled === 'filled') {
      // 必须等 Vue 重渲染：解锁按钮的 disabled 状态是计算属性，填完立刻点会点到 disabled 按钮
      await sleep(1500)
      console.log(`  [${label}] unlock btn =`, await ev(`(function(){
        var b = Array.from(document.querySelectorAll('button')).find(x => (x.textContent||'').trim() === '解锁');
        if (!b) return 'NO_BTN';
        if (b.disabled) return 'STILL_DISABLED';
        b.click(); return 'clicked';
      })()`))
      await sleep(4000)
    }
  }

  // 账号登录
  const hasUser = await ev(`!!document.querySelector('input[placeholder*="用户名"]')`)
  if (hasUser) {
    const src = (await import('node:fs')).readFileSync('backend/internal/server/server_assistant.go', 'utf8')
    const pass = (src.match(/devPass\s*=\s*"([^"]+)"/) || [])[1] || ''
    await ev(fill('input[placeholder*="用户名"]', 'admin'))
    await ev(fill('input[type="password"]', pass))
    await sleep(800)
    console.log(`  [${label}] login ->`, await ev(clickByText('登录', true)))
    await sleep(6000)
  }
  console.log(`  [${label}] after =`, await ev(`location.hash + ' token=' + !!localStorage.getItem('pocket_token')`))
}

// Vue v-model 必须走原生 setter + input 事件
const fill = (sel, val) => `(function(){
  var el = document.querySelector(${JSON.stringify(sel)});
  if (!el) return 'NOT_FOUND';
  var proto = Object.getPrototypeOf(el);
  var setter = Object.getOwnPropertyDescriptor(proto, 'value').set;
  setter.call(el, ${JSON.stringify(val)});
  el.dispatchEvent(new Event('input', { bubbles: true }));
  el.dispatchEvent(new Event('change', { bubbles: true }));
  return 'ok';
})()`
const clickByText = (text, exact) => `(function(){
  var bs = Array.from(document.querySelectorAll('button'));
  var b = bs.find(x => ${exact ? `(x.textContent||'').trim() === ${JSON.stringify(text)}` : `(x.textContent||'').indexOf(${JSON.stringify(text)}) >= 0`});
  if (!b) return 'NO_BUTTON';
  if (b.disabled) return 'DISABLED';
  b.click(); return 'clicked';
})()`
const btnStates = () => ev(`JSON.stringify(Array.from(document.querySelectorAll('button')).map(b => (b.textContent||'').trim().slice(0,20) + (b.disabled?'[off]':'[on]')).filter(s => s))`)

console.log(`=== 真机功能测试 ${SERIAL}  (note title = ${TITLE}) ===\n`)

/** 轮询等待表达式返回 truthy，避免固定 sleep 踩时序坑 */
async function waitFor(expr, label, timeoutMs = 15000) {
  const t0 = Date.now()
  while (Date.now() - t0 < timeoutMs) {
    const v = await ev(expr)
    if (v) return v
    await sleep(600)
  }
  console.log(`  !! TIMEOUT waiting for: ${label}`)
  return null
}
const abort = (why) => {
  console.log(`  !! ABORT: ${why}`)
  console.log('\n=== SUMMARY ABORTED (downstream assertions would be invalid) ===')
  try { ws.close() } catch {}
  process.exit(5)
}
/** 导航到路由并确认关键元素出现；失败直接退出，避免级联失败产生假结论 */
async function goto(route, mustExist, label) {
  await ev(`location.hash = ${JSON.stringify(route)}`)
  const ok = await waitFor(mustExist, `${route} -> ${label}`)
  if (!ok) abort(`${route} 未渲染出「${label}」；当前 hash = ${await ev('location.hash')}`)
  return ok
}

// ---------- 1. CREATE note ----------
// 会话恢复由 ensureSession() 负责：脚本开头显式调一次，force-stop 重启后
// 因为要重挂 CDP 也会再调一次（见第 2 步）。**不要在这里另写一份恢复逻辑**——
// 本轮误加过一份重复实现，结果是同一个动作有两套代码在维护。
await ensureSession('start')
console.log('--- 1. CREATE note ---')
await goto('#/notes/new', `!!document.querySelector('textarea[placeholder*="一句话概括"]')`, '新建笔记表单')
console.log('  title field  =', await ev(fill('textarea[placeholder*="一句话概括"]', TITLE)))
console.log('  content field=', await ev(fill('textarea[placeholder*="全屏编辑"]', `正文-${stamp}`)))
const createEnabled = await waitFor(
  `(function(){ var b = Array.from(document.querySelectorAll('button')).find(x => (x.textContent||'').indexOf('创建') >= 0); return (b && !b.disabled) ? 'enabled' : null })()`,
  '创建按钮 enabled', 8000)
if (!createEnabled) {
  console.log('  buttons =', await btnStates())
  abort('创建按钮始终 disabled（表单校验未通过）')
}
console.log('  create btn ->', await ev(clickByText('创建')))
await waitFor(`location.hash.indexOf('/new') < 0 ? 'left-form' : null`, '跳回列表', 15000)
const afterCreateHash = await ev(`location.hash`)
const afterCreateText = (await bodyText()) || ''
record('新建笔记：跳回列表', !afterCreateHash.includes('/new'), `hash=${afterCreateHash}`)
record('新建笔记：列表回显标题', afterCreateText.includes(TITLE), afterCreateText.includes(TITLE) ? 'found' : 'not found in list')
const noteIdMatch = (afterCreateText.match(/note-\d+-\w+/) || [])[0] || ''

// ---------- 2. 落库校验：force-stop 后重启，笔记必须还在 ----------
// 笔记存在 Capacitor SQLite 原生插件（表 local_notes），不在 WebView 的
// localStorage / IndexedDB 里。因此唯一可信的持久化判据是「进程重启后仍在」。
console.log('\n--- 2. PERSISTENCE (force-stop + relaunch) ---')
console.log('  force-stop ->', adb(['-s', SERIAL, 'shell', `am force-stop ${PKG}`]).trim() || 'ok')
await sleep(2500)
adb(['-s', SERIAL, 'shell', `monkey -p ${PKG} -c android.intent.category.LAUNCHER 1`])
await sleep(9000)
const pid2 = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
console.log('  relaunched pid =', pid2)
// 重新挂 CDP（新进程 => 新 socket）
adb(['-s', SERIAL, 'forward', '--remove', `tcp:${PORT}`])
const socks2 = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks2.find((s) => s.endsWith(`_${pid2}`)) || socks2[socks2.length - 1]}`])
const page2 = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
ws.close()
ws = new WebSocket(page2.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
id = 0
pending.clear()
ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) } })
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
await ensureSession('after-restart')
await ev(`location.hash = '#/notes'`)
await sleep(4000)
const gated = (await ev(`location.hash`)) || ''
const afterRestart = (await bodyText()) || ''
const gatedOut = gated.includes('/login')
record('落库：force-stop 重启后笔记仍在', !gatedOut && afterRestart.includes(TITLE),
  gatedOut ? `BLOCKED by route guard (${gated}) — 断言无效，需先恢复会话` :
            (afterRestart.includes(TITLE) ? 'persisted across process restart' : 'LOST after restart'))
console.log(`  (note id = ${noteIdMatch || 'n/a'})`)

// ---------- 3. 编辑笔记（详情页默认是只读，要先点「✎ 编辑」）----------
console.log('\n--- 3. UPDATE note ---')
const NEW_BODY = `已编辑-${stamp}`
await closeDialogs()
await goto('#/notes', `!!document.querySelector('.note-card')`, '笔记列表(编辑前)')
const opened = await ev(`(function(){
  var cards = Array.from(document.querySelectorAll('.note-card'));
  var card = cards.find(c => (c.textContent||'').indexOf(${JSON.stringify(TITLE)}) >= 0);
  if (!card) return 'CARD_NOT_FOUND';
  card.click(); return 'clicked';
})()`)
if (opened !== 'clicked') abort(`打开笔记失败: ${opened}`)
console.log('  open note ->', opened)
await waitFor(`location.hash.indexOf('/notes/') >= 0 ? location.hash : null`, '进入详情页', 12000)
const NOTE_ROUTE = await ev(`location.hash`)
console.log('  note route =', NOTE_ROUTE)
// 只等 hash 变化**不够**：路由已经切了但详情页的工具栏还没渲染出来，
// 这时 querySelectorAll('button') 里还没有「✎ 编辑」，clickByText 返回
// NO_BUTTON —— 看起来像"编辑入口不存在"，实际是抢跑了。
// 判据改成等「编辑」按钮真的出现。（本轮踩过，ABORT 了一次。）
const editBtnReady = await waitFor(
  `Array.from(document.querySelectorAll('button')).some(b => (b.textContent||'').indexOf('编辑') >= 0) ? 'ready' : null`,
  '详情页「编辑」按钮出现', 12000)
if (!editBtnReady) {
  console.log('  buttons =', await btnStates())
  abort('进入详情页后始终没有「编辑」按钮')
}
console.log('  click 编辑 ->', await ev(clickByText('编辑')))
const inEdit = await waitFor(`!!document.querySelector('textarea') ? 'edit-mode' : null`, '进入编辑态', 12000)
if (!inEdit) abort('点「编辑」后没有出现 textarea')
console.log('  textareas after 编辑 =', await ev(`JSON.stringify(Array.from(document.querySelectorAll('textarea')).map(t => (t.placeholder||'').slice(0,24)+'|len='+t.value.length))`))
// 注意：详情页编辑态有两个 textarea（标题 + 正文）。必须按 placeholder 精确选正文，
// 否则 fill('textarea') 会命中第一个（标题），把标题也改掉，后续按标题定位卡片就找不到。
console.log('  edit content ->', await ev(fill('textarea[placeholder*="全屏编辑"]', NEW_BODY)))
await waitFor(`(function(){ var b = Array.from(document.querySelectorAll('button')).find(x => (x.textContent||'').indexOf('保存') >= 0); return (b && !b.disabled) ? 'enabled' : null })()`, '保存按钮 enabled', 8000)
const saveState = await ev(clickByText('保存'))
console.log('  click 保存 ->', saveState)
await sleep(3500)
await goto('#/notes', `!!document.querySelector('.note-card')`, '笔记列表(编辑后)')
const listText = (await bodyText()) || ''
// 强断言：列表 snippet 必须显示新内容，而不是只验证标题还在
record('编辑笔记：列表摘要显示新正文', listText.includes(NEW_BODY),
  listText.includes(NEW_BODY) ? `snippet="${NEW_BODY}"` : 'snippet not updated')

// ---------- 4. 删除笔记 ----------
// 详情页是只读的，删除入口是「🗑 删除」；点开后弹确认框，确认按钮文本就是「删除」
// （同页另有工具栏的删除，两个同名，必须取弹窗里 button--danger 那个）。
console.log('\n--- 4. DELETE note ---')
await closeDialogs()
// 用 noteId 直接进详情页，不靠标题找卡片——编辑只改正文，标题不变，
// 但列表里可能有多条历史测试笔记，按标题定位不够稳。
await ev(`location.hash = ${JSON.stringify(NOTE_ROUTE)}`)
await waitFor(`(function(){ var b = Array.from(document.querySelectorAll('button')).find(x => (x.textContent||'').indexOf('删除') >= 0); return b ? 'detail' : null })()`, '详情页删除入口', 12000)
console.log('  navigate by id ->', NOTE_ROUTE)
console.log('  click 删除入口 ->', await ev(clickByText('删除')))
await sleep(2000)
console.log('  dialog buttons =', await ev(`JSON.stringify(Array.from(document.querySelectorAll('button')).filter(b => /删除|取消|✕/.test(b.textContent||'')).map(b => (b.textContent||'').trim()+'|'+(b.className||'')))`))
const confirmDelete = await ev(`(function(){
  var b = Array.from(document.querySelectorAll('button')).find(x => (x.textContent||'').trim() === '删除' && /danger/.test(x.className||''))
         || Array.from(document.querySelectorAll('button')).filter(x => (x.textContent||'').trim() === '删除').pop();
  if (!b) return 'NO_CONFIRM_BTN';
  if (b.disabled) return 'CONFIRM_DISABLED';
  b.click(); return 'confirmed';
})()`)
console.log('  confirm ->', confirmDelete)
if (confirmDelete !== 'confirmed') abort(`删除确认失败: ${confirmDelete}`)
await sleep(3500)
await goto('#/notes', `(document.body.innerText||'').indexOf('笔记') >= 0`, '笔记列表(删除后)')
const afterDelete = (await bodyText()) || ''
const deleteHash = (await ev(`location.hash`)) || ''
const gatedAfterDelete = deleteHash.includes('/login')
const stillThere = afterDelete.includes(TITLE) || afterDelete.includes(NEW_BODY)
record('删除笔记：列表不再回显', !gatedAfterDelete && !stillThere,
  gatedAfterDelete ? `BLOCKED by route guard (${deleteHash}) — 断言无效` :
  (stillThere ? 'still present' : `gone (cards=${await ev(`document.querySelectorAll('.note-card').length`)})`))

// ---------- 5. 删除后重启，验证不是只从内存移除 ----------
console.log('\n--- 5. delete persistence ---')
adb(['-s', SERIAL, 'shell', `am force-stop ${PKG}`])
await sleep(2500)
adb(['-s', SERIAL, 'shell', `monkey -p ${PKG} -c android.intent.category.LAUNCHER 1`])
await sleep(9000)
const pid3 = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
adb(['-s', SERIAL, 'forward', '--remove', `tcp:${PORT}`])
const socks3 = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks3.find((s) => s.endsWith(`_${pid3}`)) || socks3[socks3.length - 1]}`])
const page3 = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
ws.close()
ws = new WebSocket(page3.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
id = 0
pending.clear()
ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) } })
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
await ensureSession('after-delete-restart')
await ev(`location.hash = '#/notes'`)
await sleep(4000)
const gated2 = (await ev(`location.hash`)) || ''
const afterDeleteRestart = (await bodyText()) || ''
const gatedOut2 = gated2.includes('/login')
const cardCount = await ev(`document.querySelectorAll('.note-card').length`)
record('删除落库：重启后笔记仍不存在', !gatedOut2 && !afterDeleteRestart.includes(TITLE),
  gatedOut2 ? `BLOCKED by route guard (${gated2}) — 断言无效` :
             (afterDeleteRestart.includes(TITLE) ? 'REAPPEARED after restart (soft delete only)' : `gone after restart (cards=${cardCount})`))

// ---------- 汇总 ----------
const pass = results.filter((r) => r.pass).length
console.log(`\n=== SUMMARY ${pass}/${results.length} ===`)
for (const r of results) console.log(`  ${r.pass ? 'PASS' : 'FAIL'}  ${r.name}  ${r.detail || ''}`)
ws.close()
process.exit(pass === results.length ? 0 : 4)
