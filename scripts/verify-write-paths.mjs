#!/usr/bin/env node
/**
 * 真机写路径验证：补 §5「未验证（下一轮必须补）」里六个模块的 UI 写路径，
 * 外加任务/会话的编辑与删除。
 *
 * 设计原则（都是上一轮真机验证吃过的亏，踩过的坑写在这里，别再犯）：
 *
 * 1. **先勘探再断言。** 控件形态（页内 inline / bottom-sheet / 长按菜单）一律先用
 *    `scripts/explore-write-targets.mjs` dump 出来再写 selector。上一轮
 *    redmi-write-ops-modules.mjs 假设「新建卡组」会弹 bottom-sheet，找不到就判 FAIL，
 *    结论其实是我的假设错了。
 *
 * 2. **点击必须派发完整指针序列。** 实测 `el.click()` 对任务卡片**无反应**
 *    （触发 pointerdown→mousedown→pointerup→mouseup→click 才正常），
 *    bottom-sheet 正常弹出。见下方 `tap()`。
 *
 * 3. **判据看网络，不看像素。** 每个写操作断言「对应 /api/ 请求的状态码」+
 *    「列表里出现了刚创建的东西」，两者都满足才算 PASS。只看到 UI 变了一帧不算。
 *
 * 4. **分不清是「没这个写入口」还是「写路径坏了」时，如实记 NO_ENTRY，
 *    不要记 PASS 也不要记 FAIL。** 模块本来就没有写 UI 是事实，不是缺陷；
 *    谎报 PASS 才是上一轮禁止外推的根源。
 *
 * 5. **被主密码锁挡住的模块记 SKIPPED(GATED)，并写出解锁前置条件**，
 *    不要因为点不动就静默不测。
 *
 * 6. **造的数据要清理。** 每个 create 都带唯一 stamp，末尾删掉。
 *
 * 用法：
 *   $env:POCKET_SERIAL='192.168.31.19:5555'
 *   $env:POCKET_MASTER='<本地库主密码>'    # 可选；不给则 vault/email 记 SKIPPED
 *   node scripts/verify-write-paths.mjs
 *   node scripts/verify-write-paths.mjs gateway marketplace   # 只跑指定模块
 */
import { execFileSync } from 'node:child_process'
import { existsSync } from 'node:fs'
import { homedir } from 'node:os'
import { join } from 'node:path'

// adb 位置不能写死：原值是某台 Windows 开发机的 platform-tools/adb.exe，
// 在别的宿主上直接 ENOENT，而且报错的措辞会把矛头指向 adb，实际是本脚本
// 绑死了一台机器。顺序：POCKET_ADB → PATH → 常见安装位置（含本机 ~/bin 包装器）。
function whichFirst(cands) {
  for (const c of cands) {
    if (!c) continue
    try { if (existsSync(c)) return c } catch { /* 继续找 */ }
  }
  return 'adb'
}
const ADB = whichFirst([
  process.env.POCKET_ADB,
  join(homedir(), 'bin', 'adb'),
  join(homedir(), 'tools', 'android-sdk', 'platform-tools', 'adb'),
  'adb',
])
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9245'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const stamp = Date.now().toString().slice(-7)

// 主密码优先从 .scratch/pocket-master.txt 读（.gitignore 已忽略，避免密钥进对话/进版本库）
let MASTER = process.env.POCKET_MASTER || ''
if (!MASTER) {
  try {
    const { readFileSync } = await import('node:fs')
    MASTER = readFileSync('.scratch/pocket-master.txt', 'utf8').trim()
  } catch { /* 没有就算了，vault/email 会记 SKIPPED */ }
}

const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

