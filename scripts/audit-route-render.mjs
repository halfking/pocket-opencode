#!/usr/bin/env node
/**
 * audit-route-render.mjs — 真机上逐个导航到路由，验证**页面真的渲染出来了**。
 *
 * ## 为什么不用「逐个点入口」
 *
 * 第一版是逐个点「更多」页的入口，结果脚本卡死 150 秒无输出 —— CDP 派发指针
 * 事件后无法可靠判断点击是否落地，也没有超时保护。改用直接导航：稳定、可重复，
 * 且失败时能精确定位到是哪个路由。
 *
 * 两者是互补的，分工要记清：
 *   - **入口静态对账**（本轮手写的那次 grep）→ 发现「有没有入口」「入口指向的路径
 *     存不存在」。BUG-P（/flashcards 没入口）与 BUG-Q（/scheduled-tasks 指向
 *     不存在的路由）都是这么抓到的。
 *   - **逐路由渲染验证**（本脚本）→ 发现「路由存在但页面渲染不出来」。
 *
 * 静态对账看不见「页面渲染失败」，动态验证看不见「没有入口」。两个都要做。
 *
 * 判据（都必须是**能区分通/不通**的，不是「页面打开了」这种恒真断言）：
 *   - 落地 hash 与目标一致（没被路由守卫弹走）
 *   - body 文本长度 > 阈值（不是空白页）
 *   - 不含 404 / 页面未找到 文案
 *   - 不停在 loading 状态（等 2.2s 后仍只有 loading 视为可疑）
 *
 * 用法：POCKET_SERIAL=... POCKET_MASTER=... node scripts/audit-route-render.mjs
 */
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '4c308e2e'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9245'
const MASTER = process.env.POCKET_MASTER || ''
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

// 顶层可达页面（参数化子路由不在此列 —— 它们需要真实 id 才能构造）
const ROUTES = [
  '/ai', '/ai-chat', '/notes', '/notes/new', '/email', '/email/settings',
  '/finance', '/contacts', '/vault', '/pkm/today', '/study', '/meetings',
  '/rss', '/more', '/instances', '/tasks', '/sessions', '/notifications',
  '/settings', '/settings/llm-gateway', '/settings/permissions',
  '/settings/scheduled-tasks', '/settings/scheduled-tasks/new',
  '/marketplace/skills', '/marketplace/agents', '/marketplace/workbuddies',
  '/local-agent', '/agents', '/cost', '/gateway', '/servers',
  '/flashcards', '/flashcards/io', '/flashcards/browser', '/flashcards/stats',
  '/flashcards/new', '/flashcards/decks/nonexistent',
]

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
ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) } })
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')

/**
 * evaluate。**不带超时**。
 *
 * 前一版用 `Promise.race([send(...), sleep(12s)])`，超时后返回 undefined ——
 * 但底层的 CDP `send` **仍在排队执行**。于是 `location.hash = X` 可能十几秒后才
 * 生效，而主循环早已推进到几十条之后，赋值不断堆积。读到的 hash 是**很后面**
 * 的路由：
 *     /ai        -> #/flashcards/browser     (browser 在列表第 34 位)
 *     /ai-chat   -> #/flashcards/new         (第 35 位)
 * 报出 17 条 HASH_MISMATCH，全是假阳性。
 *
 * 超时只会把「慢」变成「错」。CDP 单条 evaluate 在这台设备上从未超过几秒，
 * 直接等它返回即可。真正的「等页面就绪」由下面的 waitHash 负责。
 */
async function ev(x) {
  const res = await send('Runtime.evaluate', { expression: x, returnByValue: true })
  return res?.result?.value
}

/**
 * 设置 hash 并**轮询等待它真的生效**，而不是固定 sleep。
 *
 * 固定 sleep 有两个毛病：短了读到旧页面（假阴性），长了白等。
 * 更要紧的是它掩盖了「赋值尚未生效」——上一版就是这么把 17 条正常路由报成
 * HASH_MISMATCH 的。判据应当是「状态达到期望」，而不是「等够时间」。
 */
async function gotoHash(route, timeoutMs = 12000) {
  await ev(`location.hash = ${JSON.stringify(route)}`)
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    const h = await ev('location.hash')
    if (h === route) return true
    await sleep(300)
  }
  return false
}

