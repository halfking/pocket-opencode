#!/usr/bin/env node
// 真机写操作 · 多模块：闪卡（卡组 + 卡片）与任务
//
// 背景：接上 PostgreSQL 之前，这两个模块在真机上是「保存恒 disabled」/
// 「POST 503」的。现在 store 已接上，需要用**真实 UI** 证明它们真的可写。
//
// 用法：
//   $env:POCKET_SERIAL='192.168.31.19:5555'
//   $env:POCKET_MASTER='<本地库主密码>'
//   node scripts/redmi-write-ops-modules.mjs
import { execFileSync } from 'node:child_process'
import { requireDevPass } from './lib/dev-pass.mjs'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9236'
const MASTER = process.env.POCKET_MASTER || ''
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const stamp = Date.now().toString().slice(-6)
const DECK = `Maestro卡组${stamp}`
const CARD_FRONT = `正面-${stamp}`
const CARD_BACK = `背面-${stamp}`
const TASK_TITLE = `Maestro任务${stamp}`
/** BUG-O 断言用：建完卡组后记下 deckId，稍后进卡组详情页验证卡片真的出现。 */
let selectedDeckId = ''

const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const sk = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${sk.find((s) => s.endsWith(`_${pid}`)) || sk[sk.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }

let ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) } })
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
await send('Network.enable')
const apiCalls = []
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) return
  const p = m.params || {}
  if (m.method === 'Network.responseReceived' && p.response?.url?.includes('/api/')) {
    apiCalls.push({ url: p.response.url.replace(/https?:\/\/[^/]+/, ''), status: p.response.status })
  }
})
const ev = async (x) => {
  const r = await send('Runtime.evaluate', { expression: x, returnByValue: true })
  return r?.exceptionDetails ? { __err: r.exceptionDetails.text } : r?.result?.value
}
const bodyText = () => ev(`document.body.innerText.replace(/\\s+/g,' ')`)
const results = []
const record = (n, pass, d) => { results.push({ n, pass, d }); console.log(`${pass ? 'PASS' : 'FAIL'}  ${n}${d ? '  — ' + d : ''}`) }
const abort = (why) => { console.log(`\n!! ABORT: ${why}`); console.log('=== SUMMARY ABORTED ==='); try { ws.close() } catch {}; process.exit(5) }

async function waitFor(expr, label, timeoutMs = 15000) {
  const t0 = Date.now()
  while (Date.now() - t0 < timeoutMs) { const v = await ev(expr); if (v) return v; await sleep(600) }
  console.log(`  !! TIMEOUT: ${label}`)
  return null
}
/** 关掉所有可能残留的弹窗/抽屉——上一段没关干净会污染下一段的 selector */
const closeOverlays = () => ev(`(function(){
  var closed = 0;
  Array.from(document.querySelectorAll('.bottom-sheet-overlay, [class*="overlay"], .dialog-backdrop')).forEach(function(o){
    try { o.click(); closed++; } catch(e) {}
  });
  Array.from(document.querySelectorAll('button.close, button[class*="close"], .sheet-close')).forEach(function(b){
    try { b.click(); closed++; } catch(e) {}
  });
  return closed;
})()`)

const goto = async (route, mustExist, label) => {
  await ev(closeOverlays())
  await ev(`location.hash = ${JSON.stringify(route)}`)
  const ok = await waitFor(mustExist, `${route} -> ${label}`)
  if (!ok) abort(`${route} 未渲染出「${label}」，hash=${await ev('location.hash')}`)
}
const fill = (sel, val) => `(function(){
  var el = document.querySelector(${JSON.stringify(sel)});
  if (!el) return 'NOT_FOUND';
  Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set.call(el, ${JSON.stringify(val)});
  el.dispatchEvent(new Event('input', { bubbles: true }));
  el.dispatchEvent(new Event('change', { bubbles: true }));
  return 'ok';
})()`
const btnStates = () => ev(`JSON.stringify(Array.from(document.querySelectorAll('button')).map(b => (b.textContent||'').trim().slice(0,18)+(b.disabled?'[off]':'[on]')).filter(Boolean))`)
// 精确文本点击；exact=false 时按 index 取第 n 个同名
// ⚠️ 关键：这些按钮对 el.click() **无反应**，必须派发完整指针序列。
// 实测（scripts/cdp-click-probe.mjs）：任务页「+ 新任务」用 .click() 点不动，
// 派发 pointerdown→mousedown→pointerup→mouseup→click 后 bottom-sheet 正常弹出。
// 真实手指触摸本来就会产生完整序列，所以**不是产品缺陷，是测试方法的坑**。
// 笔记页的「创建」按钮两种方式都work，说明不同组件绑定方式不一致。
const fullClick = (text, { exact = false, index = 0 } = {}) => `(function(){
  var bs = Array.from(document.querySelectorAll('button')).filter(b => ${exact ? `(b.textContent||'').trim() === ${JSON.stringify(text)}` : `(b.textContent||'').indexOf(${JSON.stringify(text)}) >= 0`});
  var b = bs[${index}];
  if (!b) return 'NO_BUTTON';
  if (b.disabled) return 'DISABLED';
  var r = b.getBoundingClientRect();
  var base = { bubbles: true, cancelable: true, composed: true, view: window,
               clientX: r.left + r.width/2, clientY: r.top + r.height/2,
               button: 0, buttons: 1, pointerId: 1, isPrimary: true };
  ['pointerdown','mousedown','pointerup','mouseup','click'].forEach(function(t){
    var Ctor = t.indexOf('pointer') === 0 ? PointerEvent : MouseEvent;
    b.dispatchEvent(new Ctor(t, base));
  });
  return 'dispatched:' + (b.textContent||'').trim().slice(0,16);
})()`
const click = (text, opts) => fullClick(text, opts)

