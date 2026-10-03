// trace-newtask-bounds.mjs — 在 flow 跑的同时连续采样「+ 新任务」按钮坐标。
//
// 要解决的问题（2026-10-01 13:50）：
//   `tasks-crud.yaml` 里 `tapOn "\\+ 新任务"` 报 COMPLETED，但弹窗不开。
//   我的假设是「WebView 异步发布无障碍树 ⇒ Maestro 用的是数据到达前的旧坐标」。
//   这个假设**一直只是假设**，因为从没在同一时刻对过账。
//
// 本脚本提供对账的另一半：每 250ms 记一次按钮的 CSS 矩形 + hash + 卡片数，
// 带毫秒时间戳落到 CSV。配合 maestro.log 里 Maestro 自己打印的
// `TreeNode(... bounds=[...])` 与那条日志的时间戳，就能回答唯一的问题：
//
//     Maestro 点下去的那一刻，按钮到底在不在它以为的位置上？
//
// 三个可能的结论，处置完全不同：
//   1. 日志里的 bounds 与同时刻采样一致，但 tap 无效
//      ⇒ 坐标不是问题，是「合成点击没被 WebView 当 click」
//   2. 日志里的 bounds 与同时刻采样**不一致**
//      ⇒ 陈旧坐标确证
//   3. 采样在 tap 时刻断流/报空
//      ⇒ 判据自身不可靠，这次不算数（不能拿不完整的数据下结论）
//
// 用法：
//   node scripts/trace-newtask-bounds.mjs 180 > logs/bounds.csv 2>logs/bounds.err &
//   node scripts/maestro-run.mjs .maestro/tasks-crud.yaml
import { execFileSync } from 'node:child_process'

const ADB = 'C:/Users/86133/AppData/Local/Android/platform-tools/adb.exe'
const SERIAL = process.env.POCKET_SERIAL || '192.168.31.19:5555'
const PKG = 'com.kaixuan.opencode.pocket'
const PORT = process.env.POCKET_CDP_PORT || '9630'
const SECONDS = Number(process.argv[2] || 180)
const INTERVAL = 250

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
const adbSoft = (a) => { try { return execFileSync(ADB, ['-s', SERIAL, ...a], { encoding: 'utf8', timeout: 10000, maxBuffer: 33554432 }) } catch { return '' } }

function attach() {
  const pid = adbSoft(['shell', `pidof ${PKG}`]).trim().split(/\s+/)[0]
  if (!pid) return null
  const socks = adbSoft(['shell', `cat /proc/net/unix | grep -o 'webview_devtools_remote_[0-9]*'`])
    .split(/\r?\n/).map((l) => l.trim().replace('@', '')).filter(Boolean)
  const sock = socks.find((s) => s.endsWith(`_${pid}`)) || socks[socks.length - 1]
  if (!sock) return null
  adbSoft(['forward', `tcp:${PORT}`, `localabstract:${sock}`])
  return pid
}

async function listTargets() {
  for (let i = 0; i < 3; i++) {
    try {
      const l = await (await fetch(`http://127.0.0.1:${PORT}/json/list`, { signal: AbortSignal.timeout(6000) })).json()
      const p = l.find((t) => t.type === 'page')
      if (p) return p.webSocketDebuggerUrl.replace(/:\d+\//, `:${PORT}/`)
    } catch { /* 转发可能被重建，等下一轮 */ }
    attach()
    await sleep(700)
  }
  return null
}

const PROBE = `(function(){
  var b=Array.prototype.slice.call(document.querySelectorAll('button')).filter(function(x){return (x.textContent||'').trim()==='+ 新任务'})[0];
  var r=b?b.getBoundingClientRect():null;
  return JSON.stringify({
    hash: location.hash,
    y: r?Math.round(r.top+r.height/2):null,
    rect: r?[Math.round(r.left),Math.round(r.top),Math.round(r.right),Math.round(r.bottom)].join(' '):'',
    cards: document.querySelectorAll('.task-card').length,
    sheet: document.querySelectorAll('.create-task-form').length,
  });
})()`

async function sampleOnce(url) {
  const ws = new WebSocket(url)
  let id = 0
  const pending = new Map()
  ws.addEventListener('message', (e) => {
    const m = JSON.parse(e.data)
    if (m.id && pending.has(m.id)) { pending.get(m.id)(m.result); pending.delete(m.id) }
  })
  const ok = await Promise.race([
    new Promise((r) => ws.addEventListener('open', () => r(true), { once: true })),
    new Promise((r) => setTimeout(() => r(false), 6000)),
  ])
  if (!ok) { ws.close(); return null }
  const v = await new Promise((r) => {
    const i = ++id
    const t = setTimeout(() => { pending.delete(i); r(null) }, 6000)
    pending.set(i, (x) => { clearTimeout(t); r(x) })
    ws.send(JSON.stringify({ id: i, method: 'Runtime.evaluate', params: { expression: PROBE, returnByValue: true } }))
  })
  ws.close()
  return v?.result?.value ? JSON.parse(v.result.value) : null
}

console.log('epoch_ms,wall,hash,y,rect,cards,sheet')
const t0 = Date.now()
let url = await listTargets()
let dropped = 0
let taken = 0
while (Date.now() - t0 < SECONDS * 1000) {
  if (!url) { url = await listTargets(); if (!url) { dropped++; await sleep(INTERVAL); continue } }
  const s = await sampleOnce(url)
  const now = Date.now()
  if (!s) { dropped++; url = null; await sleep(INTERVAL); continue }
  taken++
  const wall = new Date(now).toISOString()
  console.log(`${now},${wall},${s.hash},${s.y === null ? '' : s.y},"${s.rect}",${s.cards},${s.sheet}`)
  await sleep(INTERVAL)
}
console.error(`[trace] 采样 ${taken} 次，空/断流 ${dropped} 次，时长 ${SECONDS}s`)
if (dropped > taken) {
  console.error('[trace] ⚠️ 断流多于有效采样 ⇒ 判据自身不可靠，别拿这份数据下结论')
}
