/**
 * diag-flashcard-save-click.mjs —— 定位「闪卡保存点了没反应」的真因。
 *
 * 缺陷现场（2026-10-22 实测，emulator-5556 / AOSP Android 14，en-US）：
 *   .maestro/flashcards-write.yaml 第 5 步
 *     - tapOn: { text: "保存|Save", enabled: true }   → Maestro 报 COMPLETED
 *   然后页面**仍停在建卡页**（失败现场 screen-hierarchy 里 Front/Back 都已填好、
 *   Save 按钮 enabled），后端 PG 里 flashcard_notes = 0（**连 note 都没建**）。
 *
 * 三个互斥假设，这个脚本一次分清：
 *   H1 合成 tap 根本没产生 DOM click（WebView 不把合成 tap 当 click）
 *      —— 与本仓已有的同族记录一致（flow 注释：建卡组的 form submit 被 tap 吞掉，
 *         「页面内直接 b.click() 是好的」）
 *   H2 click 产生了，但 save() 内部抛错 / 静默返回
 *      —— flushOutbox() 在 !online 时**静默 return**（stores/flashcards.ts:459），
 *         不抛错也不提示；而 save() 的 catch 会把错误写进 <p class="error">
 *   H3 click 产生了且保存成功 —— 与现场不符，用来排除
 *
 * 做法：连 CDP → 直接进建卡页 URL → 填正反面 → 注入 click/fetch 记录器 →
 *       先用 **Input.dispatchMouseEvent**（Maestro 合成 tap 的等价物）点顶栏 Save
 *       → 读记录；若无 click 记录，再用 **b.click()**（真 click）点一次对照。
 *
 * 只做诊断，不改任何后端数据以外的东西；最后会把自己造的 note 删掉。
 */
import { openCdp } from './lib/adb-cdp.mjs'