console.log(`=== 真机多模块写操作 ${SERIAL} ===`)
console.log(`deck=${DECK} task=${TASK_TITLE}\n`)

// ---------- 0. 会话 ----------
console.log('--- 0. preflight ---')
await ev(`location.hash = '#/login'`); await sleep(2500)
if (MASTER && await ev(`!!document.querySelector('input[placeholder*="主密码"]')`)) {
  await ev(fill('input[placeholder*="主密码"]', MASTER)); await sleep(1500)
  console.log('  unlock ->', await ev(click('解锁', { exact: true })))
  await sleep(4000)
}
if (await ev(`!!document.querySelector('input[placeholder*="用户名"]')`)) {
  const pass = requireDevPass()
  await ev(fill('input[placeholder*="用户名"]', 'admin'))
  await ev(fill('input[type="password"]', pass)); await sleep(800)
  console.log('  login ->', await ev(click('登录', { exact: true })))
  await sleep(6000)
}
console.log('  ready, hash =', await ev('location.hash'))

// ---------- 1. 闪卡：卡片页 → 就地建卡组（BUG-K 修复后的新入口）----------
// BUG-K 修复前：列表页按钮文案「新建卡组」但实际跳 /flashcards/new（新建卡片页），
// 文案与行为不符；修复后文案改为「新建卡片」，并在卡片页新增「新建卡组」入口。
console.log('\n--- 1. FLASHCARD create deck in card page (BUG-K) ---')
await goto('#/flashcards', `(document.body.innerText||'').indexOf('闪卡') >= 0`, '闪卡列表')
apiCalls.length = 0
console.log('  list buttons =', await btnStates())
console.log('  click 新建卡片 ->', await ev(click('新建卡片')))
const onCardPage = await waitFor(`!!document.querySelector('button.save-link') ? 'card-page' : null`, '卡片编辑页', 12000)
if (!onCardPage) abort('点「新建卡片」后未进入卡片编辑页（文案/路由是否一致？）')
console.log('  on card page, hash =', await ev('location.hash'))

// 卡组入口是**页内 inline 区域**（一个 placeholder="卡组名称" 的 input +
// 一个「新建卡组」按钮），不是弹窗。
//
// 这里踩过一次坑：最初按"点新建卡组会弹出 bottom-sheet"来写，找不到 .bottom-sheet
// / [role=dialog] 就判 FAIL，结论一度写成"BUG-K 的入口在真机上没出现"。
// 用 CDP dump DOM 复查后才发现 input 和按钮都在，只是新建卡组按钮因为
// newDeckName 为空而 disabled —— 是**测试脚本的假设错了**，不是产品缺陷。
// 所以判据直接认 input+button 本身，不再假设容器形态。
const deckInput = await waitFor(
  `!!document.querySelector('input[placeholder="卡组名称"]') ? 'inline' : null`,
  '卡组名称输入框（页内 inline 入口）', 10000)
