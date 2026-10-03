#!/usr/bin/env node
/**
 * diag-empty-state.mjs — 诊断「空态 + 内联建组」在真机上的真实 DOM 状态。
 *
 * 上一轮 verify-bug-u.mjs 报了两条互相矛盾的 FAIL：
 *   - 填名后提交按钮仍 disabled
 *   - 「空态消失」失败，但「新卡组名出现在 DOM」通过
 * 第二条尤其可疑：`.empty` 还在，卡组名却出现在 innerText 里。
 * **内联输入框的 value 不算 innerText**，所以它要么来自别处，要么这条判据
 * 本身太弱（能因为错误的原因通过）。这里把 DOM 真实结构打出来，别猜。
 *
 * 用法：POCKET_SERIAL=... POCKET_MASTER=... node scripts/diag-empty-state.mjs
 */
import { execFileSync } from 'node:child_process'
import { requireDevPass } from './lib/dev-pass.mjs'
const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9248'
const MASTER = process.env.POCKET_MASTER || ''
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

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
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true }))?.result?.value

if (MASTER) {
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
}

// ⚠️ 必须先清 localStorage 再让视图挂载：FlashcardListView 的 onMounted 会
// `store.loadFromCache()`，把 `flashcards:v1` 里的旧卡组灌回来。服务端清空了
// 但本地缓存还在时，空态**根本不会渲染**，诊断会读到"没有表单"——
// 上一版诊断就栽在这里，误以为表单没渲染。
console.log('清 localStorage 并重载 …')
await ev(`(function(){try{localStorage.removeItem('flashcards:v1')}catch(e){};return localStorage.length})()`)
await send('Page.enable')
await send('Page.reload', { ignoreCache: false })
await sleep(6000)

console.log('清缓存后 hash =', await ev('location.hash'))
await ev(`location.hash = '#/flashcards'`)
const dl = Date.now() + 12000
while (Date.now() < dl && (await ev('location.hash')) !== '#/flashcards') await sleep(300)
await sleep(2500)

console.log('hash =', await ev('location.hash'))

// 限定在可见 pane（FoldAwareLayout 同时渲染 outer/inner，靠 CSS 隐藏其一）
const PANE = `(function(){
  var panes = document.querySelectorAll('.inner-pane, .outer-pane');
  for (var i=0;i<panes.length;i++){ if (panes[i].offsetParent !== null) return panes[i]; }
  return document.body;
})()`

console.log('\n--- 1. 结构盘点 ---')
console.log(await ev(`(function(){
  var pane = ${PANE};
  return JSON.stringify({
    visiblePaneClass: pane.className,
    panesInDom: document.querySelectorAll('.inner-pane, .outer-pane').length,
    deckCreateForms: pane.querySelectorAll('[data-testid="deck-create-form"]').length,
    deckCreateFormsAnywhere: document.querySelectorAll('[data-testid="deck-create-form"]').length,
    submitButtonsInForm: pane.querySelectorAll('[data-testid="deck-create-form"] button').length,
    emptyByTestId: pane.querySelectorAll('[data-testid="flashcards-empty"]').length,
    legacyEmptyByClass: document.querySelectorAll('.empty').length,
    legacyEmptyInnerHTML: (document.querySelector('.empty')||{}).outerHTML ? (document.querySelector('.empty').outerHTML||'').slice(0,80) : null
  }, null, 1);
})()`))

const FILL = (value) => `(function(){
  var pane = ${PANE};
  var el = pane.querySelector('[data-testid="deck-create-form"] input');
  if (!el) return JSON.stringify({err:'NO_INPUT'});
  var setter = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;
  setter.call(el, ${JSON.stringify(value)});
  var readBack = el.value;
  el.dispatchEvent(new Event('input', { bubbles: true }));
  el.dispatchEvent(new Event('change', { bubbles: true }));
  return JSON.stringify({ readBack: readBack });
})()`

const STATE = (tag) => `(function(){
  var pane = ${PANE};
  var form = pane.querySelector('[data-testid="deck-create-form"]');
  var el = form ? form.querySelector('input') : null;
  var btn = form ? form.querySelector('button') : null;
  return JSON.stringify({
    tag: ${JSON.stringify(tag)},
    inputValue: el ? el.value : null,
    disabledProp: btn ? btn.disabled : null,
    disabledAttr: btn ? btn.getAttribute('disabled') : null,
    hasForm: !!form
  });
})()`

console.log('\n--- 2. 填值前 ---')
console.log(await ev(STATE('before')))
console.log('\n--- 3. 填值并派发 input/change ---')
console.log(await ev(FILL('DIAG-DECK-1')))

for (const ms of [200, 500, 1000, 2000, 4000]) {
  await sleep(ms === 200 ? 200 : ms - 0)
  console.log(`\n--- 4.${ms}ms 后 ---`)
  console.log(await ev(STATE(`t+${ms}ms`)))
}

console.log('\n--- 5. 绕过按钮，直接给 form 派发 submit（看 @submit.prevent 是否生效）---')
console.log(await ev(`(function(){
  var pane = ${PANE};
  var form = pane.querySelector('[data-testid="deck-create-form"]');
  if(!form) return 'NO_FORM';
  var ev2 = new Event('submit', { bubbles: true, cancelable: true });
  var notCancelled = form.dispatchEvent(ev2);
  return JSON.stringify({ dispatched: true, notCancelled: notCancelled, inputValue: (form.querySelector('input')||{}).value });
})()`))
await sleep(2500)
console.log('\n--- 6. submit 之后 ---')
console.log(await ev(STATE('after-submit')))
console.log(await ev(`(function(){
  var pane = ${PANE};
  var items = pane.querySelectorAll('[data-testid="flashcards-deck-item"]');
  var names=[]; for(var i=0;i<items.length;i++) names.push((items[i].innerText||'').split('\\n')[0]);
  return JSON.stringify({ formStillThere: !!pane.querySelector('[data-testid="deck-create-form"]'), deckItems: names });
})()`))
process.exit(0)
