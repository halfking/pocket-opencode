// verify-edge-route-reach.mjs — 交叉核对：**后端每一条路由，是否都能从边缘到达。**
//
// 这是本仓反复吃亏的一类缺陷的通用判据：
//   BUG-P（2026-09-30）闪卡路由存在，但「更多」页没有入口 —— 路由能进 ≠ 用户能到；
//   BUG-Q（同期）入口写的是 '/scheduled-tasks'，路由表里根本没有 —— 入口指向虚空；
//   §4.97（2026-10-03）边缘一个 /callback/ 规则都没有，飞书/企微事件投递静默失败。
// 三次的共同点：**后端是对的，坏在「从外面够不够得到」那一层**，而那一层没有任何检查。
//
// 判据问的是不变式：
//   对每个「未知路径回落前端」的 vhost（就是 verify-callback-routes.mjs 认定的那批），
//   把它的反代前缀全收集起来，然后要求**后端注册的每一条路由都被其中某个前缀覆盖**。
//   少一条前缀 = 那一族路由在公网入口上全部不可达，且 nginx 不会报错。
//
// 覆盖语义照 Go 1.22 http.ServeMux 的规则还原（这是最容易写错、也最容易给出假绿灯的地方）：
//   = /healthz   → 精确匹配
//   /api/        → 子树，/api/ 本身与 /api/xxx 都算
//   /ws          → **只**匹配 /ws 本身（没有尾斜杠就不是子树！）
//   /            → 兜底，全覆盖（所以纯 API 域天然满足，这个判据对它们是空转——
//                   正是本判据只针对「SPA 回落型」vhost 的原因）
//
// 路由来源用「剥注释 + 非测试文件」而不是裸正则：实测裸正则会把
// long_lived_route_audit_test.go 注释里的示例代码当成真路由（/path、path 两条）。
//
// 用法：node scripts/verify-edge-route-reach.mjs [repoRoot]
//       可传 argv[2] 覆盖仓库根 —— 负控用它喂合成源，不必碰真文件。
import { readFileSync, readdirSync, statSync, existsSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

const DEFAULT_ROOT = fileURLToPath(new URL('..', import.meta.url))
const ROOT = process.argv[2] ?? DEFAULT_ROOT
const SERVER_DIR = join(ROOT, 'backend', 'internal', 'server')
const EDGE_DIR = join(ROOT, 'deploy', 'edge')

if (!existsSync(SERVER_DIR) || !existsSync(EDGE_DIR)) {
  console.error('[FAIL] 路径不对：需要同时存在 ' + SERVER_DIR + ' 与 ' + EDGE_DIR)
  process.exit(3)
}

// ---------- 1. 收集后端路由 ----------
/**
 * 只剥**行**注释，绝不碰块注释。
 *
 * 第一版是「先块后行」，结果提取到 0 条路由——被防空跑断言当场抓住。
 * 原因：internal/server 里 7 个非测试文件的块注释开闭符不配平（如 llm_gateway_admin_client.go
 * 的行注释里写着 /api/credentials/* 这种 glob），块注释正则把行注释里的开符当成块注释开头，
 * 一路吞到下一个闭符，把中间的真实路由全吃掉了。
 * （注：本注释里刻意不写「块注释闭符」三个字符以外的真实闭符序列，否则它会提前终止本注释。）
 * 结论：**顺序反了就会静默吃掉代码**，而这类失败输出是「0 条」而不是「报错」。
 *
 * 只剥行注释已足够：实测本仓路由注册都在行首，块注释里没有 HandleFunc。
 */
function stripGoLineComments(src) {
  return src
    .split(/\r?\n/)
    .map((line) => {
      const i = line.indexOf('//')
      return i === -1 ? line : line.slice(0, i)
    })
    .join('\n')
}

const goFiles = readdirSync(SERVER_DIR).filter(
  (f) => f.endsWith('.go') && !f.endsWith('_test.go'),
)
if (goFiles.length < 3) {
  console.error('[FAIL] 只扫到 ' + goFiles.length + ' 个非测试 .go，解析多半失明了。')
  process.exit(3)
}

const routes = new Set()
let rawCount = 0
let strippedCount = 0
for (const f of goFiles) {
  const raw = readFileSync(join(SERVER_DIR, f), 'utf8')
  const src = stripGoLineComments(raw)
  // 自检：剥注释这一步**不该**改变 HandleFunc 的出现次数。
  // 变了就说明剥离器吃掉了东西（第一版就是这样，且表现为「提取到 0 条」）。
  rawCount += (raw.match(/mux\.HandleFunc\(/g) || []).length
  strippedCount += (src.match(/mux\.HandleFunc\(/g) || []).length
  for (const m of src.matchAll(/mux\.HandleFunc\(\s*"(\/[^"]*)"/g)) {
    // 形状校验：路由必须是干净的路径。挡掉注释里残留的示例代码
    // （实测 long_lived_route_audit_test.go 注释里就写着 mux.HandleFunc("/path", …)）。
    if (/^\/[A-Za-z0-9/_.-]*$/.test(m[1])) routes.add(m[1])
  }
}
if (strippedCount < rawCount * 0.9) {
  console.error(
    '[FAIL] 剥行注释后 HandleFunc 出现次数从 ' + rawCount + ' 掉到 ' + strippedCount +
      '（掉了九成以上）—— 剥离器成片吃掉了代码，判据失明，拒绝给结论。',
  )
  process.exit(4)
}
// 「计数必须相等」是错的判据：本仓确实有 HandleFunc 出现在注释里
// （140 -> 139，剥掉它是**正确**行为）。要防的是成片删除，不是剥掉注释里的字样。
// 真正的下限由下面的哨兵路由与总数下限兜住。
const ROUTE_LIST = [...routes].sort()

// ---------- 2. 收集每个边缘 vhost 反代的前缀 ----------
function stripNginxComments(src) {
  return src
    .split(/\r?\n/)
    .map((line) => {
      let cut = line.length
      for (let i = 0; i < line.length; i++) {
        if (line[i] === '"' || line[i] === "'") break
        if (line[i] === '#') { cut = i; break }
      }
      return line.slice(0, cut)
    })
    .join('\n')
}

/** 取所有 location 前缀，形如 { exact, prefix, pattern, upstream }。 */
function locationsOf(src) {
  const out = []
  // (.+?) 而不是 (\S+)：nginx 的「精确匹配」写法是 `location = /healthz {`，
  // `=` 与路径之间有**空格**，是两个 token。第一版用 \S+ 只捕到 "="，
  // 于是 /healthz 被误报成不可达 —— 真仓因此变红，暴露的是判据的假阳性。
  const re = /^\s*location\s+(.+?)\s*\{/gm
  let m
  while ((m = re.exec(src)) !== null) {
    const rest = src.slice(m.index + m[0].length)
    const next = rest.search(/\n\s*location\s/)
    const block = next === -1 ? rest : rest.slice(0, next)
    const up = block.match(/proxy_pass\s+(https?:\/\/[^;\s]+)/)
    out.push({ pattern: m[1].trim(), upstream: up ? up[1] : null })
  }
  return out
}

const confFiles = readdirSync(EDGE_DIR).filter((f) => f.endsWith('.conf'))
if (!confFiles.length) {
  console.error('[FAIL] ' + EDGE_DIR + ' 下没有 .conf，判据会空转通过。')
  process.exit(3)
}

/** 与 Go 1.22 ServeMux 一致的覆盖判定。 */
function covers(pattern, route) {
  if (pattern === '/') return true // 兜底全覆盖
  if (pattern.startsWith('= ')) return pattern.slice(2) === route
  if (pattern.endsWith('/')) return route === pattern.slice(0, -1) || route.startsWith(pattern)
  return pattern === route // 无尾斜杠 → 精确
}

const vhosts = []
for (const f of confFiles) {
  const src = stripNginxComments(readFileSync(join(EDGE_DIR, f), 'utf8'))
  const locs = locationsOf(src)
  const root = locs.find((l) => l.pattern === '/')
  if (!root) continue
  const api = locs.find((l) => l.pattern === '/api/')
  // 「未知路径回落前端」的形状：/api/ 与 / 指向不同上游。
  // openpocket-api.* 那种 / 全量转 API 的域不在此列（对它本判据是空转）。
  if (!api || api.upstream === root.upstream) continue
  vhosts.push({ file: f, locs })
}

// ---------- 3. 防空跑 ----------
if (vhosts.length < 2) {
  console.error('[FAIL] 只识别出 ' + vhosts.length + ' 个「SPA 回落型」vhost，形状解析多半失明了。')
  process.exit(4)
}
if (ROUTE_LIST.length < 100) {
  console.error(
    '[FAIL] 只从 ' + goFiles.length + ' 个 .go 里提取到 ' + ROUTE_LIST.length +
      ' 条路由，远少于实际（约 150 条），提取多半失明了。',
  )
  process.exit(4)
}
if (!routes.has('/api/tasks')) {
  console.error('[FAIL] 连 /api/tasks 都没提到，提取器肯定坏了。')
  process.exit(4)
}

console.log(
  '后端路由 ' + ROUTE_LIST.length + ' 条（来自 ' + goFiles.length + ' 个非测试 .go）；' +
    '「SPA 回落型」vhost ' + vhosts.length + ' 个：',
)
for (const v of vhosts) {
  console.log('  ' + v.file.padEnd(34) + '反代前缀 ' + v.locs.map((l) => l.pattern).join(' '))
}

// ---------- 4. 判定 ----------
let failed = 0
for (const v of vhosts) {
  // 关键：**必须排除兜底的 location /**。
  // 它当然「覆盖」每一条路由，但那正是问题本身 —— 路由落到它就是被前端当 SPA 返回。
  // 第一版忘了排除，负控（删掉 /callback/ 前缀）跑出来**全绿**，就是这个原因。
  const carriers = v.locs.filter((l) => l.pattern !== '/')
  const uncovered = ROUTE_LIST.filter((r) => !carriers.some((l) => covers(l.pattern, r)))
  if (!uncovered.length) continue
  failed++
  console.error('\n[FAIL] ' + v.file + '：以下路由在公网入口上不可达（会落进前端兜底）：')
  for (const r of uncovered) console.error('  ✗ ' + r)
  console.error('  这些路由多半是后来加的，边缘没跟上。补一条 location 前缀即可。')
}

if (failed) process.exit(1)
console.log('\n[OK] 每个「SPA 回落型」vhost 的反代前缀都覆盖了后端的全部路由。')