const PKG = process.env.POCKET_APP_ID || 'com.kaixuan.opencode.pocket'
const API = (process.env.POCKET_API_BASE || 'http://127.0.0.1:18099').replace(/\/+$/, '')
const USER = process.env.POCKET_AUTH_USER || 'admin'
const PASS = process.env.POCKET_DEV_PASS || process.env.POCKET_AUTH_PASS || ''
const DECK = process.env.DIAG_DECK_ID || ''
const FRONT = process.env.DIAG_FRONT || '诊断正面'
const BACK = process.env.DIAG_BACK || '诊断背面'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function apiToken() {
  const res = await fetch(`${API}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: USER, password: PASS }),
  })
  const body = await res.json()
  return body.token
}

const c = await openCdp({ pkg: PKG })
let id = 0
const pending = new Map()
c.ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) {
    pending.get(m.id)(m)
    pending.delete(m.id)
  }
})
const ev = async (expression, ms = 12000) => {
  const i = ++id
  const raw = await new Promise((r) => {
    const t = setTimeout(() => { pending.delete(i); r(null) }, ms)
    pending.set(i, (y) => { clearTimeout(t); r(y) })
    c.ws.send(JSON.stringify({
      id: i, method: 'Runtime.evaluate',
      params: { expression, returnByValue: true, awaitPromise: true },
    }))
  })
  if (!raw) return '__timeout__'
  if (raw.result?.exceptionDetails) return `__error__ ${raw.result.exceptionDetails.text}`
  return raw.result?.result?.value
}

// ---- 1. 找一个卡组（没有就用 flow 建的那个）----
let deckId = DECK
if (!deckId) {
  const token = await apiToken()
  const res = await fetch(`${API}/api/flashcards`, { headers: { Authorization: `Bearer ${token}` } })
  const body = await res.json()
  deckId = body?.decks?.[0]?.deckId || ''
}
if (!deckId) {
  console.error('DIAG_NO_DECK：后端没有卡组，先跑一次 flashcards-write 或手工建一个')
  process.exit(2)
}
console.log('deckId =', deckId)

// ---- 2. 直接进建卡页 ----
await ev(`location.hash = '#/flashcards/new?deckId=${deckId}'`)
await sleep(2500)
console.log('hash    =', await ev('location.hash'))

// ---- 3. 填正反面（原生 setter + input 事件，Vue 的 v-model 才认）----
const fill = `(function(){
  var tas = Array.prototype.slice.call(document.querySelectorAll('textarea'));
  if (tas.length < 2) return 'textarea 数=' + tas.length;
  function set(el, v){
    var proto = el.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    var d = Object.getOwnPropertyDescriptor(proto, 'value');
    d.set.call(el, v);
    el.dispatchEvent(new Event('input', {bubbles:true}));
  }
  set(tas[0], ${JSON.stringify(FRONT)});
  set(tas[1], ${JSON.stringify(BACK)});
  return 'filled';
})()`
console.log('fill    =', await ev(fill))
await sleep(600)

// ---- 4. 注入记录器：DOM click + fetch/XHR ----
await ev(`(function(){
  window.__diag = { clicks: [], fetches: [], errors: [] };
  document.addEventListener('click', function(e){
    var t = e.target;
    var label = (t && (t.textContent || t.getAttribute('aria-label') || t.className) || '').toString().slice(0,60);
    window.__diag.clicks.push({ tag: t && t.tagName, label: label, trusted: e.isTrusted });
  }, true);
  var of = window.fetch;
  window.fetch = function(input, init){
    var url = typeof input === 'string' ? input : (input && input.url);
    window.__diag.fetches.push({ url: String(url).slice(0,120), method: (init && init.method) || 'GET' });
    return of.apply(this, arguments);
  };
  window.addEventListener('error', function(e){ window.__diag.errors.push(String(e.message)); });
  return 'armed';
})()`)

// ---- 5. 合成 tap 等价物：Input.dispatchMouseEvent 点顶栏 Save ----
const saveBox = await ev(`(function(){
  var bs = Array.prototype.slice.call(document.querySelectorAll('button'));
  var hit = bs.filter(function(b){ return /^(保存|Save)$/.test((b.textContent||'').trim()); });
  if (!hit.length) return null;
  var b = hit[0];  // 顶栏 save-link（表单底部那个是 type=submit）
  var r = b.getBoundingClientRect();
  return JSON.stringify({ x: r.left + r.width/2, y: r.top + r.height/2, cls: b.className, type: b.type });
})()`)
console.log('saveBtn =', saveBox)
if (!saveBox) {
  console.error('DIAG_NO_SAVE_BUTTON：建卡页上没找到「保存/Save」按钮')
  process.exit(3)
}
const box = JSON.parse(saveBox)
await c.send('Input.dispatchMouseEvent', { type: 'mousePressed', x: box.x, y: box.y, button: 'left', clickCount: 1 })
await c.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: box.x, y: box.y, button: 'left', clickCount: 1 })
await sleep(2500)
console.log('=== 合成 tap 之后 ===')
console.log('clicks   =', JSON.stringify(await ev('window.__diag.clicks')))
console.log('fetches  =', JSON.stringify(await ev('window.__diag.fetches')))
console.log('hash     =', await ev('location.hash'))
console.log('error节点=', await ev(`(function(){var p=document.querySelector('p.error');return p? p.textContent : '(无)'})()`))

// ---- 6. 若合成 tap 没产生 click，用真 click（b.click()）对照 ----
const clicksAfterTap = (await ev('window.__diag.clicks.length')) || 0
if (Number(clicksAfterTap) === 0) {
  console.log('--- 合成 tap 没产生任何 DOM click（H1 成立），改用 b.click() 对照 ---')
  await ev(`(function(){
    var bs = Array.prototype.slice.call(document.querySelectorAll('button'));
    var hit = bs.filter(function(b){ return /^(保存|Save)$/.test((b.textContent||'').trim()); });
    if (!hit.length) return 'no-btn';
    hit[0].click();
    return 'clicked:' + hit[0].className;
  })()`)
  await sleep(2500)
  console.log('clicks   =', JSON.stringify(await ev('window.__diag.clicks')))
  console.log('fetches  =', JSON.stringify(await ev('window.__diag.fetches')))
  console.log('hash     =', await ev('location.hash'))
  console.log('error节点=', await ev(`(function(){var p=document.querySelector('p.error');return p? p.textContent : '(无)'})()`))
}

c.close()
process.exit(0)
