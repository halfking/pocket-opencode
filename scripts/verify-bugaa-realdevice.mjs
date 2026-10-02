#!/usr/bin/env node
/**
 * verify-bugaa-realdevice.mjs — **真机**验证 BUG-AA。
 *
 * ## 为什么用 CDP 而不是 Maestro
 *
 * 本轮实测确认（见 handoff §4.16.3 的矩阵）：这台 MIUI 真机**拦全新安装**
 * （Maestro driver 装不上，`adb install` / `pm install` 都是
 * INSTALL_FAILED_USER_RESTRICTED），**但不拦更新已装应用**
 * （`adb install -r` 对本项目 APK 返回 Success）。
 * 所以「真机验证」与「真机 Maestro」是**两件事**——本脚本走 CDP，拿到的是同样的真机证据。
 *
 * ## 缺陷是什么
 *
 * 1. `FlashcardListView` 主 CTA 文案键 `flashcards.list.create`，跳 `/flashcards/new`
 *    （**新建卡片**页）；BUG-K 只把 zh-CN / en-US 改对了，其余 7 种语言仍是「建卡组」的直译。
 * 2. `StudyHubView` 零卡组空态的按钮文案是「新建牌组 / New deck」，点击同样跳
 *    `/flashcards/new` —— 9/9 全错，且从零状态进去必然撞 BUG-U 那个死胡同。
 *
 * ## 怎么才算验过（判据必须能区分通/不通）
 *
 * 「页面渲染了」几乎恒真，不算证据。这里做四段：
 *   1. **前置**：服务端 deck 数 = 0（**直接查 PG**，不看 UI 文本、不信 localStorage）
 *   2. **文案正确**：空态里**没有**「新建卡组 / New deck」字样，且建组输入框存在
 *   3. **真能建**：填名 → 提交 → 空态消失、卡组名进 DOM
 *   4. **真落库**：**直接查 PG** 看到新卡组
 *
 * 第 4 步是唯一能排除「UI 假象」的判据；前三步过而第 4 步不过，仍算失败。
 *
 * ## 对照组（缺了它，「空态永远显示」和「空态正确显示」在报告上一样）
 *
 * 建完之后页面必须**不再**是空态、而是卡组列表。
 *
 * 用法：POCKET_SERIAL=... POCKET_MASTER=... node scripts/verify-bugaa-realdevice.mjs
 */
import { execFileSync } from 'node:child_process'
import { requireDevPass } from './lib/dev-pass.mjs'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9251'
const MASTER = process.env.POCKET_MASTER || ''

function resolvePsql() {
  const cands = [
    process.env.POCKET_PSQL,
    'logs/pg/dist2/pgsql/bin/psql.exe',
    'C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe',
  ].filter(Boolean)
  for (const c of cands) {
    try { execFileSync(c, ['--version'], { stdio: 'ignore' }); return c } catch { /* next */ }
  }
  console.error('找不到 psql.exe，请设置 POCKET_PSQL')
  process.exit(4)
}
const PSQL = resolvePsql()
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

function pgNum(sql) {
  const out = execFileSync(PSQL, ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres',
    '-t', '-A', '-c', sql], { encoding: 'utf8' }).trim()
  const m = out.match(/-?\d+/)
  return m ? Number(m[0]) : NaN
}
// 兜底串必须是纯 ASCII：中文会经系统 ANSI 码页传给 psql 报 invalid byte sequence
const deckNames = () => execFileSync(PSQL, ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres',
  '-t', '-A', '-c', "select coalesce(string_agg(name,'|' order by created_at),'(none)') from opencode_pocket.flashcard_deck_config;"],
  { encoding: 'utf8' }).trim()
const deckCount = () => pgNum('select count(*) from opencode_pocket.flashcard_deck_config;')

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
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true }))?.result?.value

let origin = null
const readyDl = Date.now() + 20000
while (Date.now() < readyDl) {
  origin = await ev('location.origin')
  if (origin && origin !== 'null') break
  await sleep(500)
}
console.log('origin =', origin, '（必须是 http://localhost）')
if (origin !== 'http://localhost') { console.log('装的是生产(https)包或 WebView 未就绪，中止。'); process.exit(5) }

