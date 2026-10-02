// sweep-api-readonly.mjs — 对后端全部 GET 路由做一次带鉴权的只读扫描，产出接口健康全景。
//
// ## 为什么需要它
// 交接里「38 个 POST 端点未探」是一条**散文记账**，跨很多轮累积，无法复核，也一定
// 会再次过期。本脚本把它换成一份**可重跑**的产出：一条命令就能知道当下每个 GET
// 路由的真实状态码，不再依赖谁记得什么。
//
// ## 为什么只扫 GET
//   · GET 在本仓基本是只读的；
//   · POST 里混着会**改动真实邮件数据**的端点（purge / cleanup / organize / move /
//     classify / backfill / pipeline.run），而并行会话正在分析那批发票与垃圾邮件样本。
//     盲探等于替别人删样本。
// POST 不在本脚本范围内，这是有意的设计，不是遗漏。
//
// ## 不接进 gates
// 它需要一台**开着真机 WebView** 的设备（token 只能从那里取），CI 里跑不了。
// gates 里放一个必然失败或必然跳过的脚本，比不放更糟。
//
// ## 用法
//   adb forward tcp:9222 localabstract:webview_devtools_remote_<app pid>
//   node scripts/sweep-api-readonly.mjs
//   （API / CDP / 输出路径都可用环境变量覆盖：SWEEP_API / SWEEP_CDP / SWEEP_OUT）
//
// ## 纪律
//   · 先打一个对照路由证明 token 有效；401/403 时不产出任何结论，exit 3。
//     （本会话已经吃过一次亏：探针按「keys 里第一个长度>20」取 token，捞到快照 blob，
//      全程 401 却没有结论。）
//   · token 只打印长度，不回显内容。
//   · 响应体只取前 160 字节，20 位以上的长串一律打码（可能是凭据/令牌片段）。
//   · 15s 超时且**不重试** —— 重试会把慢端点变成压测。
import { readFileSync, writeFileSync } from 'node:fs'

const API = process.env.SWEEP_API || 'http://127.0.0.1:18099'
const CDP = process.env.SWEEP_CDP || 'http://127.0.0.1:9222'
const OUT = process.env.SWEEP_OUT || 'logs/get-sweep-20261003-0410.json'
const TIMEOUT_MS = 15000

// ---- 取 token（内容不回显） ----
async function getToken() {
  const list = await (await fetch(CDP + '/json')).json()
  const page = list.find(t => t.type === 'page' && t.webSocketDebuggerUrl)
  if (!page) throw new Error('没有可调试的 page target（adb forward 是否还在？）')
  const ws = new WebSocket(page.webSocketDebuggerUrl)
  await new Promise((res, rej) => {
    ws.onopen = res
    ws.onerror = () => rej(new Error('CDP WebSocket 连接失败'))
  })
  let id = 0
  const call = (method, params) => new Promise((res, rej) => {
    const myId = ++id
    const onMsg = (ev) => {
      const m = JSON.parse(ev.data)
      if (m.id !== myId) return
      ws.removeEventListener('message', onMsg)
      m.error ? rej(new Error(JSON.stringify(m.error))) : res(m.result)
    }
    ws.addEventListener('message', onMsg)
    ws.send(JSON.stringify({ id: myId, method, params }))
  })
  const r = await call('Runtime.evaluate', {
    expression: 'localStorage.getItem("pocket_token") || ""',
    returnByValue: true,
  })
  ws.close()
  const v = r && r.result && r.result.value
  if (!v) throw new Error('localStorage 里没有 pocket_token')
  return v
}

// ---- 枚举路由（剥行注释后再抽，避免注释里的注册混进来） ----
function listRoutes() {
  const src = readFileSync('backend/internal/server/server.go', 'utf8')
  const stripped = src.split(/\r?\n/).map(l => l.replace(/(^|\s)\/\/.*$/, '$1')).join('\n')
  const re = /mux\.HandleFunc\(\s*"([^"]+)"\s*,\s*([A-Za-z0-9_.]+)/g
  const out = []
  let m
  while ((m = re.exec(stripped)) !== null) out.push({ path: m[1], handler: m[2] })
  return out
}

// 打码：任何 20 位以上的长串都可能是凭据/令牌片段
function mask(s) {
  return s
    .replace(/[A-Za-z0-9_\-]{20,}/g, m => m.slice(0, 4) + '***(' + m.length + ')')
    .replace(/[\r\n]+/g, ' ')
}

const token = await getToken()
console.log('token 长度 ' + token.length + '（内容不回显）')

const routes = listRoutes()
console.log('注册路由 ' + routes.length + ' 条')

// 先打一个对照：证明 token 有效。全 401 时不产出任何结论。
const probe = await fetch(API + '/api/email/accounts', {
  headers: { Authorization: 'Bearer ' + token },
})
console.log('对照路由 /api/email/accounts -> ' + probe.status)
if (probe.status === 401 || probe.status === 403) {
  console.error('!! token 无效（' + probe.status + '）。不产出任何结论，退出 3。')
  process.exit(3)
}

const skip = p => p.startsWith('/ws') || p === '/healthz' || p.startsWith('/callback/')

const results = []
for (const r of routes) {
  if (skip(r.path)) { results.push({ path: r.path, handler: r.handler, status: 'skipped', note: '非 GET 目标/回调/ws' }); continue }
  const t0 = Date.now()
  const ctl = new AbortController()
  const timer = setTimeout(() => ctl.abort(), TIMEOUT_MS)
  let status, ms, body = ''
  try {
    const res = await fetch(API + r.path, {
      method: 'GET',
      headers: { Authorization: 'Bearer ' + token },
      signal: ctl.signal,
    })
    status = res.status
    body = (await res.text()).slice(0, 160)
  } catch (e) {
    status = 'ERR'
    body = String(e && e.message ? e.message : e)
  }
  clearTimeout(timer)
  ms = Date.now() - t0
  results.push({ path: r.path, handler: r.handler, status, ms, body: mask(body) })
  console.log(String(status).padEnd(5) + String(ms).padStart(6) + 'ms  ' + r.path)
}

writeFileSync(OUT, JSON.stringify({ at: new Date().toISOString(), api: API, count: results.length, results }, null, 1))

const tally = {}
for (const r of results) tally[r.status] = (tally[r.status] || 0) + 1
console.log('\n--- 汇总 ---')
console.log(JSON.stringify(tally))
console.log('非 2xx 明细：')
for (const r of results) {
  const ok = typeof r.status === 'number' && r.status >= 200 && r.status < 300
  if (!ok) console.log('  ' + String(r.status).padEnd(5) + r.path.padEnd(36) + r.handler.padEnd(30) + ' ' + String(r.body || r.note || '').slice(0, 90))
}
console.log('\n写入 ' + OUT)
