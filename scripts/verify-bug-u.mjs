#!/usr/bin/env node
/**
 * verify-bug-u.mjs — 真机验证 BUG-U：零卡组时列表页能**就地**建卡组。
 *
 * ## 缺陷是什么
 *
 * 修之前：`/flashcards` 的空态只有一个「新建卡片」按钮，跳 `/flashcards/new`
 * （卡片编辑页）。但那页的「保存」在没有卡组时**恒 disabled**
 * （selectedDeckId 为空 → isValid false）。用户点进去才发现要先建组，
 * 而建组入口是那页顶部的另一个输入框。**从零状态看这是一个死胡同。**
 *
 * 修之后：空态直接内联建组表单，建完列表立刻出现卡组。
 *
 * ## 怎么才算验过（判据必须能区分通/不通）
 *
 * 「页面渲染了」「有个输入框」几乎恒真，不算证据。这里用四段式：
 *   1. **前置**：服务端 deck 数 = 0（直接查 PG，不看 UI 文本）
 *   2. **空态真出现**：空态容器存在，且**内联建组 input + submit button 存在**
 *   3. **真的能建**：填名 → 点提交 → 空态消失、卡组名出现在 DOM
 *   4. **真的落库**：直接查 PG 看到新卡组（不信 localStorage、不信 API 返回）
 *
 * 第 4 步是唯一能排除「只是 UI 假象」的判据。前三步全过但第 4 步不过，
 * 仍然算失败。
 *
 * ## 对照组
 *
 * 同一支脚本在**有卡组**状态下必须能看到卡组列表而不是空态表单。
 * 没有对照组的话，「空态永远显示」和「空态正确显示」在报告上一样。
 *
 * 用法：POCKET_SERIAL=... POCKET_MASTER=... node scripts/verify-bug-u.mjs
 */
import { execFileSync } from 'node:child_process'
import { requireDevPass } from './lib/dev-pass.mjs'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9247'
const MASTER = process.env.POCKET_MASTER || ''
/** 直接查 PG —— 落库判据的唯一可信来源。
 *  用全限定表名而不是 `SET search_path`：`SET` 会把额外行混进 -t -A 的输出，
 *  导致 count() 解析成 NaN（踩过一次）。
 *  psql 路径要能解析：`logs/` 是 gitignored，在 git worktree 里不存在，
 *  所以按 POCKET_PSQL → 相对路径 → 主仓库绝对路径 依次找。 */
function resolvePsql() {
  const cands = [
    process.env.POCKET_PSQL,
    'logs/pg/dist2/pgsql/bin/psql.exe',
    'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe',
  ].filter(Boolean)
  for (const c of cands) {
    try {
      execFileSync(c, ['--version'], { stdio: 'ignore' })
      return c
    } catch { /* 试下一个 */ }
  }
  console.error('找不到 psql.exe，请设置 POCKET_PSQL 环境变量')
  process.exit(4)
}
const PSQL = resolvePsql()
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

/** 直接查 PG —— 落库判据的唯一可信来源。
 *  用全限定表名而不是 `SET search_path`：`SET` 会把额外行混进 -t -A 的输出，
 *  导致 count() 解析成 NaN（踩过一次）。 */
function pg(sql) {
  const out = execFileSync(PSQL, ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres',
    '-t', '-A', '-c', sql], { encoding: 'utf8' }).trim()
  const m = out.match(/-?\d+/)
  return m ? Number(m[0]) : NaN
}
const deckCount = () => pg('select count(*) from opencode_pocket.flashcard_deck_config;')
const deckNames = () => execFileSync(PSQL, ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres',
  // 兜底串必须是纯 ASCII：中文会经系统 ANSI 码页传给 psql，报
  // `invalid byte sequence for encoding UTF8`（踩过一次）。
  '-t', '-A', '-c', "select coalesce(string_agg(name,'|' order by created_at),'(none)') from opencode_pocket.flashcard_deck_config;"],
  { encoding: 'utf8' }).trim()