// ---------- 登录 ----------
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
  // ⚠️ 必须**精确匹配**「登录」：页面上还有「密码登录」那个 tab，
  //    用 indexOf 会点到 tab 上（踩过一次）。
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='登录'});if(b)b.click();return b?1:0})()`)
  await sleep(6500)
}
const hashAfterLogin = await ev('location.hash')
console.log('登录后 hash =', hashAfterLogin)

// ---------- 清缓存，强制走「零卡组」路径 ----------
await ev(`(function(){try{localStorage.removeItem('flashcards:v1')}catch(e){};return 1})()`)

const checks = []
const check = (name, pass, detail) => {
  checks.push({ name, pass, detail })
  console.log(`${pass ? 'PASS' : 'FAIL'}  ${name}${detail ? '  — ' + detail : ''}`)
}
const DECK = 'BUGAA-STUDY-DECK'

// ---------- 1. 前置：服务端为空 ----------
const before = deckCount()
check('前置：服务端 deck 数为 0（直接查 PG）', before === 0, `PG 实际 ${before}`)
if (before !== 0) {
  console.log('\n服务端非空，无法验证「零卡组」路径。先清空 flashcard_deck_config。')
  process.exit(3)
}

// ---------- 2. 导航到 /study ----------
await ev(`location.hash = '#/study'`)
const dl = Date.now() + 15000
while (Date.now() < dl && (await ev('location.hash')) !== '#/study') await sleep(300)
await sleep(2500)

// 等状态到达期望（不固定 sleep）：空态出现 或 列表出现
const PANE = `(function(){
  var panes = document.querySelectorAll('.inner-pane, .outer-pane');
  for (var i=0;i<panes.length;i++){ if (panes[i].offsetParent !== null) return panes[i]; }
  return document.body;
})()`
const st = Date.now() + 20000
let emptyVisible = false
while (Date.now() < st) {
  emptyVisible = await ev(`!!(${PANE}).querySelector('[data-testid="study-empty"]')`)
  if (emptyVisible) break
  await sleep(400)
}
check('StudyHub 零卡组空态出现（可见 pane 内，data-testid 钩子）', emptyVisible === true, `hash=${await ev('location.hash')}`)

// ---------- 3. 行为判据（**不能用文本匹配**） ----------
//
// 第一版这里写的是「空态文案不含『建卡组 / New deck』」，结果**误报**：
// 修复后的空态本来就应该有「新建卡组」这个**诚实**的建组按钮标签，
// 于是 11/12 里唯一的 FAIL 是判据自己的错。
//
// 教训同本项目的 `deck.create` 断言：**文本匹配区分不了「标签在说谎」和「标签说实话」**。
// 缺陷的本质是「点了会跳到新建卡片页」，所以判据必须是**行为**：
// 点提交之后 location.hash 必须**仍然是 #/study**（没跳走）。
// 修之前那个按钮点了会跳 #/flashcards/new —— 这条判据对它有区分能力。
const emptyText = await ev(`(function(){var e=(${PANE}).querySelector('[data-testid="study-empty"]');return e?e.innerText:''})()`)
console.log('   空态文案 =', JSON.stringify(emptyText))
// 旧形态是「一个 button、没有 input」；新形态是「带 input 的内联表单」。
// 断言 input 存在 = 旧代码结构已被替换掉。
const hasInput = await ev(`!!(${PANE}).querySelector('[data-testid="study-deck-name-input"]')`)
check('内联建卡组输入框存在（旧代码是纯 button、无 input）', hasInput === true)
const hasSubmit = await ev(`(function(){var b=(${PANE}).querySelector('[data-testid="study-deck-create-submit"]');return b?{text:(b.textContent||'').trim(),disabled:b.disabled}:null})()`)
check('提交按钮存在且初始 disabled', !!hasSubmit && hasSubmit.disabled === true, JSON.stringify(hasSubmit))
check('提交按钮文案是「建卡组」（这次是**正确**的标签）',
  !!hasSubmit && /新建卡组/.test(hasSubmit.text || ''), hasSubmit ? hasSubmit.text : '(无)')

// ---------- 4. 真能建，且**不跳走** ----------
const hashBefore = await ev('location.hash')
const fillRes = await ev(`(function(){
  var el=(${PANE}).querySelector('[data-testid="study-deck-name-input"]');
  if(!el) return 'NO_INPUT';
  var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;
  s.call(el,${JSON.stringify(DECK)}); el.dispatchEvent(new Event('input',{bubbles:true})); return 'ok';
})()`)
await sleep(900)
const afterFill = await ev(`(function(){var b=(${PANE}).querySelector('[data-testid="study-deck-create-submit"]');return b?b.disabled:null})()`)
check('填名后提交按钮变为可用（等状态，不固定 sleep）', fillRes === 'ok' && afterFill === false, `disabled=${afterFill}`)

// ★ 核心行为判据：点完仍在 #/study。
//
// ⚠️ 证伪时发现这条**曾经是空过**：第一版只点 `[data-testid="study-deck-create-submit"]`，
// 而修复前版本根本没有这个元素 → 点击表达式成了 no-op → hash 自然不变 → **PASS**。
// 也就是说那条判据单独**没有区分能力**，属于「静默通过」陷阱。
// 现在改成：必须先确认**空态里确实有一个可点的建组控件**，没有就直接判 FAIL，
// 而不是把「没东西可点」当成「点了没跳走」。
const ctaInfo = await ev(`(function(){
  var p=(${PANE});
  var s=p.querySelector('[data-testid="study-deck-create-submit"]');
  if(s) return {found:true,tag:s.tagName,text:(s.textContent||'').trim(),how:'submit'};
  // 回退到修复前形态的空态按钮。必须用 **div.empty button**：
  // 该文件里同时有 <span class="deck-badge empty">，用 .empty 或 button 会撞车
  // （与 §4.22 同一个坑）。限定标签名才只命中空态容器。
  var bs=p.querySelectorAll('div.empty button');
  if(bs.length) return {found:true,tag:bs[0].tagName,text:(bs[0].textContent||'').trim(),how:'legacy-div-empty-button'};
  return {found:false};
})()`)
check('空态里存在可点击的建组控件（没有则判 FAIL，不当空过）', ctaInfo.found === true, JSON.stringify(ctaInfo))

const clickedHow = await ev(`(function(){
  var p=(${PANE});
  var s=p.querySelector('[data-testid="study-deck-create-submit"]');
  if(s){s.click();return 'submit';}
  var bs=p.querySelectorAll('div.empty button');
  if(bs.length){bs[0].click();return 'legacy-div-empty-button';}
  return 'none';
})()`)
await sleep(3500)
const hashAfter = await ev('location.hash')
check('**点击后未跳走到新建卡片页**（BUG-AA 核心行为判据）', hashAfter === hashBefore,
  `before=${hashBefore} after=${hashAfter} 点击的是=${clickedHow}`)

const emptyGone = await ev(`!(${PANE}).querySelector('[data-testid="study-empty"]')`)
check('空态在提交后消失', emptyGone === true)

const bodyHasDeck = await ev(`(function(){return (document.body.innerText||'').indexOf(${JSON.stringify(DECK)})>=0})()`)
check('卡组名出现在页面文本中', bodyHasDeck === true)

// ---------- 5. 真落库：直接查 PG ----------
const names = deckNames()
check('**直接查 PG** 确认落库（唯一能排除 UI 假象的判据）', names.includes(DECK), `PG names=${names}`)

// ---------- 6. 对照组：不再显示空态 ----------
const listShown = await ev(`!!(${PANE}).querySelector('[data-testid="study-deck-list"]')`)
check('对照组：建完后显示卡组列表而非空态表单', listShown === true)

check('无未捕获 JS 异常', errors.length === 0, errors.slice(0, 2).join(' | ') || '0 条')

console.log('\n=== 汇总 ===')
const passed = checks.filter((c) => c.pass).length
console.log(`${passed}/${checks.length} 通过`)
checks.filter((c) => !c.pass).forEach((c) => console.log(`  FAIL: ${c.name} — ${c.detail || ''}`))
process.exit(passed === checks.length ? 0 : 1)