if (!deckInput) {
  console.log('  btns =', await btnStates())
  console.log('  body =', ((await bodyText()) || '').slice(0, 200))
  record('闪卡：可创建卡组（BUG-K 修复）', false, '卡片页未出现「卡组名称」输入框')
} else {
  console.log('  卡组入口 =', deckInput, '| 新建卡组按钮初始 =',
    await ev(`(function(){ var b=Array.from(document.querySelectorAll('button')).find(x=>(x.textContent||'').trim().indexOf('新建卡组')>=0); return b ? ('disabled='+b.disabled) : '(absent)' })()`))
  console.log('  deck sheet inputs =', await ev(`JSON.stringify(Array.from(document.querySelectorAll('input')).map(i=>i.type+'|'+(i.placeholder||'')))`))
  // 直接按 placeholder 定位。原先用「所有 text input 里取最后一个」，
  // 页面同时有「输入标签后回车」的标签 input，取 .pop() 可能选错元素。
  const deckNameFill = await ev(`(function(){
    var el = document.querySelector('input[placeholder="卡组名称"]');
    if (!el) return 'NO_INPUT';
    Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set.call(el, ${JSON.stringify(DECK)});
    el.dispatchEvent(new Event('input', { bubbles: true }));
    return 'filled:' + el.value;
  })()`)
  console.log('  deck name ->', deckNameFill)
  const deckCreated = await waitFor(
    `(()=>{ var b=Array.from(document.querySelectorAll('button')).find(x=>(x.textContent||'').trim().indexOf('新建卡组')>=0); return (b && !b.disabled) ? 'enabled' : null })()`,
    '「新建卡组」按钮 enabled（依赖 newDeckName）', 8000)
  if (!deckCreated) {
    record('闪卡：可创建卡组（BUG-K 修复）', false, '填了卡组名后「新建卡组」仍 disabled')
  } else {
    console.log('  create deck ->', await ev(fullClick('新建卡组')))
    await sleep(3500)
    const deckApi = apiCalls.filter((c) => c.url.includes('flashcards/decks'))
    console.log('  deck api =', JSON.stringify(deckApi))
    const selected = await ev(`(()=>{ var s=document.querySelector('select'); return s && s.value ? s.value : '(empty)' })()`)
    console.log('  selected deck after create =', selected)
    selectedDeckId = selected !== '(empty)' ? selected : ''
    console.log('  save button now =', await ev(`(function(){ var b=document.querySelector('button.save-link'); return b ? ('disabled='+b.disabled) : '(absent)' })()`))
    record('闪卡：可创建卡组（BUG-K 修复）',
      deckApi.some((c) => c.status >= 200 && c.status < 300) && selected !== '(empty)',
      `deck api=${JSON.stringify(deckApi)} selected=${selected}`)
  }
}
console.log('  after deck, hash =', await ev('location.hash'),
  '| has 正面 =', await ev(`!!document.querySelector('textarea[placeholder="正面"]')`))

// ---------- 2. 闪卡：填正反面 → 保存 ----------
console.log('\n--- 2. FLASHCARD card ---')
apiCalls.length = 0
console.log('  front ->', await ev(fill('textarea[placeholder="正面"]', CARD_FRONT)))
console.log('  back  ->', await ev(fill('textarea[placeholder="背面"]', CARD_BACK)))
const saveOn = await waitFor(
  `(()=>{ var b=document.querySelector('button.save-link'); return (b && !b.disabled) ? 'enabled' : null })()`,
  '保存按钮 enabled', 8000)
if (!saveOn) {
  console.log('  save-link =', await ev(`(()=>{var b=document.querySelector('button.save-link');return b?JSON.stringify({disabled:b.disabled,html:b.outerHTML.slice(0,160)}):'NONE'})()`))
  console.log('  all buttons =', await btnStates())
  record('闪卡：保存按钮可用（此前恒 disabled）', false, 'save-link still disabled after 正面/背面/卡组')
} else {
  console.log('  save ->', await ev(click('保存', { exact: true })))
  await sleep(4000)
  const cardApi = apiCalls.filter((c) => c.url.includes('flashcard'))
  console.log('  card api =', JSON.stringify(cardApi))
  record('闪卡：保存按钮可用（此前恒 disabled）', true, 'enabled after 卡组+正面+背面')
  record('闪卡：卡片保存请求 2xx', cardApi.some((c) => c.status < 300),
    cardApi.length ? JSON.stringify(cardApi) : 'no flashcard API call captured')

  // 验收标准纠正（2026-09-30）：**列表页按设计只显示卡组**
  // （name + 今日待复习数 + N cards），卡片正文在卡组详情页
  // /flashcards/decks/:id。原先断言"列表正文里出现 CARD_FRONT"永远为假，
  // 差点被当成缺陷记进 handoff。
  //
  // 所以分两层断言，且都用**能区分修前/修后**的强判据：
  //   列表层：新建卡组的 totalCards 从 0 变成 1
  //           （BUG-O 修前是 0 cards，因为服务端生成的 card 拉不回来）
  //   详情层：进卡组页后「开始复习」不再 disabled
  await goto('#/flashcards', `(document.body.innerText||'').indexOf('闪卡') >= 0`, '闪卡列表(保存后)')
  await sleep(1500)
  const listText = ((await bodyText()) || '').replace(/\s+/g, ' ')
  const deckHasOne = new RegExp(`${DECK}[^|]{0,40}?1 cards`).test(listText)
  record('闪卡：列表卡组计数 = 1（BUG-O 修复前为 0）', deckHasOne,
    deckHasOne ? `"${DECK}" 后有 1 cards` : `list=${listText.slice(0, 140)}`)

  const deckId = selectedDeckId
  if (deckId) {
    await goto(`#/flashcards/decks/${deckId}`, `!!document.querySelector('button')`, '卡组详情页', 15000)
    await sleep(2000)
    const deckBody = ((await bodyText()) || '').replace(/\s+/g, ' ')
    const startBtns = await ev(`JSON.stringify(Array.from(document.querySelectorAll('button')).map(b=>(b.textContent||'').trim()+'|'+(b.disabled?'dis':'en')).filter(s=>/复习/.test(s)))`)
    const startEnabled = (startBtns || '').includes('|en')
    record('闪卡：卡组页「开始复习」可用（BUG-O 修复前恒 disabled）', startEnabled,
      `${startBtns} | body=${deckBody.slice(0, 120)}`)
    record('闪卡：卡组页能看到刚建的卡片', deckBody.includes(CARD_FRONT) || deckBody.includes(CARD_BACK),
      deckBody.includes(CARD_FRONT) ? 'front shown' : `body=${deckBody.slice(0, 140)}`)
  } else {
    record('闪卡：卡组页「开始复习」可用（BUG-O 修复前恒 disabled）', false, '未取到 deckId，跳过')
  }
}

