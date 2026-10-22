#!/usr/bin/env node
/**
 * diag-statusbar-tap.mjs —— 分清「合成 tap 根本没进页面」与「tap 落进了系统状态栏那条带子」。
 *
 * 缺陷现场（2026-10-22 实测，emulator-5556 / AOSP Android 14，en-US）：
 *   .maestro/flashcards-write.yaml 第 5 步点顶栏「保存|Save」
 *     → Maestro 报 COMPLETED，点击落点 (989,120)（= 按钮 a11y 边界 [931,81][1047,160] 的中心）
 *     → 页面**仍停在建卡页**，前端连本地队列都没写
 *       （设备 localStorage `flashcards:v1` 的 notes=[]、`flashcards:v1:outbox`=null，
 *         lastSyncedAt 停在建卡组那一刻 1792636432）
 *     → 后端 PG `flashcard_notes`=0 行
 *
 * 互斥假设：
 *   H1a 触摸事件根本没进 WebView
 *   H1b 触摸进了 WebView，但落点在**系统状态栏**覆盖的那条带子里
 *        ⇒ Android 把状态栏带子里的触摸吃给系统，WebView 收不到
 *   H2  click 产生了，但 save() 抛错 / 静默返回（save() 的 catch 会写 <p class="error">，现场没有）
 *   H3  click 产生了且保存成功（与现场不符）
 *
 * ★ 坐标事实（都是实测，不推断）：
 *   · 状态栏在**设备像素**里占 [0, insetPx)；insetPx = body 的 padding-top × dpr
 *     （本仓 App 用 `padding-top: env(safe-area-inset-top)` 给状态栏让位，WebView 本身
 *      是 edge-to-edge 的，文档原点就在屏幕 (0,0)）
 *   · a11y 边界 = 文档坐标 × dpr（实测：登录页 '登录' 按钮 CDP 文档 y=611 CSS，
 *     uiautomator 报 [1603,1729] ÷ 2.625 = 610.7 CSS —— 零偏移）
 *   · 所以「按钮 a11y 中心落在状态栏带子里」⇔「这个 tap 打不到按钮」
 *
 * 做法（本脚本按顺序做四件事，每一步都留下可对照的读数）：
 *   1. 进建卡页、填正反面（CDP 原生 setter，**不开输入法**）
 *   2. 用**真实触摸**（adb input tap）点背面 textarea ⇒ 输入法弹出，
 *      WebView 把焦点元素滚进可视区 ⇒ 页面整体上移（视觉视口偏移）
 *   3. 读数：状态栏带子 / Save 按钮 a11y 边界 / visualViewport.offsetTop / IME 状态
 *   4. 试三种「把输入法关掉」的手段，看哪一种能让 Save 回到状态栏之下：
 *        a) adb input keyevent 4    （BACK —— Maestro 的 hideKeyboard 就是它）
 *        b) adb input keyevent 111  （ESCAPE）
 *        c) 真实触摸模板 tab（让 textarea 失焦，输入法自动收起）
 *      每种之后都读：hash 是否被导航回退 / IME 是否收起 / Save 按钮新边界。
 *
 * 只做诊断；最后把自己造的 note 删掉（若真的建出来了）。
 */
import { openCdp, adb } from './lib/adb-cdp.mjs'