// ---------- 连 CDP ----------
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let id = 0
const pending = new Map()
const errors = []
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  if (m.method === 'Runtime.exceptionThrown') {
    errors.push(m.params?.exceptionDetails?.exception?.description || m.params?.exceptionDetails?.text || '')
  }
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
// 不带超时：超时只说明"没在窗口内跑完"，不能推断"卡住"
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true }))?.result?.value

// 等 WebView 真正加载出文档。装完包立刻连 CDP 时 location.origin 还是 null，
// 直接往下走会拿到"origin=null"这种假故障。
let origin = null
const readyDl = Date.now() + 20000
while (Date.now() < readyDl) {
  origin = await ev('location.origin')
  if (origin && origin !== 'null') break
  await sleep(500)
}
console.log('origin =', origin, ' (必须是 http://localhost)')
if (origin !== 'http://localhost') {
  console.log('装的是生产(https)包或 WebView 尚未就绪 —— 后续断言无意义，直接中止。')
  process.exit(5)
}

// ---------- 会话恢复（重启后必须先解锁，否则路由守卫弹回 /login） ----------
await ev(`location.hash = '#/login'`); await sleep(2600)
if (await ev(`!!document.querySelector('input[placeholder*="主密码"]')`)) {
  await ev(`(function(){var el=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(MASTER)});el.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`)
  await sleep(1700)
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim().indexOf('解锁')>=0});if(b)b.click();return 1})()`)
  await sleep(4200)
}
if (await ev(`!!document.querySelector('input[placeholder*="用户名"]')`)) {
  const devPass = requireDevPass()
  const fillBy = (sel, val) => `(function(){var el=document.querySelector(${JSON.stringify(sel)});if(!el)return 'NF';var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(val)});el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return 'ok'})()`
  await ev(fillBy('input[placeholder*="用户名"]', 'admin'))
  await ev(fillBy('input[type="password"]', devPass)); await sleep(900)
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim().indexOf('登录')>=0});if(b)b.click();return 1})()`)
  await sleep(6500)
}

// 清掉本地缓存，否则 loadFromCache 会把旧卡组灌回来，空态永远不出现
await ev(`(function(){try{localStorage.removeItem('flashcards:v1')}catch(e){};return 1})()`)

const checks = []
const check = (name, pass, detail) => {
  checks.push({ name, pass, detail })
  console.log(`${pass ? 'PASS' : 'FAIL'}  ${name}${detail ? '  — ' + detail : ''}`)
}
const DECK = 'BUGU-ZEROSTATE-DECK'

// ---------- 前置：服务端为空 ----------
const before = deckCount()
check('前置：服务端 deck 数为 0', before === 0, `PG 实际 ${before}`)
if (before !== 0) {
  console.log('\n服务端非空，无法验证「零卡组」路径。请先清空 flashcard_deck_config。')
  process.exit(3)
}

// ---------- 导航到列表页 ----------
await ev(`location.hash = '#/flashcards'`)
const deadline = Date.now() + 12000
while (Date.now() < deadline && (await ev('location.hash')) !== '#/flashcards') await sleep(300)
await sleep(2000)

// ---------- 步骤 2：空态 + 内联建组表单 ----------
//
// ⚠️ 两个已踩过的坑，写在这里防止第三次：
//  1. `.empty` 这个类名**撞车**：列表里的徽章是 `<span class="badge empty">`，
//     `querySelector('.empty')` 拿到的是徽章而不是空态容器。于是
//     「空态是否消失」这条判据构造上就永远失败 —— 而功能其实是好的。
//     改为 `[data-testid="flashcards-empty"]`。
//  2. `FoldAwareLayout` **故意同时渲染** #outer / #inner 两个 slot，
//     靠 CSS `display:none` 隐藏其一。所以任何 `querySelectorAll` 都会拿到
//     两份，必须限定在**可见 pane**（offsetParent !== null）。
//     之前 `document.querySelectorAll('form.deck-create').length === 0`
//     不是因为没渲染，而是因为当时已有卡组、空态整体不在了。
const VISIBLE_PANE = `(function(){
  var panes = document.querySelectorAll('.inner-pane, .outer-pane');
  for (var i = 0; i < panes.length; i++) {
    if (panes[i].offsetParent !== null) return panes[i];
  }
  return document.body;
})()`

