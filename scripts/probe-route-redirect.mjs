#!/usr/bin/env node
/**
 * probe-route-redirect.mjs — 查清 audit-route-render.mjs 报的那 6 条 HASH_MISMATCH
 * 到底**什么时候**、被**什么**改写了 hash。
 *
 * ## 背景
 *
 * audit-route-render.mjs 报 6 条落地 hash 与目标不符：
 *   /agents -> /ai, /gateway -> /ai-chat, /servers -> /study,
 *   /flashcards/io -> /meetings, /flashcards/stats -> /more,
 *   /flashcards/decks/nonexistent -> /notes
 * 落点全是 BottomNav 的 tab 路由，但每次都不一样，上一轮没有定性。
 *
 * ## 为什么不猜，直接采样
 *
 * 「落地 hash 不对」至少有四种成因，处置完全不同：
 *   1. 路由守卫 beforeEach 主动 redirect  → 真缺陷（BUG 风格：被弹走）
 *   2. 页面挂载后某段异步逻辑 router.push → 真缺陷（BUG 风格：加载完就跑掉）
 *   3. 渲染进程崩溃/重载，hash 被 localStorage 里的旧值恢复 → 环境问题
 *   4. CDP 派发堆积，读到的是几轮之前的 hash → **探针自己的假阳性**
 *
 * 只看终态无法区分。做法是：设置 hash 之后以 200ms 为间隔连续采样
 * 25 次（5 秒），记录 hash 的**变化时间线**，同时监听
 * Page.frameNavigated / Runtime.exceptionThrown / console。
 * 「先对后错」= 异步跳转；「从头就不对」= 守卫或拒绝；「伴随重载」= 环境。
 *
 * 判据必须能区分：只要时间线上出现过目标 hash，就说明导航本身是成功的，
 * 后续改写另算。
 *
 * 用法：POCKET_SERIAL=... node scripts/probe-route-redirect.mjs
 */
import { execFileSync } from 'node:child_process'
import { readFileSync } from 'node:fs'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9246'
const MASTER = process.env.POCKET_MASTER || ''
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adb = (a, t = 60000) => execFileSync(ADB, a, { encoding: 'utf8', timeout: t, maxBuffer: 33554432 })

// audit-route-render.mjs 报出 HASH_MISMATCH 的 6 条
const SUSPECTS = [
  '/agents',
  '/gateway',
  '/servers',
  '/flashcards/io',
  '/flashcards/stats',
  '/flashcards/decks/nonexistent',
]
// 对照组：上一轮判定为 OK 的 2 条。必须有对照组，否则「全都失败」和「全都正常」
// 在报告上长得一样。
const CONTROLS = ['/cost', '/flashcards/browser']

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
const events = []
const send = (m, p = {}) => new Promise((r) => { const i = ++id; pending.set(i, r); ws.send(JSON.stringify({ id: i, method: m, params: p })) })
ws.addEventListener('message', (e) => {
  const m = JSON.parse(e.data)
  if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id); return }
  if (m.method === 'Page.frameNavigated') events.push(['frameNavigated', m.params?.frame?.url || ''])
  if (m.method === 'Runtime.exceptionThrown') {
    const d = m.params?.exceptionDetails
    events.push(['exception', d?.exception?.description || d?.text || ''])
  }
  if (m.method === 'Runtime.consoleAPICalled' && ['error', 'warning'].includes(m.params?.type)) {
    events.push(['console.' + m.params.type, (m.params.args || []).map((a) => a.value ?? a.description ?? '').join(' ').slice(0, 200)])
  }
})
await new Promise((r) => ws.addEventListener('open', r))
await send('Runtime.enable')
await send('Page.enable')

const ev = async (x) => (await send('Runtime.evaluate', { expression: x, returnByValue: true }))?.result?.value

if (MASTER) {
  // 会话恢复（与 audit-route-render.mjs 同流程：先解锁主密码，再必要时登录）
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
}

console.log(`serial=${SERIAL} pid=${pid}`)
console.log(`起始 hash = ${await ev('location.hash')}\n`)

/**
 * 采样一条路由的完整时间线。
 * 返回 {verdict, timeline, seenTarget, firstHit, events}
 */
async function sample(route, { samples = 25, everyMs = 200 } = {}) {
  const target = '#' + route
  events.length = 0
  const t0 = Date.now()
  await ev(`location.hash = ${JSON.stringify(target)}`)

  const timeline = []
  let seenTarget = false
  let firstHitMs = -1
  for (let i = 0; i < samples; i++) {
    await sleep(everyMs)
    const h = await ev('location.hash')
    const t = Date.now() - t0
    if (h === target) { seenTarget = true; if (firstHitMs < 0) firstHitMs = t }
    if (!timeline.length || timeline[timeline.length - 1].h !== h) timeline.push({ t, h })
  }

  const finalHash = await ev('location.hash')
  const bodyLen = ((await ev(`(document.body.innerText||'').replace(/\\s+/g,' ').trim().length`)) || 0)
  const body = ((await ev(`(document.body.innerText||'').replace(/\\s+/g,' ').trim().slice(0,90)`)) || '')

  // 判据分档 —— 每档对应一种成因，处置不同
  let verdict
  if (finalHash === target) verdict = 'STABLE_最终停在目标'
  else if (!seenTarget) verdict = 'NEVER_ARRIVED_从未到达目标（守卫/拒绝）'
  else verdict = 'DRIFTED_到达后被改写（异步跳转或重载）'

  return { route, verdict, seenTarget, firstHitMs, timeline, finalHash, bodyLen, body, events: events.slice() }
}

const results = []
console.log('=== 嫌疑组（上一轮报 HASH_MISMATCH）===')
for (const r of SUSPECTS) {
  const res = await sample(r)
  results.push({ group: 'suspect', ...res })
  console.log(`${res.verdict.padEnd(34)} ${r.padEnd(34)} -> ${res.finalHash.padEnd(20)} len=${res.bodyLen}`)
  console.log(`    时间线: ${res.timeline.map((x) => `${x.t}ms:${x.h}`).join('  ')}`)
  if (res.events.length) res.events.forEach((e) => console.log(`    [${e[0]}] ${e[1]}`))
}

console.log('\n=== 对照组（上一轮判定 OK，必须复现 STABLE 才说明探针有区分能力）===')
for (const r of CONTROLS) {
  const res = await sample(r)
  results.push({ group: 'control', ...res })
  console.log(`${res.verdict.padEnd(34)} ${r.padEnd(34)} -> ${res.finalHash.padEnd(20)} len=${res.bodyLen}`)
  console.log(`    时间线: ${res.timeline.map((x) => `${x.t}ms:${x.h}`).join('  ')}`)
  if (res.events.length) res.events.forEach((e) => console.log(`    [${e[0]}] ${e[1]}`))
}

console.log('\n=== 汇总 ===')
for (const r of results) {
  console.log(`${r.group.padEnd(8)} ${r.verdict.padEnd(34)} ${r.route}`)
}

const controls = results.filter((r) => r.group === 'control')
const controlsOk = controls.every((r) => r.verdict.startsWith('STABLE'))
console.log(`\n探针自证：对照组 ${controls.filter((r) => r.verdict.startsWith('STABLE')).length}/${controls.length} STABLE` +
  (controlsOk ? ' ✅ 探针有区分能力' : ' ❌ 对照组也不稳，本轮数据不可用'))

process.exit(0)
