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

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
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

const ev = async (expression) => {
  const r = await send('Runtime.evaluate', { expression, returnByValue: true })
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
/** 从 apiCalls 里找出最后一次匹配 method+url 的状态码 */
function lastStatus(method, urlRe) {
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

    apiCalls.length = 0
    await mustTap(tapText('保存', { exact: true }), '保存')
    await sleep(4000)
  } catch (err) {
    return record(M, '新增节点', 'FAIL', `验证脚本自身失败：${err.message}`)
  }

  const createStatus = lastStatus('POST', /llm-gateway\/nodes/)
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
    apiCalls.length = 0
    const editIdx = await ev(`(function(){ var bs=Array.from(document.querySelectorAll('button')).filter(b=>(b.textContent||'').trim()==='编辑');
      for (var i=0;i<bs.length;i++){ var card=bs[i].closest('li,div[class*=card],div[class*=node],[class*=gw-item]'); if(card && card.textContent.indexOf(${JSON.stringify(name)})>=0) return i; } return -1; })()`)
    if (editIdx === -1) record(M, '编辑节点', 'FAIL', '列表里有该节点但定位不到对应「编辑」按钮')
    else {
      await mustTap(tapText('编辑', { exact: true, index: editIdx }), '编辑')
      await waitFor(`!!document.querySelector('input[placeholder*="prod"]') ? 'form' : null`, '编辑表单')
      await mustFill('input', renamed, { index: 0, label: '名称' })
      await sleep(600)
      await mustTap(tapText('保存', { exact: true }), '保存(编辑)')
      await sleep(4000)
      const putStatus = lastStatus('PUT', /llm-gateway\/nodes/) ?? lastStatus('POST', /llm-gateway\/nodes/)
      const renamedListed = (await bodyText() || '').includes(renamed)
      record(M, '编辑节点', putStatus && putStatus < 300 && renamedListed ? 'PASS' : 'FAIL',
        `${putStatus ?? '无请求'}，改名后命中=${renamedListed}`)
    }
  } catch (err) {
    record(M, '编辑节点', 'FAIL', `验证脚本自身失败：${err.message}`)
  }

  // ---- 删除 ----
  try {
    apiCalls.length = 0
    const delIdx = await ev(`(function(){ var bs=Array.from(document.querySelectorAll('button')).filter(b=>(b.textContent||'').trim()==='删除');
      for (var i=0;i<bs.length;i++){ var card=bs[i].closest('li,div[class*=card],div[class*=node],[class*=gw-item]'); if(card && card.textContent.indexOf(${JSON.stringify(renamed)})>=0) return i; } return -1; })()`)
    if (delIdx === -1) record(M, '删除节点', 'FAIL', `定位不到「${renamed}」的删除按钮`)
    else {
      await mustTap(tapText('删除', { exact: true, index: delIdx }), '删除')
      await sleep(1000)
      const hasConfirm = await ev(`(function(){ var b=Array.from(document.querySelectorAll('button')).find(x=>/^(确认|确定|删除)$/.test((x.textContent||'').trim()) && x.offsetParent!==null); return b?1:0 })()`)
      if (hasConfirm) await mustTap(tapText('确认', { exact: true }), '确认删除')
      await sleep(3500)
      const delStatus = lastStatus('DELETE', /llm-gateway\/nodes/)
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
  if (entry !== 'yes') return record(M, '安装技能', 'NO_ENTRY', '列表页没有可见的「安装」按钮（只有 查看版本）')
  apiCalls.length = 0
  try {
    await mustTap(tapText('安装', { exact: true }), '市场 安装')
  } catch (err) {
    return record(M, '安装技能', 'FAIL', `验证脚本自身失败：${err.message}`)
  }
  await sleep(3500)
  // 安装可能先弹确认框
  const confirm = await ev(`(function(){ var b=Array.from(document.querySelectorAll('button')).find(x=>/^(确认|确定|安装)$/.test((x.textContent||'').trim()) && x.offsetParent!==null); return b?1:0 })()`)
  if (confirm) { try { await mustTap(tapText('确认', { exact: true }), '确认安装') } catch { /* 无所谓，继续观察 */ } await sleep(3500) }
  const st = lastStatus('POST', /marketplace/)
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
  // 不猜 selector：把可见输入框 dump 出来，按「第一个可见的 text/textarea」填
  const inputInfo = await ev(`(function(){ var i=Array.from(document.querySelectorAll('input[type=text],input:not([type]),textarea')).filter(e=>e.offsetParent!==null); if(!i.length) return 'none'; var e=i[i.length-1]; return JSON.stringify({tag:e.tagName, type:e.type, ph:e.placeholder||'', n:i.length}); })()`)
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
  apiCalls.length = 0
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
  const st = lastStatus('POST', /\/api\/tasks/)
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
      apiCalls.length = 0
      try { await mustTap(tapText('删除', { exact: false }), '任务 删除') } catch (err) {
        return record(M, '删除任务', 'FAIL', `验证脚本自身失败：${err.message}`)
      }
      await sleep(1000)
      const cf = await ev(`(function(){ var b=Array.from(document.querySelectorAll('button')).find(x=>/^(确认|确定)$/.test((x.textContent||'').trim()) && x.offsetParent!==null); return b?1:0 })()`)
      if (cf) { try { await mustTap(tapText('确认', { exact: true }), '确认删除') } catch { /* noop */ } }
      await sleep(3500)
      const d = lastStatus('DELETE', /\/api\/tasks/)
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
  const entry = await ev(`(function(){ var b=Array.from(document.querySelectorAll('button,a,[role=button]')).find(x=>/删除|归档|重命名|编辑/.test((x.textContent||'').trim()) && x.offsetParent!==null); return b?(b.textContent||'').trim():'no' })()`)
  if (entry === 'no') {
    return record(M, '会话编辑/删除', 'NO_ENTRY',
      `列表无编辑/删除/归档控件；页面提示「${(txt.match(/[^ ]*会话[^ ]*/) || ['?'])[0]}」，会话需在 AI 页创建`)
  }
  record(M, '会话编辑/删除', 'NO_ENTRY', `存在「${entry}」控件但语义未确认，未自动化`)
}

/** 7. 密码箱 / 8. 邮箱：被主密码锁 */
async function checkGated(M, route, action) {
  await goto(route)
  if (await isGated()) return record(M, action, 'SKIPPED', GATE_NOTE)
  return record(M, action, 'NO_ENTRY', '已解锁但未找到写入口（需人工确认 UI 形态）')
}

// ---------- 跑 ----------
const CHECKS = {
  gateway: checkGateway,
  marketplace: checkMarketplace,
  cost: checkCost,
  instances: checkInstances,
  tasks: checkTasks,
  sessions: checkSessions,
  vault: () => checkGated('密码箱', '#/vault', '条目 CRUD'),
  email: () => checkGated('邮箱', '#/email', '账号/规则写路径'),
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
