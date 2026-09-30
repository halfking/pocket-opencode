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

/** evaluate 加超时保护：卡死的表达式不该让整个审计挂住。 */
async function ev(x, timeoutMs = 12000) {
  const res = await Promise.race([
    send('Runtime.evaluate', { expression: x, returnByValue: true }),
    sleep(timeoutMs).then(() => ({ __timeout: true })),
  ])
  if (res?.__timeout) return undefined
  return res?.result?.value
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
console.log('session restored, hash =', await ev('location.hash', 8000))
console.log(`\n逐路由渲染验证（${ROUTES.length} 条）\n`)

const rows = []
for (const r of ROUTES) {
  await ev(`location.hash = ${JSON.stringify('#' + r)}`)
  await sleep(2200)
  const hash = await ev('location.hash', 8000)
  const body = ((await ev(`(document.body.innerText||'').replace(/\\s+/g,' ').trim()`, 8000)) || '')
  const guarded = String(hash).includes('/login')
  const notFound = /404|页面未找到|not found|找不到页面/i.test(body)
  const blank = body.length < 12
  const onlyLoading = body.length < 30 && /加载|loading|…|\.\.\./i.test(body)
  const verdict = guarded ? 'GUARDED(被路由守卫弹走)'
    : notFound ? 'NOT_FOUND'
    : blank ? 'BLANK(空白页)'
    : onlyLoading ? 'STUCK_LOADING'
    : 'OK'
  rows.push({ r, hash, verdict, len: body.length, body })
  console.log(`${verdict.padEnd(22)} ${r.padEnd(34)} -> ${String(hash).padEnd(34)} len=${body.length}`)
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