const emptyUI = await ev(`(function(){
  var pane = ${VISIBLE_PANE};
  var empty = pane.querySelector('[data-testid="flashcards-empty"]');
  var form  = pane.querySelector('[data-testid="deck-create-form"]');
  var input = form ? form.querySelector('input') : null;
  var btn   = form ? form.querySelector('button[type=submit]') : null;
  return JSON.stringify({
    hasEmpty: !!empty,
    hasForm: !!form,
    hasInput: !!input,
    hasSubmit: !!btn,
    submitDisabled: btn ? btn.disabled : null,
    inputPlaceholder: input ? input.getAttribute('placeholder') : null,
    submitText: btn ? (btn.textContent||'').trim() : null,
    deckItems: pane.querySelectorAll('[data-testid="flashcards-deck-item"]').length,
    bodyLen: (document.body.innerText||'').replace(/\\s+/g,' ').trim().length
  });
})()`)
const ui = JSON.parse(emptyUI || '{}')
check('空态容器出现（可见 pane 内）', ui.hasEmpty === true, `bodyLen=${ui.bodyLen} deckItems=${ui.deckItems}`)
check('内联建组表单存在', ui.hasForm === true)
check('建组输入框存在', ui.hasInput === true, `placeholder=${ui.inputPlaceholder}`)
check('提交按钮存在且初始 disabled（空名字不该能提交）', ui.hasSubmit === true && ui.submitDisabled === true, `text=${ui.submitText}`)

// ---------- 对照组自证：填了名字后按钮必须变可用 ----------
//
// ⚠️ 必须先等 `store.loading` 落定再交互：FlashcardListView 的 onMounted 会
// `loadFromCache()` + `refresh()`，refresh 期间 `v-if="store.loading"` 会把整个
// 表单**卸载**。在飞行途中填值，随后 refresh 完成重新挂载，v-model 重新绑定 ——
// 读到的 disabled 会是"旧节点"的状态。上一版就是在这里拿到假 FAIL 的
// （判据错，feature 是好的）。判据应当是**等状态达到期望**，不是等够时间。
const LOADING_GONE = `(function(){
  var pane = ${VISIBLE_PANE};
  return !(pane.querySelector('.state') || document.body.innerText.indexOf('加载中') >= 0);
})()`
let settled = false
const setDl = Date.now() + 15000
while (Date.now() < setDl) {
  if ((await ev(LOADING_GONE)) === true) { settled = true; break }
  await sleep(400)
}
check('列表加载完成（loading 态消失后才交互）', settled === true)

const fillRes = await ev(`(function(){
  var pane = ${VISIBLE_PANE};
  var el = pane.querySelector('[data-testid="deck-create-form"] input');
  if(!el) return JSON.stringify({ err: 'NO_INPUT' });
  var s = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;
  s.call(el, ${JSON.stringify(DECK)});
  var readBack = el.value;
  el.dispatchEvent(new Event('input',{bubbles:true}));
  el.dispatchEvent(new Event('change',{bubbles:true}));
  return JSON.stringify({ readBack: readBack });
})()`)
const fill = JSON.parse(fillRes || '{}')
check('填值回读一致（v-model 的输入确实落到 DOM）', fill.readBack === DECK, `readBack=${fill.readBack}`)

