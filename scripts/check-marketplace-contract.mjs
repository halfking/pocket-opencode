#!/usr/bin/env node
/**
 * check-marketplace-contract.mjs — 门禁：前端 marketplace 真正会调的每个 API 都得通。
 *
 * ## 为什么做这个
 *
 * 之前围绕 `/api/marketplace/agents` 有过两轮结论，都不成立：
 *   - 一轮记「404 = 路由没注册」——那次探测**没带 token**，被 requireAuth 先挡成 401，
 *     路由是否注册根本走不到。
 *   - 另一轮记「市场接口 401」——同样是没带凭证的只读探测。
 *
 * 实测（带**有效** token，隔离后端 18101）：
 *   /api/marketplace/packages?kind=agent -> 200 {"packages":[]}
 *   /api/marketplace/packages?kind=skill -> 200 {"packages":[...]}
 *   /api/marketplace/releases             -> 200 {"releases":[]}
 *   /api/marketplace/agents               -> 404
 *
 * 而 `frontend/src/features/marketplace/api.ts` 的 `base = '/api/marketplace'` 下
 * **只**列了 packages / releases / packages/{id}/versions / submit / review /
 * publish / install / revoke ——**没有任何地方调 `/api/marketplace/agents`**。
 *
 * ⇒ `/api/marketplace/agents` 是**契约里不存在的 URL**。404 不是缺陷，401 也不是。
 *    「智能体市场」页面（AgentMarketView）走的是 `packages?kind=agent`，是通的。
 *
 * 这道门禁的判据不是「某个 URL 该不该存在」，而是：
 *   **前端源码里真实列出的那些 marketplace 路径，逐个拿有效 token 打一遍，必须 2xx。**
 * 这样「页面看着在、接口其实不通」这类问题才会被抓到 —— 那才是真缺陷。
 *
 * 读方法的门禁：只读 GET，不触发任何写路径。
 *
 * 用法：
 *   POCKET_AUTH_PASS=... node scripts/check-marketplace-contract.mjs
 *   POCKET_VERIFY_BASE=http://127.0.0.1:18101 node scripts/check-marketplace-contract.mjs
 */
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { requireDevPass } from './lib/dev-pass.mjs'

const SELF = path.resolve(fileURLToPath(import.meta.url))
const ROOT = path.resolve(path.dirname(SELF), '..')
const API_TS = path.join(ROOT, 'frontend', 'src', 'features', 'marketplace', 'api.ts')
const BASE = process.env.POCKET_VERIFY_BASE || 'http://127.0.0.1:18101'

