// verify-endpoints-no-device.mjs — 不用真机，直接从后端侧结掉三条待验项。
//
// 为什么值得单独做：这三条都需要「带有效 token 打真实端点」，
// 而真机此刻 adb 不可用。但它们**本来就不依赖 UI**，
// 之前一直挂着只是因为当时是用设备侧探针做的。
//
// 三条：
//   A. /api/marketplace/agents 到底是 404 还是 401
//      （verifier 质疑「只读探测下返回 401，无法证实 404」）
//      ⇒ 做法：先登录拿 token，再用 token 打；同时打一条**对照路由**
//        证明 token 本身是好的。401 与 404 必须靠这个对照才分得开。
//   B. 生产后端相对本地代码落后哪些端点
//   C. 前端 meeting-* 与后端 mtg_* 两套 id 体系到底通不通
import { execFileSync } from 'node:child_process'

const LOCAL = process.env.POCKET_API_BASE || 'http://127.0.0.1:18099'
const PROD = 'https://pocket.itestu.cn'
const USER = 'admin'

// dev 口令从 Go 源码取，不在本文件里落明文
const goSrc = execFileSync('git', ['-C', '.', 'show', 'HEAD:backend/internal/server/server_assistant.go'], { encoding: 'utf8', maxBuffer: 33554432 })
const m = goSrc.match(/devPass\s*=\s*"([^"]+)"/)
if (!m) { console.error('未能从 Go 源码取 dev 口令'); process.exit(2) }
const PASS = m[1]

async function login(base, password) {
  const r = await fetch(`${base}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: USER, password }),
  })
  const t = await r.text()
  let j = null
  try { j = JSON.parse(t) } catch { /* 非 JSON */ }
  return { status: r.status, body: j || t.slice(0, 200), token: j?.token || j?.access_token || null }
}

async function probe(base, token, path) {
  try {
    const r = await fetch(`${base}${path}`, {
      headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
      signal: AbortSignal.timeout(20000),
    })
    const t = await r.text()
    let shape = ''
    try {
      const j = JSON.parse(t)
      if (Array.isArray(j)) shape = `array(${j.length})`
      else if (j && typeof j === 'object') shape = `object{${Object.keys(j).slice(0, 6).join(',')}}`
      else shape = typeof j
    } catch { shape = `non-json(${t.slice(0, 40)})` }
    return { status: r.status, shape, head: t.slice(0, 160).replace(/\s+/g, ' ') }
  } catch (e) {
    return { status: 'ERR', shape: '-', head: String(e?.message || e).slice(0, 80) }
  }
}

const row = (n, r) => console.log(`  ${String(r.status).padEnd(5)} ${n.padEnd(34)} ${String(r.shape).padEnd(26)} ${r.head.slice(0, 80)}`)

// ══════════════ A. 本地：marketplace/agents 的 404 vs 401 ══════════════
console.log(`\n═══ A. 本地 ${LOCAL} · 带 token 探端点 ═══`)
const l = await login(LOCAL, PASS)
console.log(`登录: status=${l.status} token=${l.token ? l.token.slice(0, 10) + '…' : '<无>'} body=${JSON.stringify(l.body).slice(0, 120)}`)

if (!l.token) {
  console.error('❌ 本地登录失败，无法继续 A/B/C（不猜、不用无 token 的结果下结论）')
  process.exit(1)
}

console.log('\n对照组 + 目标端点：')
const A_PATHS = [
  '/api/health',                       // 一定 200，证明 token 不是问题
  '/api/tasks',                        // 已知 200 的业务端点
  '/api/agents',                       // BUG-AL 相关
  '/api/chat-agents',                  // 已知有真实 id
  '/api/marketplace/packages',
  '/api/marketplace/agents',           // ← 争议点
]
const A_RES = {}
for (const p of A_PATHS) { const r = await probe(LOCAL, l.token, p); A_RES[p] = r; row(p, r) }

const ctrl = A_RES['/api/tasks']
const tgt = A_RES['/api/marketplace/agents']
console.log(`\n判定：`)
console.log(`  对照 /api/tasks = ${ctrl.status}（证明 token 有效、鉴权链路通）`)
console.log(`  目标 /api/marketplace/agents = ${tgt.status}`)
if (tgt.status === 404) console.log('  ⇒ **404 成立**（同 token 下对照路由 200，说明不是鉴权问题）')
else if (tgt.status === 401) console.log('  ⇒ 仍是 401，404 说法不成立')
else console.log(`  ⇒ 实际是 ${tgt.status}，既不是 404 也不是 401；以实测为准`)

// 无 token 对照：证明 401 确实是「没带 token」时的表现
const noTok = await fetch(`${LOCAL}/api/marketplace/agents`, { signal: AbortSignal.timeout(15000) })
console.log(`  无 token 打同一路径 = ${noTok.status}  ← 401 的真实来源`)

// ══════════════ B. 生产 vs 本地 端点漂移 ══════════════
console.log(`\n═══ B. 生产 ${PROD} vs 本地 · 端点漂移 ═══`)
const p = await login(PROD, PASS)
console.log(`生产登录: status=${p.status} token=${p.token ? p.token.slice(0, 10) + '…' : '<无>'} body=${JSON.stringify(p.body).slice(0, 140)}`)
if (p.token) {
  console.log('\n同一路径在本地 / 生产的状态对照：')
  for (const path of ['/api/flashcards', '/api/flashcards/notes', '/api/rss/items', '/api/marketplace/packages', '/api/chat-agents', '/api/marketplace/agents', '/api/tasks']) {
    const a = await probe(LOCAL, l.token, path)
    const b = await probe(PROD, p.token, path)
    const flag = String(a.status) === String(b.status) ? '' : '   ← 漂移'
    console.log(`  ${path.padEnd(30)} 本地=${String(a.status).padEnd(5)} 生产=${String(b.status).padEnd(5)} ${String(b.shape).padEnd(24)}${flag}`)
  }
} else {
  console.log('  生产登录失败，跳过漂移对照（不猜）')
}

// ══════════════ C. meeting-* vs mtg_* 两套 id ══════════════
console.log(`\n═══ C. 前端 meeting-* 与后端 mtg_* 两套 id ═══`)
for (const path of ['/api/meetings', '/api/sessions']) {
  const r = await probe(LOCAL, l.token, path)
  row(path, r)
  if (r.status === 200) {
    try {
      // ⚠️ 这里原来漏了 await，fetch 返回 Promise 被当成对象去 JSON.parse，
      //    报 "[object Promise] is not valid JSON"。判据自己坏了必须先修。
      const res2 = await fetch(`${LOCAL}${path}`, { headers: { Authorization: `Bearer ${l.token}` } })
      const j = JSON.parse(await res2.text())
      const arr = Array.isArray(j) ? j : (j.meetings || j.sessions || j.items || [])
      const ids = (arr || []).map((x) => x.id).filter(Boolean)
      const pre = {}
      for (const id of ids) { const k = String(id).split('-')[0]; pre[k] = (pre[k] || 0) + 1 }
      console.log(`    id 前缀分布: ${JSON.stringify(pre)}  样本: ${JSON.stringify(ids.slice(0, 5))}`)
    } catch (e) { console.log(`    解析失败: ${e.message}`) }
  }
}