// ---------- CDP ----------
const pid = adb(['-s', SERIAL, 'shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
if (!pid) { console.log('APP_NOT_RUNNING'); process.exit(2) }
const socks = adb(['-s', SERIAL, 'shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
  .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
adb(['-s', SERIAL, 'forward', `tcp:${PORT}`, `localabstract:${socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]}`])
const page = (await (await fetch(`http://127.0.0.1:${PORT}/json/list`)).json()).find((t) => t.type === 'page')
if (!page) { console.log('NO_PAGE'); process.exit(1) }

const ws = new WebSocket(page.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`))
let msgId = 0
const pending = new Map()
const send = (method, params = {}) => new Promise((res) => { const i = ++msgId; pending.set(i, res); ws.send(JSON.stringify({ id: i, method, params })) })
const apiCalls = []
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  const p = m.params || {}
  if (m.method === 'Network.responseReceived' && p.response?.url?.includes('/api/')) {
    apiCalls.push({ method: p.response.method || p.request?.method || '?', url: p.response.url.replace(/https?:\/\/[^/]+/, ''), status: p.response.status })
  }
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
await send('Network.enable')

// 页面内网络 hook（CDP Network 域的兜底，见 lastStatus 的注释）。
// 记进 window.__apiCalls，由 drainPageCalls() 并入宿主侧的 apiCalls。
await send('Runtime.evaluate', { expression: `(() => {
  if (window.__apiCalls) return 'already'
  window.__apiCalls = []
  const rec = (method, url, status) => {
    try { if (String(url).includes('/api/')) window.__apiCalls.push({ method, url: String(url).replace(/https?:\\/\\/[^/]+/, ''), status: status ?? 0 }) } catch (e) {}
  }
  const of = window.fetch
  if (of) window.fetch = function (input, init) {
    const m = (init && init.method) || (input && input.method) || 'GET'
    const u = (typeof input === 'string' ? input : (input && input.url)) || ''
    return of.apply(this, arguments).then(r => { rec(m, u, r.status); return r }, e => { rec(m, u, 0); throw e })
  }
  const oo = XMLHttpRequest.prototype.open
  const os = XMLHttpRequest.prototype.send
  XMLHttpRequest.prototype.open = function (m, u) { this.__m = m; this.__u = u; return oo.apply(this, arguments) }
  XMLHttpRequest.prototype.send = function () {
    this.addEventListener('loadend', () => rec(this.__m, this.__u, this.status))
    return os.apply(this, arguments)
  }
  return 'hooked'
})()`, returnByValue: true })

const ev = async (expression) => {
  // awaitPromise 必须有：没有它，一个 async IIFE 的返回值是**未被 await 的
  // Promise**，returnByValue 只能把它序列化成 `{}`。
  // 2026-10-03 在 vivo V2436A 上实测：邮箱账户清理那段 async IIFE 因此
  // 什么都没报告，合成账户 `e2e-ui-7952594@example.invalid` 留在用户库里。
  // 仓库另两个 helper（lib/adb-cdp.mjs、maestro-run.mjs）一直都传着这个参数，
  // 只有这里漏了。对非 Promise 的表达式它是 no-op，不影响任何既有调用。
  const r = await send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true })
  if (r?.exceptionDetails) {
    console.error('  [EVAL ERR]', (r.exceptionDetails.exception?.description || r.exceptionDetails.text || '').split('\n')[0])
    return null
  }
  return r?.result?.value
}

// ---------- 断言底座 ----------
const results = []
/** verdict: PASS | FAIL | NO_ENTRY | SKIPPED | BLOCKED */
function record(module, action, verdict, detail) {
  results.push({ module, action, verdict, detail })
  const mark = { PASS: 'PASS ', FAIL: 'FAIL ', NO_ENTRY: 'NOENT', SKIPPED: 'SKIP ', BLOCKED: 'BLKD ' }[verdict]
  console.log(`  ${mark}  ${module} · ${action}${detail ? '  — ' + detail : ''}`)
}
async function waitFor(expression, label, timeoutMs = 12000) {
  const start = Date.now()
  while (Date.now() - start < timeoutMs) { const v = await ev(expression); if (v) return v; await sleep(600) }
  console.log(`       !! TIMEOUT: ${label}`)
  return null
}
/** 完整指针序列点击 —— el.click() 对任务卡片无反应，必须派发这五个事件。
 *  findExpr 必须已经带上 [index]，否则 el 会是数组而不是元素
 *  （第一版这里漏了 index，三个模块全部误报 FAIL，教训：点击失败要先看返回码）。 */
const tapExpr = (findExpr) => `(function(){ var el = ${findExpr}; if(!el) return 'NOT_FOUND';
    if (el.nodeType !== 1) return 'NOT_ELEMENT';
    if (el.disabled) return 'DISABLED';
    var r = el.getBoundingClientRect();
    var base = { bubbles:true, cancelable:true, composed:true, view:window,
      clientX:r.left+r.width/2, clientY:r.top+r.height/2, button:0, buttons:1, pointerId:1, isPrimary:true };
    ['pointerdown','mousedown','pointerup','mouseup','click'].forEach(function(t){
      el.dispatchEvent(new (t.indexOf('pointer')===0?PointerEvent:MouseEvent)(t, base)); });
    return 'dispatched'; })()`
const tap = (selector, { index = 0 } = {}) => tapExpr(`document.querySelectorAll(${JSON.stringify(selector)})[${index}]`)
const tapText = (text, { exact = false, index = 0 } = {}) => {
  const match = exact ? `(b.textContent||'').trim() === ${JSON.stringify(text)}` : `(b.textContent||'').indexOf(${JSON.stringify(text)}) >= 0`
  return tapExpr(`Array.from(document.querySelectorAll('button,a,[role=button]')).filter(b => ${match})[${index}]`)
}
const setInput = (selector, value, { index = 0 } = {}) => {
  const find = `document.querySelectorAll(${JSON.stringify(selector)})[${index}]`
  return `(function(){ var el = ${find}; if(!el) return 'NOT_FOUND';
    if (el.nodeType !== 1) return 'NOT_ELEMENT';
    var d = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value');
    (d && d.set ? d : Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value')).set.call(el, ${JSON.stringify(value)});
    el.dispatchEvent(new Event('input',{bubbles:true}));
    el.dispatchEvent(new Event('change',{bubbles:true}));
    return 'filled:'+el.value; })()`
}

/**
 * 填值并**断言真的填进去了**。
 *
 * 踩坑记录（务必保留这个断言）：第一版网关检查直接 `await ev(setInput(...))`
 * 然后就不管返回值了，而 `setInput` 在元素不存在时返回 'NOT_FOUND'。
 * 结果「Admin 用户名」那一栏根本没填上，保存时后端正确返回
 * 400 adminUsername is required —— 我把这个记成了「网关写路径 FAIL」。
 * 真相是：写路径完全正常，是我没检查自己那一步有没有成功。
 * 教训和上一轮「后端没有日志 ≠ 没到达」同源：**先确认自己那步做成了，再判别人的错。**
 */
async function mustFill(selector, value, { index = 0, label = '' } = {}) {
  const r = String(await ev(setInput(selector, value, { index })) || '')
  if (!r.startsWith('filled:')) {
    throw new Error(`填值失败 ${label || selector}[${index}] -> ${r}（未写入，判据无效）`)
  }
  return r
}
/** 断言点击真的派发出去了 */
async function mustTap(expr, label) {
  const r = String(await ev(expr) || '')
  if (r !== 'dispatched') throw new Error(`点击失败 ${label} -> ${r}`)
  return r
}
const closeOverlays = () => ev(`(function(){ var n=0;
  Array.from(document.querySelectorAll('.bottom-sheet-overlay,[class*="overlay"],.dialog-backdrop')).forEach(function(o){try{o.click();n++}catch(e){}});
  return n; })()`)
const bodyText = () => ev(`(document.body.innerText||'').replace(/\\s+/g,' ').trim()`)
const isGated = async () => /#\/login/.test((await ev('location.hash')) || '')

/**
 * 本地加密库是否处于锁状态。
 *
 * 为什么单独判：`/#/login` 只说明路由被守卫弹回，**不代表写控件一定不可用**；
 * 真正决定「写路径能不能验」的是本地库锁没锁。实测：任务页在锁住时
 * 「+ 新任务」能打开、能填标题，但「创建」按钮是 disabled —— 这时候
 * 记 FAIL 是冤枉产品，记 PASS 是撒谎。正确记法是 SKIPPED(GATED)。
 */
async function dbLocked() {
  const hasUnlockInput = await ev(`!!document.querySelector('input[placeholder*="主密码"]')`)
  if (hasUnlockInput) return true
  const unlockBtn = await ev(`(function(){ var b=Array.from(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim()==='解锁'}); return b? (b.disabled?'disabled':'enabled') : 'absent'; })()`)
  return unlockBtn === 'disabled'
}
/** 写控件因库锁而 disabled 时，统一记 SKIPPED(GATED) 而不是 FAIL */
async function recordGatedIfLocked(module, action, whatIsDisabled) {
  if (await dbLocked()) {
    record(module, action, 'SKIPPED', `本地加密库未解锁，${whatIsDisabled}按钮为 disabled —— 非产品缺陷`)
    return true
  }
  return false
}

async function goto(route) {
  await ev(closeOverlays())
  await ev(`location.hash = ${JSON.stringify(route)}`)
  await sleep(2800)
}
/** 复位两侧的调用记录。只清宿主侧会漏：页面 hook 里可能还攒着上一次读之后、
 *  复位之前发出的调用，下次 drain 进来就会被当成"本轮提交窗口内"的。 */
async function resetCalls() {
  apiCalls.length = 0
  try { await send('Runtime.evaluate', { expression: 'window.__apiCalls = []', returnByValue: true }) } catch { /* 页面没了 */ }
}

/** 把页面内 hook 记下的调用并入宿主侧 apiCalls（取走后清空，避免重复计数）。 */
async function drainPageCalls() {
  let got
  try {
    const r = await send('Runtime.evaluate', { expression: 'JSON.stringify(window.__apiCalls || [])', returnByValue: true })
    got = JSON.parse(r?.result?.value || '[]')
  } catch { got = [] }
  if (got.length) {
    for (const c of got) apiCalls.push(c)
    try { await send('Runtime.evaluate', { expression: 'window.__apiCalls = []', returnByValue: true }) } catch { /* 页面没了就当没有 */ }
  }
}

/**
 * 从 apiCalls 里找出最后一次匹配 method+url 的状态码。
 *
 * 两个来源一起看，缺一不可：
 *   1. CDP `Network.responseReceived`
 *   2. 页面内 hook 的 fetch / XMLHttpRequest
 *
 * ⚠️ 只靠 (1) 会**漏报**，而且漏报的方向最坏：2026-10-03 在 vivo V2436A 上，
 * 脚本点「创建」后任务**确实建出来了**（后端库里能查到 E2E-任务-6718596），
 * 但 (1) 一个 `Network.responseReceived` 都没收到，于是判成
 * `FAIL 创建任务 — POST 无请求`。
 * 「用网络判据」本来是为了不信像素，结果网络判据自己漏了，比没有判据更糟：
 * 它会把一个真实可用的写路径报成坏的。
 * WebView 上 DevTools 的 Network 域事件本来就不保证齐全，所以这里加页面内
 * hook 兜底，两边都收，缺哪边都不影响结论。
 */
async function lastStatus(method, urlRe) {
  await drainPageCalls()
  for (let i = apiCalls.length - 1; i >= 0; i--) {
    const c = apiCalls[i]
    if (c.method === method && urlRe.test(c.url)) return c.status
  }
  return null
}

// ---------- 0. preflight：解锁 ----------
console.log(`=== 真机写路径验证 ${SERIAL} (stamp=${stamp}) ===`)
await goto('#/login')
if (await isGated()) {
  const hasInput = await ev(`!!document.querySelector('input[placeholder*="主密码"]')`)
  if (hasInput && MASTER) {
    console.log('  unlock ->', await ev(setInput('input[placeholder*="主密码"]', MASTER)))
    await sleep(1200)
    console.log('  解锁按钮 ->', await ev(tapText('解锁', { exact: true })))
    await sleep(4000)
  } else if (hasInput && !MASTER) {
    console.log('  ! 需要主密码但未提供 POCKET_MASTER / .scratch/pocket-master.txt')
  }
}
console.log('  当前 hash =', await ev('location.hash'))

// ---------- 各模块检查 ----------
const GATE_NOTE = '主密码锁（未提供 POCKET_MASTER / .scratch/pocket-master.txt）'

/** 1. 网关：完整 CRUD */
async function checkGateway() {
  const M = '网关'
  await goto('#/gateway')
  if (await isGated()) return record(M, 'CRUD', 'SKIPPED', GATE_NOTE)
  const name = `E2E-GW-${stamp}`
  const renamed = `${name}-R`
  const base = `https://e2e-${stamp}.invalid.test`

  // 定位「属于某个节点的」编辑/删除按钮。编辑和删除两步都要用，所以定义在
  // 函数作用域，不是编辑那个 try 块里。
  //
  // ⚠️ 别用 `closest('…,div[class*=node],…')` 找所属卡片：2026-10-03 在
  // vivo V2436A 上实测，`GatewayNodeListView` 的结构是
  //   <div class="node-card"> … <div class="node-actions"> <button>编辑</button> …
  // `.node-actions` 自己就匹配 `div[class*=node]`，而 closest() 返回**最近**
  // 的匹配祖先，于是停在 `.node-actions` 上——它的文本只有「探测编辑删除」，
  // 永远不含节点名 ⇒ 恒 -1 ⇒ 编辑/删除两步永远 FAIL。
  // 正确做法是不猜 class：往上走到**第一个文本里含该节点名**的祖先。
  const OWNER = (btn, nameExpr) => `(function(){
    var bs=Array.from(document.querySelectorAll('button')).filter(b=>(b.textContent||'').trim()===${JSON.stringify(btn)} && b.offsetParent!==null);
    for (var i=0;i<bs.length;i++){
      for (var p=bs[i].parentElement; p; p=p.parentElement){
        if ((p.textContent||'').indexOf(${nameExpr})>=0) return i;
      }
    }
    return -1;
  })()`

  const addBtn = await ev(`!!document.querySelector('.gw-add-btn')`)
  if (!addBtn) return record(M, 'CRUD', 'NO_ENTRY', '页面上没有 .gw-add-btn（新增节点入口）')

  // ---- 新增 ----
  try {
    await mustTap(tap('.gw-add-btn'), '网关 + 新增')
    const form = await waitFor(`!!document.querySelector('input[placeholder*="prod"]') ? 'form' : null`, '新增节点表单')
    if (!form) return record(M, '新增节点', 'FAIL', '点了「+ 新增」但表单没出现')

    // 表单字段在文档里的 input 顺序（explore + diag 实证）：
    //   0=名称  1=Base URL  2=Admin 用户名  3=Admin 密码  4=启用(checkbox)
    // 不用 type=text 定位：Admin 用户名那一栏的 type 在不同渲染下不稳定，
    // 第一版按 type=text[2] 取，取到 undefined 还静默跳过，直接造成误判。
    const shape = await ev(`JSON.stringify(Array.from(document.querySelectorAll('input')).map(i=>i.type))`)
    const types = JSON.parse(shape || '[]')
    if (types.length < 5) return record(M, '新增节点', 'FAIL', `表单 input 数量异常：${shape}`)

    await mustFill('input', name, { index: 0, label: '名称' })
    await mustFill('input', base, { index: 1, label: 'Base URL' })
    await mustFill('input', 'e2e-admin', { index: 2, label: 'Admin 用户名' })
    await mustFill('input', `E2e-${stamp}`, { index: 3, label: 'Admin 密码' })
    await sleep(700)

    await resetCalls()
    await mustTap(tapText('保存', { exact: true }), '保存')
    await sleep(4000)
  } catch (err) {
    return record(M, '新增节点', 'FAIL', `验证脚本自身失败：${err.message}`)
  }

  const createStatus = await lastStatus('POST', /llm-gateway\/nodes/)
  const listed = (await bodyText() || '').includes(name)
  if (createStatus && createStatus < 300 && listed) {
    record(M, '新增节点', 'PASS', `POST ${createStatus}，列表已出现「${name}」`)
  } else {
    // 区分三种情况，别混成 FAIL：
    //  a) 库锁导致前端根本没发请求
    //  b) 后端 4xx（参数/权限问题，是真结果，要报出来）
    //  c) 库锁导致保存无效
    if (!createStatus && await dbLocked()) {
      record(M, '新增节点', 'SKIPPED', '本地加密库未解锁，保存未发出请求 —— 非产品缺陷')
      return
    }
    if (!createStatus) {
      // 没有请求也没有错误提示：原因未定（可能页面已被导航走、表单被重置、
      // 或并发会话正在驱动设备）。**不猜**，记 BLOCKED 交下一轮定位。
      record(M, '新增节点', 'BLOCKED',
        `既无 POST 也无错误提示，且锁态判据不成立。列表命中=${listed}（可能是表单残留文本而非真创建）。原因待定位`)
      return
    }
    const backendMsg = await ev(`JSON.stringify(Array.from(document.querySelectorAll('body *')).map(e=>e.childElementCount===0?(e.textContent||'').trim():'').filter(t=>/is required|无效|失败|错误|denied/.test(t)).slice(0,3))`)
    record(M, '新增节点', 'FAIL',
      `POST=${createStatus}，列表命中=${listed}，页面/后端提示=${backendMsg}`)
    return
  }

  // ---- 编辑：改名 ----
  try {
    await resetCalls()
    const editIdx = await ev(OWNER('编辑', JSON.stringify(name)))
    if (editIdx === -1) record(M, '编辑节点', 'FAIL', '列表里有该节点但定位不到对应「编辑」按钮')
    else {
      await mustTap(tapText('编辑', { exact: true, index: editIdx }), '编辑')
      await waitFor(`!!document.querySelector('input[placeholder*="prod"]') ? 'form' : null`, '编辑表单')
      await mustFill('input', renamed, { index: 0, label: '名称' })
      await sleep(600)
      await mustTap(tapText('保存', { exact: true }), '保存(编辑)')
      await sleep(4000)
      const putStatus = await lastStatus('PUT', /llm-gateway\/nodes/) ?? await lastStatus('POST', /llm-gateway\/nodes/)
      const renamedListed = (await bodyText() || '').includes(renamed)
      record(M, '编辑节点', putStatus && putStatus < 300 && renamedListed ? 'PASS' : 'FAIL',
        `${putStatus ?? '无请求'}，改名后命中=${renamedListed}`)
    }
  } catch (err) {
    record(M, '编辑节点', 'FAIL', `验证脚本自身失败：${err.message}`)
  }

  // ---- 删除 ----
  try {
    await resetCalls()
    const delIdx = await ev(OWNER('删除', JSON.stringify(renamed)))
    if (delIdx === -1) record(M, '删除节点', 'FAIL', `定位不到「${renamed}」的删除按钮`)
    else {
      await mustTap(tapText('删除', { exact: true, index: delIdx }), '删除')
      await sleep(1000)
      // 确认框的按钮文案**就是「删除」**（GatewayNodeListView.vue 的
      // `confirmText: '删除'`），不是「确认」。原实现先用一个把「删除」也算进去
      // 的正则判断"有没有确认框"——那个正则命中的其实是**列表页上刚点过的那个
      // 删除按钮**，于是紧接着去点「确认」，而页面上根本没有「确认」⇒ NOT_FOUND。
      // 正确做法：只在**弹层内部**找按钮，候选文案按 删除/确认/确定 逐个试。
      const clickedConfirm = await ev(`(function(){
        var words=['删除','确认','确定'];
        var bs=Array.from(document.querySelectorAll('button')).filter(function(x){
          return !x.disabled && x.offsetParent!==null && x.closest('[role=dialog],.confirm,.modal,.sheet,.confirm-dialog,.toast');
        });
        for (var w of words){ for (var i=0;i<bs.length;i++){ if((bs[i].textContent||'').trim()===w){ bs[i].click(); return w; } } }
        return 'none';
      })()`)
      if (clickedConfirm === 'none') {
        return record(M, '删除节点', 'FAIL', '点「删除」后弹层里没有可点的确认按钮（候选 删除/确认/确定）')
      }
      await sleep(3500)
      const delStatus = await lastStatus('DELETE', /llm-gateway\/nodes/)
      const gone = !(await bodyText() || '').includes(renamed)
      record(M, '删除节点', delStatus && delStatus < 300 && gone ? 'PASS' : 'FAIL', `DELETE ${delStatus ?? '无请求'}，已从列表消失=${gone}`)
    }
  } catch (err) {
    record(M, '删除节点', 'FAIL', `验证脚本自身失败：${err.message}`)
  }
}

/** 2. 市场：安装技能 */
async function checkMarketplace() {
  const M = '市场'
  await goto('#/marketplace/skills')
  if (await isGated()) return record(M, '安装技能', 'SKIPPED', GATE_NOTE)
  const entry = await ev(`(function(){ var b=Array.from(document.querySelectorAll('button,a,[role=button]')).find(x=>(x.textContent||'').trim()==='安装' && x.offsetParent!==null); return b?'yes':'no' })()`)
  if (entry !== 'yes') {
    // 「没有安装按钮」有两种完全不同的原因，混为一谈会误导下一个人：
    //   · 列表是空的 —— 没有对象，自然没有针对对象的操作（不是缺陷）
    //   · 列表有东西但装不了 —— 那才是真问题
    // 2026-10-03 vivo V2436A 实测是前者：技能/智能体/工作搭子三个子路由
    // 都是空态，文案分别写着「暂无技能包」「暂无智能体」「暂无工作流模板」。
    const t = await bodyText() || ''
    // 短一点，而且**要排除空白**：`[^，。]` 会把空格也算进去，于是实测出现过
    // 「暂无技能包 技能由发布者提交」这种跨句拼接。
    // ⚠️ 这是**正则字面量**，空白类要写 `\s` 而不是 `\\s` —— 后者匹配的是
    // 一个字面反斜杠加字母 s，静默失效（我第一版就是这么写错的：
    // 单测里用的是 `\s` 所以通过了，文件里却是 `\\s`，两边不一致）。
    const empty = (t.match(/暂无[^，。\s]{2,8}/) || [])[0]
    return record(M, '安装技能', 'NO_ENTRY',
      empty
        ? `空列表（页面写着「${empty}」）：没有可安装对象，所以没有「安装」按钮 —— 不是写路径缺陷`
        : `列表非空但找不到「安装」按钮，这**可能**是缺陷。文案首段：${t.slice(0, 80)}`)
  }
  await resetCalls()
  try {
    await mustTap(tapText('安装', { exact: true }), '市场 安装')
  } catch (err) {
    return record(M, '安装技能', 'FAIL', `验证脚本自身失败：${err.message}`)
  }
  await sleep(3500)
  // 安装可能先弹确认框
  const confirm = await ev(`(function(){ var b=Array.from(document.querySelectorAll('button')).find(x=>/^(确认|确定|安装)$/.test((x.textContent||'').trim()) && x.offsetParent!==null); return b?1:0 })()`)
  if (confirm) { try { await mustTap(tapText('确认', { exact: true }), '确认安装') } catch { /* 无所谓，继续观察 */ } await sleep(3500) }
  const st = await lastStatus('POST', /marketplace/)
  const body = (await bodyText() || '')
  if (st && st < 300) record(M, '安装技能', 'PASS', `POST ${st}`)
  else record(M, '安装技能', 'FAIL', `POST=${st ?? '无请求'}，页面可见提示：${body.slice(0, 120)}`)
}

/** 3. 费用配额：策略写路径 */
async function checkCost() {
  const M = '费用配额'
  await goto('#/cost')
  if (await isGated()) return record(M, '策略写入', 'SKIPPED', GATE_NOTE)
  const hasPolicy = await ev(`(function(){ var t=document.body.innerText||''; return /策略|配额|审计模式/.test(t) ? 'text' : 'none' })()`)
  if (hasPolicy !== 'text') return record(M, '策略写入', 'NO_ENTRY', '页面没有配额/策略区块')
  const editables = await ev(`JSON.stringify(Array.from(document.querySelectorAll('button,select,input')).map(e=>({t:(e.textContent||e.value||'').trim().slice(0,18),tag:e.tagName,off:!!e.disabled})).filter(e=>/策略|配额|审计|always_allow|保存|修改/.test(e.t)))`)
  if (!editables || editables === '[]') return record(M, '策略写入', 'NO_ENTRY', '只读视图：页面上没有任何可写控件')
  record(M, '策略写入', 'NO_ENTRY', `存在可写控件但语义未确认，未自动化：${editables.slice(0, 200)}`)
}

/** 4. 实例 */
async function checkInstances() {
  const M = '实例'
  await goto('#/instances')
  if (await isGated()) return record(M, '实例写路径', 'SKIPPED', GATE_NOTE)
  const entry = await ev(`(function(){ var b=Array.from(document.querySelectorAll('button,a,[role=button]')).find(x=>/新增|添加|创建/.test((x.textContent||'').trim()) && x.offsetParent!==null); return b?(b.textContent||'').trim():'no' })()`)
  if (entry === 'no') return record(M, '实例写路径', 'NO_ENTRY', '实例页只有选择器（当前服务器/功能入口），无新增/编辑/删除控件')
  record(M, '实例写路径', 'NO_ENTRY', `存在「${entry}」入口但语义未确认，未自动化`)
}

/** 5. 任务：创建 + 编辑 + 删除 */
async function checkTasks() {
  const M = '任务'
  await goto('#/tasks')
  if (await isGated()) return record(M, '任务 CRUD', 'SKIPPED', GATE_NOTE)
  const title = `E2E-任务-${stamp}`
  const add = await ev(`(function(){ var b=Array.from(document.querySelectorAll('button')).find(x=>/新任务/.test(x.textContent||'') && !x.disabled); return b?'yes':'no' })()`)
  if (add !== 'yes') return record(M, '任务 CRUD', 'NO_ENTRY', '没有可用的「+ 新任务」按钮')

  try {
    await mustTap(tapText('新任务'), '任务 + 新任务')
  } catch (err) {
    return record(M, '创建任务', 'FAIL', `验证脚本自身失败：${err.message}`)
  }
  await sleep(2200)
  // 不猜 selector：把可见输入框 dump 出来，**优先填标题那个**，其次第一个。
  //
  // ⚠️ 这里原本是 `var e = i[i.length - 1]`（最后一个），注释却写着「第一个」——
  // 注释和代码自相矛盾，而最后一个恰好是错的。创建任务弹窗的 DOM 顺序是
  //   标题(input) → 描述(textarea) → 截止日期(input[type=date])
  // 取末位 ⇒ 填进「描述」，标题永远是空的 ⇒「创建」保持 disabled ⇒
  // 报成 `[BLOCKED]「创建[disabled]」在标题已填的情况下仍禁用`。
  // 2026-10-03 在 vivo V2436A 上实测：按 placeholder 精确填标题后，
  // `disabled` 立刻 true→false，点击发出 `POST /api/tasks`。
  // 也就是说那不是产品缺陷，是本脚本自己填错了框——而一个把自己 selector
  // 写错却报成「产品 BLOCKED」的脚本，会让下一个人去查一个不存在的 bug。
  const inputInfo = await ev(`(function(){
    var i=Array.from(document.querySelectorAll('input[type=text],input:not([type]),textarea')).filter(e=>e.offsetParent!==null);
    if(!i.length) return 'none';
    var titled=i.find(e=>/标题|title/i.test(e.placeholder||'')) || i[0];
    return JSON.stringify({tag:titled.tagName, type:titled.type, ph:titled.placeholder||'', n:i.length,
      all:i.map(e=>(e.placeholder||e.tagName))});
  })()`)
  if (!inputInfo || inputInfo === 'none') {
    const btns = await ev(`JSON.stringify(Array.from(document.querySelectorAll('button')).map(b=>(b.textContent||'').trim()).filter(Boolean).slice(0,25))`)
    return record(M, '创建任务', 'FAIL', `点了「+ 新任务」但页面上没有可见输入框。当前按钮：${btns}`)
  }
  const info = JSON.parse(inputInfo)
  const sel = info.ph ? `${info.tag.toLowerCase()}[placeholder="${info.ph}"]` : info.tag.toLowerCase()
  try {
    await mustFill(sel, title, { label: '任务标题' })
  } catch (err) {
    return record(M, '创建任务', 'FAIL', `验证脚本自身失败：${err.message}`)
  }
  await sleep(900)
  await resetCalls()
  // 提交按钮文案不固定，逐个试「创建/保存/提交/确定」中第一个可点的
  const submitted = await ev(`(function(){
    var words=['创建','保存','提交','确定','发送'];
    for (var w of words){ var b=Array.from(document.querySelectorAll('button')).find(x=>(x.textContent||'').trim()===w && !x.disabled && x.offsetParent!==null); if(b) return w; }
    return 'none'; })()`)
  if (submitted === 'none') {
    // 关键区分：提交按钮存在但 disabled —— 原因可能不止一种
    //   (a) 本地加密库未解锁（本轮实测过：标题能填、创建 disabled）
    //   (b) 表单还有别的必填项没满足
    //   (c) 产品侧逻辑禁用
    // 分不清是哪种就记 BLOCKED，**不要**记 FAIL（那等于替产品认领一个我没定位的缺陷），
    // 也**不要**记 PASS。
    const disabledSubmit = await ev(`(function(){
      var words=['创建','保存','提交','确定','发送'];
      for (var w of words){ var b=Array.from(document.querySelectorAll('button')).find(x=>(x.textContent||'').trim()===w); if(b) return (b.textContent||'').trim()+(b.disabled?'[disabled]':'[enabled]'); }
      return 'absent'; })()`)
    if (String(disabledSubmit).includes('[disabled]')) {
      if (await dbLocked()) {
        return record(M, '创建任务', 'SKIPPED', `本地加密库未解锁，「${disabledSubmit}」 —— 非产品缺陷`)
      }
      return record(M, '创建任务', 'BLOCKED',
        `「${disabledSubmit}」在标题已填的情况下仍禁用；当前页面无主密码输入框，锁态判据不成立，原因待人工定位（可能：还有必填项 / 产品侧逻辑禁用）`)
    }
    const btns = await ev(`JSON.stringify(Array.from(document.querySelectorAll('button')).map(b=>(b.textContent||'').trim()+(b.disabled?'[off]':'')).filter(Boolean).slice(0,25))`)
    return record(M, '创建任务', 'FAIL', `填了标题但找不到提交按钮（候选=${disabledSubmit}）。当前按钮：${btns}`)
  }
  try { await mustTap(tapText(submitted, { exact: true }), `任务提交(${submitted})`) } catch (err) {
    return record(M, '创建任务', 'FAIL', `验证脚本自身失败：${err.message}`)
  }
  await sleep(4500)
  const st = await lastStatus('POST', /\/api\/tasks/)
  const listed = (await bodyText() || '').includes(title)
  record(M, '创建任务', st && st < 300 && listed ? 'PASS' : 'FAIL', `POST ${st ?? '无请求'}，列表命中=${listed}（提交按钮=「${submitted}」）`)

  // 编辑/删除：上一轮记录是「长按任务卡片操作」
  if (listed) {
    const down = await ev(`(function(){
      var els=Array.from(document.querySelectorAll('li,div[class*=task],div[class*=card]')).filter(e=>e.textContent.indexOf(${JSON.stringify(title)})>=0);
      var el=els[els.length-1]; if(!el) return 'NOT_FOUND';
      var r=el.getBoundingClientRect();
      var base={bubbles:true,cancelable:true,composed:true,view:window,clientX:r.left+40,clientY:r.top+r.height/2,button:0,buttons:1,pointerId:1,isPrimary:true};
      el.dispatchEvent(new PointerEvent('pointerdown',base)); return 'down'; })()`)
    await sleep(1300)
    await ev(`(function(){
      var els=Array.from(document.querySelectorAll('li,div[class*=task],div[class*=card]')).filter(e=>e.textContent.indexOf(${JSON.stringify(title)})>=0);
      var el=els[els.length-1]; if(!el) return 'x';
      var r=el.getBoundingClientRect();
      var base={bubbles:true,cancelable:true,composed:true,view:window,clientX:r.left+40,clientY:r.top+r.height/2,button:0,buttons:1,pointerId:1,isPrimary:true};
      el.dispatchEvent(new PointerEvent('pointerup',base)); return 'up'; })()`)
    await sleep(1300)
    const menu = await ev(`JSON.stringify(Array.from(document.querySelectorAll('button,a,[role=menuitem]')).map(b=>(b.textContent||'').trim()).filter(t=>/编辑|删除|重命名|归档/.test(t)))`)
    if (down !== 'NOT_FOUND' && menu && menu !== '[]') {
      record(M, '长按菜单', 'PASS', `长按弹出：${menu}`)
      await resetCalls()
      // 删除入口有**两处**，实测（2026-10-03 vivo V2436A）：
      //   · 长按卡片 → 上下文菜单里的「🗑 删除」（TasksView.vue:351）
      //   · 任务详情页顶部的 action-btn.delete（TaskDetailView.vue:202）
      // 原实现只认第一处，而且长按时很容易长按到**会话**卡片上
      // （会话菜单只有「归档」，于是脚本看到 ["归档0"] 以为自己在任务菜单里，
      //  再去找「删除」自然 NOT_FOUND）。
      // 先在当前菜单里找删除；找不到就退回详情页那条路——两条都走不通才记 FAIL。
      let deleted = false
      if (await ev(`(function(){ var b=Array.from(document.querySelectorAll('button')).find(x=>/删除/.test(x.textContent||'') && x.offsetParent!==null); if(!b) return 0; b.click(); return 1 })()`)) {
        await sleep(1200)
        deleted = true
      } else {
        // 退��：点开任务详情，从详情页的删除按钮走。
        await ev(`(function(){
          var t=${JSON.stringify(title)};
          var n=Array.from(document.querySelectorAll('*')).find(e=>e.children.length===0 && (e.textContent||'').trim()===t);
          if(!n) return;
          var card=n.closest('[class*=card],[class*=task]')||n;
          card.click();
        })()`)
        await sleep(2000)
        if (await ev(`(function(){ var b=Array.from(document.querySelectorAll('button')).find(x=>/🗑|删除/.test(x.textContent||'') && x.offsetParent!==null); if(!b) return 0; b.click(); return 1 })()`)) {
          await sleep(1200)
          deleted = true
        }
      }
      if (!deleted) {
        return record(M, '删除任务', 'FAIL', '长按菜单与详情页都没找到删除入口')
      }
      // 确认框的按钮文案就是「删除」，要挑在弹层里的那个，
      // 否则会点到详情页那个刚按过的删除按钮上。
      await ev(`(function(){
        var b=Array.from(document.querySelectorAll('button')).find(x=>(x.textContent||'').trim()==='删除' && !x.disabled && x.closest('[role=dialog],.confirm,.modal,.sheet,.confirm-dialog'));
        if(b) b.click();
      })()`)
      await sleep(3500)
      const d = await lastStatus('DELETE', /\/api\/tasks/)
      const gone = !(await bodyText() || '').includes(title)
      record(M, '删除任务', d && d < 300 && gone ? 'PASS' : 'FAIL', `DELETE ${d ?? '无请求'}，已消失=${gone}`)
    } else {
      record(M, '长按菜单', 'NO_ENTRY', `长按(${down})未弹出编辑/删除/归档菜单`)
    }
  }
}

/** 6. 会话：编辑/删除 */
async function checkSessions() {
  const M = '会话'
  await goto('#/sessions')
  if (await isGated()) return record(M, '会话编辑/删除', 'SKIPPED', GATE_NOTE)
  const txt = await bodyText() || ''
  // ⚠️ 先看列表是不是空的，**再**找操作控件。
  // 原来直接找 /删除|归档|重命名|编辑/，结果命中的是筛选 tab「归档0」——
  // 那是"按归档状态过滤"的按钮，不是对某条会话的归档操作。
  // 拿筛选控件当写入口，会把"这里没有写路径"误报成"有但语义未确认"，
  // 方向正好反了。2026-10-03 vivo V2436A 实测会话列表为空态。
  const empty = (txt.match(/暂无会话|没有会话|会话 0/) || [])[0]
  if (empty) {
    return record(M, '会话编辑/删除', 'NO_ENTRY',
      `空列表（页面写着「${empty}」）：没有会话对象，所以没有编辑/删除入口 —— 不是写路径缺陷。会话需在 AI 页创建后才有`)
  }
  // 排除筛选 tab：只看不是「活跃/归档/全部」这类纯过滤标签的按钮
  const entry = await ev(`(function(){
    var b=[].slice.call(document.querySelectorAll('button,a,[role=button]')).filter(function(x){
      var t=(x.textContent||'').trim();
      return /删除|重命名|编辑/.test(t) && !/^\\s*(活跃|归档|全部|未分类)\\s*\\d*\\s*$/.test(t) && x.offsetParent!==null;
    })[0];
    return b?(b.textContent||'').trim():'no';
  })()`)
  if (entry === 'no') {
    return record(M, '会话编辑/删除', 'NO_ENTRY',
      `列表非空但找不到**针对某条会话**的编辑/删除控件（已排除「活跃/归档」这类筛选 tab）。这**可能**是缺陷。文案首段：${txt.slice(0, 80)}`)
  }
  record(M, '会话编辑/删除', 'NO_ENTRY', `存在「${entry}」控件但语义未确认，未自动化`)
}

/** 7. 密码箱 / 8. 邮箱：被主密码锁 */
/**
 * 9. 邮箱：账户新增写路径。
 *
 * 这一条原来是 `checkGated('邮箱', '#/email', '账号/规则写路径')` 的桩，
 * 进去 `goto('#/email')` 看一眼就报 NO_ENTRY。可 `#/email` 是**邮件列表页**，
 * 本来就没有写入口 —— 账户管理在 `#/email/accounts`，新增在
 * `#/email/accounts/new`。桩把"我没找"说成了"没有"，覆盖就这么丢了。
 *
 * 2026-10-03 在 vivo V2436A 上按真实 UI 跑通（E2E-UI-<stamp> 合成账户）：
 *   选「其他 IMAP」→ 填 7 个字段 → 点「保存并测试收发」
 *   ⇒ POST /api/email/accounts 201，随后自动做真实连通性测试，
 *     假主机返回 400，页面如实报「已保存…但连接未全部通过」。
 * 合成账户用完即删，绝不碰用户真实账户。
 */
async function checkEmailAccounts() {
  const M = '邮箱'
  await goto('#/email/accounts/new')
  if (await isGated()) return record(M, '账号新增', 'SKIPPED', GATE_NOTE)

  const addr = `e2e-ui-${stamp}@example.invalid`
  // 选服务商：必须派发完整指针序列，`el.click()` 在这个列表上不生效
  // （脚本自己的第 2 条原则，见文件头）。
  try {
    await mustTap(tapText('其他 IMAP'), '选「其他 IMAP」')
  } catch (err) {
    return record(M, '账号新增', 'FAIL', `验证脚本自身失败：${err.message}`)
  }
  await sleep(2500)

  // 按 label 文案找控件。⚠️ 归一化必须**两边都**去空白：
  // 只把 label 文本的空白删掉、key 还带着空格的话，「IMAP 密码」会变成
  // 「IMAP密码」再去找「IMAP 密码」，indexOf 恒 -1，于是所有**带空格**的
  // 字段全 MISS、而「邮箱地址」「显示名」这种不带空格的却碰巧命中——
  // 这种"一半对"的失败最难看出来。
  const filled = await ev(`(function(){
    var norm=function(s){ return (s||'').replace(/\\s+/g,'') };
    var labels=[].slice.call(document.querySelectorAll('label'));
    function byLabel(t){ var k=norm(t); for (var i=0;i<labels.length;i++){ if(norm(labels[i].textContent).indexOf(k)===0) return labels[i].control; } return null; }
    function setv(el,v){ if(!el) return 'MISS'; var p=el.tagName==='TEXTAREA'?window.HTMLTextAreaElement.prototype:window.HTMLInputElement.prototype;
      Object.getOwnPropertyDescriptor(p,'value').set.call(el,v);
      el.dispatchEvent(new Event('input',{bubbles:true})); el.dispatchEvent(new Event('change',{bubbles:true})); return 'ok'; }
    var vals={ '邮箱地址': ${JSON.stringify(addr)}, '显示名': ${JSON.stringify(`E2E-UI-${stamp}`)},
               'IMAP 密码': 'e2e-ui-pw', 'IMAP 主机': 'imap.example.invalid', 'IMAP 端口': '993',
               'SMTP 主机': 'smtp.example.invalid', 'SMTP 端口': '465' };
    var log=[]; for (var k in vals) log.push(k+'='+setv(byLabel(k), vals[k]));
    return log.join(' ');
  })()`)
  if (String(filled).includes('MISS')) {
    return record(M, '账号新增', 'FAIL', `表单字段没填全：${filled}`)
  }

  await resetCalls()
  try {
    await mustTap(tapText('保存并测试收发'), '保存并测试收发')
  } catch (err) {
    return record(M, '账号新增', 'FAIL', `验证脚本自身失败：${err.message}`)
  }
  // 提交后 App 还会去连一次真实的 IMAP/SMTP（合成主机必然失败），
  // 所以这里等的是"POST 账户"那个请求，不是整条流程结束。
  let st = null
  for (let i = 0; i < 14; i++) {
    await sleep(1200)
    st = await lastStatus('POST', /\/api\/email\/accounts$/)
    if (st) break
  }
  const listed = (await bodyText() || '').includes(addr)
  record(M, '账号新增', st && st < 300 && listed ? 'PASS' : 'FAIL',
    `POST=${st ?? '无请求'}，结果页命中=${listed}`)

  // 清理：合成账户必须删掉，绝不能留在用户库里。
  //
  // ⚠️ 这里必须**回读确认**，不能只看 delete 的返回码就当清理成功：
  // 第一版只在页内发了个 DELETE 就 `console.log`，那次那个账户其实**没删掉**，
  // 留下一条 e2e-ui-7857623@example.invalid 在用户库里没人发现。
  // 合成数据留在真实库里比不测更糟——它会混进同步结果和邮件列表。
  // 所以：发 DELETE → 重新拉列表 → 确认真的不在了，两种结论都如实打印。
  if (st) {
    const res = await ev(`(async function(){
      try {
        // base 必须取 App 自己的 pocket_api_base，不能写相对路径。
        // 页面 origin 是 https://localhost，API 在 http://127.0.0.1:18099；
        // 相对路径 '/api/...' 打到 https://localhost 上会被 WebView 当静态资源，
        // 返回 index.html ⇒ json() 报 "Unexpected token '<'"。
        // 这个错看着像网络问题，其实是打错了地址。
        var base = (localStorage.getItem('pocket_api_base') || '').replace(/\\/+$/, '');
        var tok = localStorage.getItem('pocket_token') || '';
        var H = { 'content-type': 'application/json', 'authorization': 'Bearer ' + tok };
        var d0 = await (await fetch(base + '/api/email/accounts', { headers: H })).json();
        var hit = (d0.accounts||[]).filter(function(a){ return a.emailAddress === ${JSON.stringify(addr)} })[0];
        if (!hit) return 'already-absent';
        var x = await fetch(base + '/api/email/accounts/' + hit.id, { method: 'DELETE', headers: H });
        var d1 = await (await fetch(base + '/api/email/accounts', { headers: H })).json();
        var still = (d1.accounts||[]).filter(function(a){ return a.emailAddress === ${JSON.stringify(addr)} }).length;
        return 'delete-status=' + x.status + ' still-present=' + still;
      } catch (e) { return 'cleanup-error: ' + (e && e.message); }
    })()`)
    const txt = (res && typeof res === 'object') ? (res.value ?? res.description ?? JSON.stringify(res)) : String(res)
    const okClean = /still-present=0/.test(txt) || /already-absent/.test(txt)
    console.log(`  ${okClean ? '已清理' : '⚠ 清理未确认'} 合成账户 ${addr} -> ${txt}`)
  }
}

/**
 * 被主密码锁，或该模块在当前平台/数据下**确定**没有写入口。
 *
 * ⚠️ 原来这个函数只会说「已解锁但未找到写入口（需人工确认 UI 形态）」，
 * 而事实往往不是"没找"。2026-10-03 在 vivo V2436A 上逐个查过，NO_ENTRY
 * 里的绝大多数是**真实的产品/平台事实**，把它们和"我没找"分开很重要：
 * 说成"没找"，下一个人会以为还有没查的；说成"平台不支持/只读/空列表"，
 * 才是可以直接采信的结论。
 *
 * `reason` 由调用方按**页面实际文案**给出，不许写"需人工确认"这种占位话。
 */
async function checkNoEntry(M, route, action, probe) {
  await goto(route)
  if (await isGated()) return record(M, action, 'SKIPPED', GATE_NOTE)
  const why = await probe()
  return record(M, action, 'NO_ENTRY', why)
}

// ---------- 跑 ----------
//
// NO_ENTRY 的理由全部来自 2026-10-03 vivo V2436A 上的**页面实际文案**，
// 不是"没找到"。分三类，含义完全不同：
//   · 平台不支持  —— 该功能在这台设备上根本没接（密码箱：Android 无原生插件）
//   · 空列表      —— 列表是空的，没有对象自然没有针对对象的操作（市场三个子页）
//   · 只读视图    —— 这个页面设计上就没有写控件（成本配额、实例）
const EMPTY_OR_UNSUPPORTED = (re) => async () => {
  const t = await bodyText() || ''
  if (re.test(t)) return `页面明确说明：${(t.match(re) || [])[0]}`
  return `页面文案里没找到预期说明，当前首段：${t.slice(0, 80)}`
}
const READ_ONLY = (hints) => async () => {
  const t = await bodyText() || ''
  const btns = await ev(`JSON.stringify([].slice.call(document.querySelectorAll('button')).filter(function(b){return b.offsetParent!==null}).map(function(b){return (b.textContent||'').trim()||('['+(b.getAttribute('aria-label')||'')+']')}).slice(0,10))`)
  return `只读视图：页面只有 ${btns}，没有任何新增/编辑/删除控件。文案首段：${t.slice(0, 70)}`
}

const CHECKS = {
  gateway: checkGateway,
  marketplace: checkMarketplace,
  cost: () => checkNoEntry('费用配额', '#/cost', '策略写入', READ_ONLY()),
  instances: () => checkNoEntry('实例', '#/instances', '实例写路径', READ_ONLY()),
  tasks: checkTasks,
  sessions: checkSessions,
  vault: () => checkNoEntry('密码箱', '#/vault', '条目 CRUD', EMPTY_OR_UNSUPPORTED(/当前平台未提供密码箱原生插件[^。]*。/)),
  email: checkEmailAccounts,
}
const only = process.argv.slice(2)
const toRun = only.length ? only.filter((k) => CHECKS[k]) : Object.keys(CHECKS)
for (const key of toRun) {
  console.log(`\n--- ${key} ---`)
  try { await CHECKS[key]() } catch (err) { record(key, '执行异常', 'FAIL', err.message) }
}

// ---------- 汇总 ----------
console.log('\n=== SUMMARY ===')
const by = (v) => results.filter((r) => r.verdict === v)
console.log(`PASS=${by('PASS').length}  FAIL=${by('FAIL').length}  BLOCKED=${by('BLOCKED').length}  NO_ENTRY=${by('NO_ENTRY').length}  SKIPPED=${by('SKIPPED').length}`)
console.table(results.map((r) => ({ 模块: r.module, 操作: r.action, 判定: r.verdict, 说明: (r.detail || '').slice(0, 90) })))
console.log('\n只有 PASS 算「已验证」。以下全部不算，下一轮需人工接手：')
for (const r of results.filter((x) => x.verdict !== 'PASS')) {
  console.log(`  - [${r.verdict}] ${r.module} · ${r.action}：${r.detail}`)
}
if (by('PASS').length === 0) {
  console.log('\n⚠ 本轮 0 项通过。不要把这一轮的任何结论写成「已验证」。')
}
ws.close()
process.exit(0)