// ---------- 3. 任务 ----------
console.log('\n--- 3. TASK create ---')
await goto('#/tasks', `(document.body.innerText||'').indexOf('任务') >= 0`, '任务列表')
apiCalls.length = 0
const beforeTasks = await ev(`(function(){ var m=(document.body.innerText||'').match(/全部正常 . (\\d+)/); return m?m[1]:'?' })()`)
console.log('  tasks before =', beforeTasks)
console.log('  click + 新任务 ->', await ev(click('新任务')))
const sheet = await waitFor(`!!document.querySelector('.bottom-sheet, [class*="sheet"]') ? 'sheet-open' : null`, '创建任务弹窗', 12000)
if (!sheet) abort('任务创建弹窗未打开（full-click 仍无效）')
console.log('  sheet opened')
console.log('  title  ->', await ev(fill('input[placeholder*="任务标题"]', TASK_TITLE)))
console.log('  desc   ->', await ev(fill('textarea[placeholder*="任务描述"]', `描述-${stamp}`)))
await sleep(800)
console.log('  btns =', await btnStates())
// sheet 标题是「创建任务」，但确认按钮文本只是「创建」——别用 exact 匹配标题
const taskCreate = await ev(click('创建', { exact: true }))
console.log('  create task ->', taskCreate)
if (taskCreate === 'NO_BUTTON' || taskCreate === 'DISABLED') {
  console.log('  btns =', await btnStates())
  abort(`任务 sheet 确认按钮不可用: ${taskCreate}`)
}
await sleep(4500)
const taskApi = apiCalls.filter((c) => c.url.includes('/api/tasks'))
console.log('  task api =', JSON.stringify(taskApi))
const afterTasks = (await bodyText()) || ''
const createApi = taskApi.find((c) => c.status === 201 || (c.status >= 200 && c.status < 300))
record('任务：创建请求 2xx（此前 503）', !!createApi, taskApi.length ? JSON.stringify(taskApi) : 'no task API call captured')
// ⚠️ 不要用「列表回显」当断言：任务列表默认带 `?source=opencode` 过滤，
// 而本 UI 创建的是 source=local；且 API 侧按 workspace 隔离
// （UI 落到 workspace=default，admin dev token 落在 ws_user-admin）。
// 权威判据是**直接查库**：
//   select id,title,source,workspace_id from opencode_pocket.tasks order by created_at desc
//   -> task-1790735438707 | Maestro任务417149 | local | default
const listedInView = afterTasks.includes(TASK_TITLE)
console.log(`  (列表当前筛选 source=opencode，UI 建的是 local，故不显示属预期：listedInView=${listedInView})`)

// ---------- 汇总 ----------
const pass = results.filter((r) => r.pass).length
console.log(`\n=== SUMMARY ${pass}/${results.length} ===`)
for (const r of results) console.log(`  ${r.pass ? 'PASS' : 'FAIL'}  ${r.n}  ${r.d || ''}`)
ws.close()
process.exit(pass === results.length ? 0 : 4)