const PKG = process.env.POCKET_APP_ID || 'com.kaixuan.opencode.pocket'
const API = (process.env.POCKET_API_BASE || 'http://127.0.0.1:18099').replace(/\/+$/, '')
const USER = process.env.POCKET_AUTH_USER || 'admin'
const PASS = process.env.POCKET_DEV_PASS || process.env.POCKET_AUTH_PASS || ''
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
const ev = async (expression, ms = 15000) => {
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

// ---- 0. 登录（App 侧）-----------------------------------------------------
let hash = String(await ev('location.hash'))
if (hash.startsWith('#/login')) {
  const filled = await ev(`(function(){
    var ins = Array.prototype.slice.call(document.querySelectorAll('input'));
    function byPh(p){ return ins.filter(function(e){ return (e.placeholder||'')===p; })[0]; }
    var u = byPh('输入用户名'), pw = byPh('输入密码');
    if (!u || !pw) return 'no-form:' + ins.map(function(e){return e.placeholder||e.type;}).join('/');
    var set = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype,'value').set;
    function put(el,v){ set.call(el,v); el.dispatchEvent(new Event('input',{bubbles:true})); }
    put(u, ${JSON.stringify(USER)}); put(pw, ${JSON.stringify(PASS)});
    return 'ok';
  })()`)
  if (!String(filled).startsWith('ok')) {
    console.error('DIAG_LOGIN_FORM_FAIL:', filled)
    process.exit(2)
  }
  await ev(`(function(){
    var els = Array.prototype.slice.call(document.querySelectorAll('button,[role=button],input[type=submit]'));
    var hit = els.filter(function(e){ return (e.innerText||e.value||'').trim()==='登录'; })[0];
    if (!hit) return 'no-btn';
    hit.click(); return 'ok';
  })()`)
  for (let i = 0; i < 20; i++) {
    await sleep(1000)
    hash = String(await ev('location.hash'))
    if (!hash.startsWith('#/login')) break
  }
  console.log('login →', hash)
}
const bodyText = String(await ev('document.body.innerText.slice(0,400)'))
if (bodyText.includes('解锁本地数据')) {
  console.error('DIAG_LOCAL_DB_LOCKED：App 停在解锁屏，先用 maestro-run.mjs 跑一条 flow 把本地库解锁。')
  process.exit(3)
}

// ---- 1. 找一个卡组 ---------------------------------------------------------
const token = await apiToken()
const res = await fetch(`${API}/api/flashcards`, { headers: { Authorization: `Bearer ${token}` } })
const body = await res.json()
const deckId = body?.decks?.[0]?.deckId || ''
if (!deckId) {
  console.error('DIAG_NO_DECK：后端没有卡组')
  process.exit(2)
}
console.log('deckId =', deckId)

// ---- 2. 进建卡页并填正反面（CDP 填值，**不碰输入法**）----------------------
await ev(`location.hash = '#/flashcards/new?deckId=${deckId}'`)
await sleep(2500)
console.log('hash    =', await ev('location.hash'))
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
await sleep(500)

// ---- 3. 装记录器 + 读坐标 -------------------------------------------------
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

/** 读一次「现场读数」：状态栏带子 / Save 按钮位置 / 视觉视口偏移 / IME 状态 */
const readState = async (tag) => {
  const geo = JSON.parse(await ev(`(function(){
    var dpr = window.devicePixelRatio || 1;
    var padTop = parseFloat(getComputedStyle(document.body).paddingTop) || 0;
    var bs = Array.prototype.slice.call(document.querySelectorAll('button'));
    var hit = bs.filter(function(b){ return /^(保存|Save)$/.test((b.textContent||'').trim()); });
    var out = { dpr: dpr, statusBarPx: Math.round(padTop*dpr), bodyPadTop: padTop, scrollY: window.scrollY,
                vv: { h: visualViewport.height, offsetTop: visualViewport.offsetTop, pageTop: visualViewport.pageTop },
                hash: location.hash, btns: [] };
    hit.forEach(function(b){
      var r = b.getBoundingClientRect();
      out.btns.push({ cls: b.className, type: b.type,
        devTop: Math.round(r.top*dpr), devBottom: Math.round((r.top+r.height)*dpr),
        devLeft: Math.round(r.left*dpr), devRight: Math.round((r.left+r.width)*dpr),
        center: { x: Math.round((r.left+r.width/2)*dpr), y: Math.round((r.top+r.height/2)*dpr) } });
    });
    var tas = Array.prototype.slice.call(document.querySelectorAll('textarea'));
    out.textareas = tas.map(function(t){ var r=t.getBoundingClientRect();
      return { devTop: Math.round(r.top*dpr), devBottom: Math.round((r.top+r.height)*dpr), cx: Math.round((r.left+r.width/2)*dpr), cy: Math.round((r.top+r.height/2)*dpr) }; });
    return JSON.stringify(out);
  })()`))
  let ime = 'unknown'
  try {
    const dump = adb(['shell', 'dumpsys', 'input_method'], 20000)
    const m = dump.match(/mInputShown=([a-z]+)/i)
    ime = m ? m[1] : (dump.includes('mInputShown=true') ? 'true' : 'n/a')
  } catch { ime = 'dumpsys-failed' }
  console.log(`--- 读数【${tag}】---`)
  console.log('  状态栏带子 = 设备 y [0,' + geo.statusBarPx + ')')
  console.log('  Save 按钮 =', JSON.stringify(geo.btns))
  console.log('  textarea  =', JSON.stringify(geo.textareas))
  console.log('  visualViewport =', JSON.stringify(geo.vv), ' scrollY =', geo.scrollY)
  console.log('  hash =', geo.hash, ' IME mInputShown =', ime)
  return { geo, ime }
}

const before = await readState('未开输入法（CDP 填值）')

// ---- 4. 真实触摸背面 textarea ⇒ 输入法弹出、页面被顶上去 -----------------
const backTa = before.geo.textareas[1]
if (!backTa) { console.error('DIAG_NO_BACK_TEXTAREA'); process.exit(3) }
console.log(`\n>>> 真实触摸背面 textarea (${backTa.cx},${backTa.cy}) 以弹出输入法`)
adb(['shell', 'input', 'tap', String(backTa.cx), String(backTa.cy)], 20000)
await sleep(3000)
const afterIme = await readState('输入法弹出后')

// ---- 5. 三种「关输入法」的手段，逐个看 Save 是否回到状态栏之下 ----------
const tryDismiss = async (label, fn) => {
  console.log(`\n>>> 试【${label}】`)
  try { fn() } catch (e) { console.log('   执行失败:', e.message) }
  await sleep(2500)
  const st = await readState(label)
  const save = st.geo.btns[0]
  const back = st.geo.btns.find((b) => b.type === 'submit')
  console.log(`   ⇒ IME=${st.ime} hash=${st.geo.hash}`)
  console.log(`   ⇒ 顶栏 Save 中心 ${save ? save.center.y : 'n/a'}（状态栏下沿 ${st.geo.statusBarPx}）→ ${save && save.center.y >= st.geo.statusBarPx ? '★可点★' : '仍被状态栏压住'}`)
  console.log(`   ⇒ 底部 submit 按钮 = ${back ? JSON.stringify(back) : 'n/a'}`)
  return st
}

const stBack = await tryDismiss('BACK（keyevent 4，Maestro hideKeyboard 就是它）', () => {
  adb(['shell', 'input', 'keyevent', '4'], 20000)
})

if (stBack.geo.hash.includes('#/flashcards/new')) {
  // 还在建卡页 ⇒ BACK 没有导航回退，接着试 ESCAPE
  await tryDismiss('ESCAPE（keyevent 111）', () => {
    adb(['shell', 'input', 'keyevent', '111'], 20000)
  })
}

// ---- 6. 收尾：真的点一次 Save，看 click / fetch 是否发生 -------------------
const st = JSON.parse(await ev(`(function(){
  var dpr = window.devicePixelRatio || 1;
  var bs = Array.prototype.slice.call(document.querySelectorAll('button'));
  var hit = bs.filter(function(b){ return /^(保存|Save)$/.test((b.textContent||'').trim()); });
  var b = hit[0]; var r = b.getBoundingClientRect();
  return JSON.stringify({ x: Math.round((r.left+r.width/2)*dpr), y: Math.round((r.top+r.height/2)*dpr) });
})()`))
await ev('window.__diag.clicks.length = 0; window.__diag.fetches.length = 0')
console.log(`\n>>> 点 Save 中心 (${st.x},${st.y})`)
adb(['shell', 'input', 'tap', String(st.x), String(st.y)], 20000)
await sleep(3000)
console.log('  clicks =', JSON.stringify(await ev('window.__diag.clicks')))
console.log('  fetches=', JSON.stringify(await ev('window.__diag.fetches')))
console.log('  hash   =', await ev('location.hash'))
console.log('  error  =', await ev(`(function(){var p=document.querySelector('p.error');return p?p.textContent:'(无)'})()`))

c.close()
process.exit(0)