/** 从 api.ts 里抽出 base 常量 + 所有 `${base}/xxx` 形态的子路径。 */
function extractPaths(src) {
  const base = (src.match(/const\s+base\s*=\s*['"]([^'"]+)['"]/) || [])[1]
  if (!base) return { base: null, paths: [] }
  const paths = new Set()
  // 只取 `…${base}/…` 里的后半段；带 {…} 参数的替换成实测用的具体值。
  for (const m of src.matchAll(/\$\{base\}([^`'"]*)/g)) {
    let p = m[1].split('?')[0]
    // 末尾的 ${…} 是**查询串**占位符（listPackages 里的 `${base}/packages${query}`），
    // 不是路径参数 —— 不去掉就会拼出 `/packagesX` 这种根本不存在的 URL，
    // 然后门禁会拿一个自己造出来的地址去判后端有缺陷。
    // （头一版就栽在这：判据红灯，报的却是「后端不可达」，真凶是抽取器。）
    p = p.replace(/\$\{[^}]+\}$/, '')
    p = p.replace(/\$\{[^}]+\}/g, 'X').replace(/\/{2,}/g, '/').replace(/\/+$/, '')
    if (p) paths.add(p)
  }
  return { base, paths: [...paths] }
}

const src = readFileSync(API_TS, 'utf8')
const { base, paths } = extractPaths(src)
// 这里保留 exit 2：解析不出自己的输入 ⇒ **判据自身跑不起来**，
// 与「环境没准备好」不是一回事（见下面两处 exit 3 的说明）。
if (!base) { console.error('❌ 没能从 api.ts 解析出 base —— 判据失效，别下结论'); process.exit(2) }
console.log(`api.ts 的 base = ${base}`)
console.log(`抽出 ${paths.length} 条路径\n`)

// 退出码语义（2026-10-05 统一，对齐 run-gates.mjs 的约定）：
//
//	0  = 跑到了被检查对象且通过
//	1  = 跑到了被检查对象且**不通过**（真判红）
//	2  = 判据自身跑不起来（解析不出自己的输入、脚本有 bug）
//	3  = **前置缺失**：根本没跑到被检查对象，拒绝给结论
//
// 原来后两处前置缺失也报 2。危害不在数字本身，在于 2 与 1 在一次全量扫描里
// 长得一样：看到「红」的人会先去查脚本/判据，而真正要做的是「先把隔离后端起起来」。
// 一条前置门禁最常见的失败原因就是环境没起，把它归到「判据坏了」那一档，
// 就会有人去改不该改的代码。
const health = await fetch(`${BASE}/healthz`, { signal: AbortSignal.timeout(5000) }).catch((e) => ({ status: 'ERR:' + e.message }))
if (health.status !== 200) {
  console.error(`[前置缺失] 隔离后端 ${BASE} 不通（${health.status}）—— 这次没有跑到被检查对象，`)
  console.error('  所以退出码是 3 而不是 1。先起隔离后端再重跑；不要把这一条当成判红去排查。')
  process.exit(3)
}

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: requireDevPass() }),
  signal: AbortSignal.timeout(15000),
})
const { token } = await login.json()
if (!token) {
  console.error('[前置缺失] 登录不通，拿不到有效 token —— 下面全是 401，会被误读成「路由没注册」。')
  console.error('  这次同样没跑到被检查对象，退出码是 3 而不是 1。')
  process.exit(3)
}
console.log(`登录 200，token ${String(token).length} 字符（**带凭证**，所以 401 不会再冒充成路由缺失）\n`)

// 只读 GET 路径集合。写方法（submit/publish/install/revoke/review）**不探测** ——
// 那会往库里写真实数据，不是这个门禁该干的事。
const READ_ONLY = /^\/(packages|releases)/

const checks = []
for (const p of paths) {
  if (!READ_ONLY.test(p)) { console.log(`  SKIP  ${p}（写路径，本门禁不探）`); continue }
  const r = await fetch(`${BASE}${base}${p}`, { headers: { authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(15000) })
  const body = String(await r.text()).slice(0, 70).replace(/\s+/g, ' ')
  const pass = r.status >= 200 && r.status < 300
  checks.push([`${base}${p} 在有效 token 下 2xx`, pass])
  console.log(`  ${r.status}  ${(base + p).padEnd(42)} ${body}`)
}

// 幻影 URL 复核：agents 不是契约的一部分，但把结论钉住，别再被反复讨论。
const ghost = await fetch(`${BASE}/api/marketplace/agents`, { headers: { authorization: `Bearer ${token}` }, signal: AbortSignal.timeout(15000) })
const noCred = await fetch(`${BASE}/api/marketplace/agents`, { signal: AbortSignal.timeout(15000) })
console.log(`\n  ${ghost.status}  /api/marketplace/agents（有效 token）   ${String(await ghost.text()).slice(0, 50)}`)
console.log(`  ${noCred.status}  /api/marketplace/agents（无凭证）`)
checks.push(['幻影 URL 在有效 token 下是 404（证明「404=未注册」这条本身是对的）', ghost.status === 404])
checks.push(['幻影 URL 在无凭证时是 401（证明历史上那两轮只读探测根本没走到路由判定）', noCred.status === 401])

let ok = true
console.log('')
for (const [n, p] of checks) { if (!p) ok = false; console.log(`  ${p ? 'PASS' : 'FAIL'}  ${n}`) }
console.log(`\n结论：${ok ? '前端 marketplace 实际使用的只读接口全部可达' : '有接口不可达 —— 这才是真缺陷'}`)
console.log('      /api/marketplace/agents 不在前端契约里，404/401 都不是缺陷。')
process.exitCode = ok ? 0 : 1