// 轮询到按钮变可用或超时，而不是固定 sleep。
// 每次都把**那一刻的完整状态**带回来（输入值 / disabled 属性与 property /
// 该 selector 命中几个节点 / 所在 pane），否则只剩一个 `disabled=true` 时，
// 无法区分「v-model 没生效」和「读错了节点」。
let enabled = null
let snapshot = null
const enDl = Date.now() + 8000
while (Date.now() < enDl) {
  await sleep(300)
  snapshot = JSON.parse((await ev(`(function(){
    var panes = document.querySelectorAll('.inner-pane, .outer-pane');
    var pane = null, paneIdx = -1;
    for (var i=0;i<panes.length;i++){ if (panes[i].offsetParent !== null) { pane = panes[i]; paneIdx = i; break; } }
    if (!pane) pane = document.body;
    var form = pane.querySelector('[data-testid="deck-create-form"]');
    var el = form ? form.querySelector('input') : null;
    var btns = pane.querySelectorAll('[data-testid="deck-create-form"] button');
    var btn = btns[0] || null;
    return JSON.stringify({
      paneIdx: paneIdx,
      paneClass: pane.className || '(body)',
      formCount: pane.querySelectorAll('[data-testid="deck-create-form"]').length,
      formInDom: document.querySelectorAll('[data-testid="deck-create-form"]').length,
      inputValue: el ? el.value : null,
      btnCount: btns.length,
      disabledProp: btn ? btn.disabled : null,
      disabledAttr: btn ? btn.getAttribute('disabled') : null,
      btnType: btn ? btn.getAttribute('type') : null
    });
  })()`)) || '{}')
  enabled = snapshot.disabledProp
  if (enabled === false) break
}
check('填名后提交按钮变为可用（v-model 真的连上了）', enabled === false,
  `disabled=${enabled} ${JSON.stringify(snapshot)}`)

// ---------- 步骤 3 + 4：点提交 → DOM 变化 + PG 落库 ----------
await ev(`(function(){var pane=${VISIBLE_PANE};var b=pane.querySelector('[data-testid="deck-create-form"] button[type=submit]');if(b)b.click();return 1})()`)
await sleep(3000)

// ⚠️ 收紧判据：不能用 `innerText.indexOf(DECK) >= 0` —— 输入框的残留 value、
// 错误提示文案都可能让这条「因为错误的原因通过」。必须看到**卡组条目节点**。
const after = JSON.parse((await ev(`(function(){
  var pane = ${VISIBLE_PANE};
  var items = pane.querySelectorAll('[data-testid="flashcards-deck-item"]');
  var names = [];
  for (var i=0;i<items.length;i++) names.push((items[i].innerText||'').split('\\n')[0]);
  return JSON.stringify({
    emptyStillThere: !!pane.querySelector('[data-testid="flashcards-empty"]'),
    formStillThere: !!pane.querySelector('[data-testid="deck-create-form"]'),
    deckItemCount: items.length,
    deckItemNames: names
  });
})()`)) || '{}')
check('空态在提交后消失', after.emptyStillThere === false)
check('建组表单在提交后消失', after.formStillThere === false)
check('卡组条目节点出现（不是 innerText 碰巧含名字）',
  after.deckItemCount === 1 && String(after.deckItemNames?.[0] || '').includes(DECK),
  `items=${JSON.stringify(after.deckItemNames)}`)

const pgCount = deckCount()
const names = deckNames()
check('PG 落库（不信 UI，不信 localStorage）', pgCount === 1 && names.includes(DECK), `PG deck 数=${pgCount} names=${names}`)

const errs = errors.filter((e) => e && !/favicon/i.test(e))
check('无未捕获 JS 异常', errs.length === 0, errs.slice(0, 2).join(' | ') || '无')

console.log('\n=== 汇总 ===')
const passed = checks.filter((c) => c.pass).length
console.log(`${passed}/${checks.length} 通过`)
checks.filter((c) => !c.pass).forEach((c) => console.log(`  FAIL: ${c.name} — ${c.detail || ''}`))
process.exit(passed === checks.length ? 0 : 1)