// 会话恢复
await ev(`location.hash = '#/login'`); await sleep(2600)
if (await ev(`!!document.querySelector('input[placeholder*="主密码"]')`)) {
  await ev(`(function(){var el=document.querySelector('input[placeholder*="主密码"]');var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(MASTER)});el.dispatchEvent(new Event('input',{bubbles:true}));return 1})()`)
  await sleep(1700)
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim().indexOf('解锁')>=0});if(b)b.click();return 1})()`)
  await sleep(4200)
}
if (await ev(`!!document.querySelector('input[placeholder*="用户名"]')`)) {
  const devPass = (readFileSync('backend/internal/server/server_assistant.go', 'utf8').match(/devPass\s*=\s*"([^"]+)"/) || [])[1] || ''
  const fillBy = (sel, val) => `(function(){var el=document.querySelector(${JSON.stringify(sel)});if(!el)return 'NF';var s=Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el),'value').set;s.call(el,${JSON.stringify(val)});el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return 'ok'})()`
  await ev(fillBy('input[placeholder*="用户名"]', 'admin'))
  await ev(fillBy('input[type="password"]', devPass)); await sleep(900)
  await ev(`(function(){var b=Array.prototype.slice.call(document.querySelectorAll('button')).find(function(x){return (x.textContent||'').trim().indexOf('登录')>=0});if(b)b.click();return 1})()`)
  await sleep(6500)
}
console.log('session restored, hash =', await ev('location.hash'))
console.log(`\n逐路由渲染验证（${ROUTES.length} 条）\n`)

const rows = []
for (const r of ROUTES) {
  const target = '#' + r
  // 轮询等待 hash 真的到位，而不是固定 sleep（理由见 gotoHash 的注释）
  const arrived = await gotoHash(target)
  await sleep(1200) // hash 到位后给页面渲染 / 拉数据的时间
  const hash = await ev('location.hash')
  const body = ((await ev(`(document.body.innerText||'').replace(/\\s+/g,' ').trim()`)) || '')
  const guarded = String(hash).includes('/login')
  // 关键判据：**落地 hash 必须等于目标**。
  //
  // 这一条第一版漏了，结果报出「37/37 全通过」而实际日志长这样：
  //     OK  /agents        -> #/ai            len=165
  //     OK  /cost          -> #/ai-chat       len=305
  //     OK  /flashcards    -> #/more          len=526
  // 落地页根本不是目标页，只因为「有内容、非空白、不含 404」就判了 OK。
  // 页面内容是上一条路由的残留 -> 非空白 -> 假阳性。
  //
  // 教训：判据里少一条，就等于把「没测」当成「测过了」。自己定的纪律
  // （断言要能区分通/不通）自己也违反了。
  const mismatch = !arrived || (!guarded && String(hash) !== target)
  const notFound = /404|页面未找到|not found|找不到页面/i.test(body)
  const blank = body.length < 12
  const onlyLoading = body.length < 30 && /加载|loading|…|\.\.\./i.test(body)
  const verdict = guarded ? 'GUARDED(被路由守卫弹走)'
    : mismatch ? `HASH_MISMATCH(期望 ${target} 实际 ${hash})`
    : notFound ? 'NOT_FOUND'
    : blank ? 'BLANK(空白页)'
    : onlyLoading ? 'STUCK_LOADING'
    : 'OK'
  rows.push({ r, hash, verdict, len: body.length, body })
  console.log(`${verdict.padEnd(30)} ${r.padEnd(34)} -> ${String(hash).padEnd(34)} len=${body.length}`)
}

const bad = rows.filter((x) => x.verdict !== 'OK')
console.log(`\n=== SUMMARY: ${rows.length - bad.length} / ${rows.length} 路由渲染正常 ===`)
if (bad.length) {
  console.log('\n异常路由：')
  for (const b of bad) {
    console.log(`  ${b.verdict.padEnd(22)} ${b.r}  ->  ${b.hash}`)
    console.log(`      body="${b.body.slice(0, 110)}"`)
  }
}
process.exitCode = bad.length ? 1 : 0
