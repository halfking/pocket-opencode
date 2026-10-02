#!/usr/bin/env node
// 后端端点矩阵：对比「无 PG」与「有 PG」两种 dev 后端下的可用性
// 目的是把「环境未配存储」和「代码缺陷」区分开，并为写操作验证选路。
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const BASE = process.env.POCKET_BASE || 'http://localhost:8088'

// 2026-10-02 修复：这里原本从 Go 源码正则抓 `devPass = "..."` 常量，与
// scripts/maestro-run.mjs 是同一个缺陷、同一场事故的漏网之鱼——那天做安全整改时
// **有意删掉了那个硬编码口令**（server_assistant.go:221 现在是
// `devPass := s.cfg.DevAuthPass`，取不到配置就关闭 dev 旁路并告警），于是本脚本
// 每天都在登录前 exit(3)，整条端点矩阵直接不可用；而它报的那句
// `CANNOT_READ_DEV_PASS` 指向的是一个**已经不存在的东西**，看报错完全猜不到
// 真正原因是「该找环境变量了」。
//
// 正确的来源是环境变量：服务端读的就是 POCKET_AUTH_PASS，启动 pocketd 的那个
// shell 里本来就有。改读 env 之后「口令不进仓库」这个安全属性一点没变（它本来
// 就不在仓库里，是从源码抓的），反而更不容易和实际配置漂移。
//
// 顺序：POCKET_AUTH_PASS 环境变量 -> 旧版源码常量（仅当老 checkout 还在用）。
// 环境变量优先，源码只当兜底，否则本地临时换的口令会被源码里的旧值盖掉。
const GO = join(ROOT, 'backend/internal/server/server_assistant.go')
let src = ''
try { src = readFileSync(GO, 'utf8') } catch { /* 老 checkout 可能没有这个文件 */ }
const pass = process.env.POCKET_AUTH_PASS || (src.match(/devPass\s*=\s*"([^"]+)"/) || [])[1]
if (!pass) {
  console.error(
    '拿不到 dev 口令。请设置 POCKET_AUTH_PASS 环境变量，' +
    '并确保它与启动 pocketd 时用的 POCKET_AUTH_PASS 一致。\n' +
    '（服务端在 POCKET_AUTH_PASS 未设置时会直接关闭 dev 旁路，见 ' +
    'server_assistant.go devBypassCredentials）',
  )
  process.exit(3)
}

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: pass }),
})
if (!login.ok) { console.error('LOGIN_FAIL', login.status, await login.text()); process.exit(1) }
const { token, user } = await login.json()
console.log(`login ok: user=${user} tokenLen=${token.length}\n`)
const H = { Authorization: `Bearer ${token}` }
const J = { 'Content-Type': 'application/json', ...H }

// [名称, method, path, body?] —— GET 类用于探活，POST/PUT 用于探写路径
const CASES = [
  ['auth/me',            'GET',  '/api/auth/me', null],
  ['tasks list',         'GET',  '/api/tasks', null],
  ['tasks CREATE',       'POST', '/api/tasks', { title: 'PG matrix probe', source: 'local', description: 'store probe' }],
  ['notes list',         'GET',  '/api/notes', null],
  ['notes CREATE',       'POST', '/api/notes', { title: 'PG matrix note', content: 'probe' }],
  ['flashcards list',    'GET',  '/api/flashcards', null],
  ['meetings list',      'GET',  '/api/meetings', null],
  ['instances list',     'GET',  '/api/instances', null],
  ['sessions list',      'GET',  '/api/sessions', null],
  ['llm usage',          'GET',  '/api/llm/usage?days=7', null],
  ['llm quota',          'GET',  '/api/llm/quota', null],
  ['llm-gateway nodes',  'GET',  '/api/llm-gateway/nodes', null],
  ['llm-gateway config', 'GET',  '/api/llm-gateway/config', null],
  // 注意：/api/vault 没有注册（只有 /api/vault/sync/）——密码箱是**纯本地**功能
  // （SQLCipher 本地库 + Keystore 派生主密钥），后端只有同步子树。
  // 所以 GET /api/vault -> 404 是设计如此，不代表前端密码箱不可用。
  ['vault sync subtree',  'GET',  '/api/vault/sync/latest', null],
  ['marketplace pkgs',    'GET',  '/api/marketplace/packages', null],
  ['marketplace releases','GET',  '/api/marketplace/releases', null],
  // 这一条 404 是**预期**，而且它验的是「catch-all 路由有没有挂上」：
  // /api/marketplace/ 前缀由 server.go:857 的 handleMarketplaceRouter 兜住
  // （submit/review/publish/install/revoke/rate 都走它），而 /agents 并不是
  // 产品会调的子路径——前端 marketplaceApi（features/marketplace/api.ts）只用
  // /packages、/releases、/packages/{id}/versions 和那 6 个写动作。
  // 判别点在于**响应体形状**：路由挂着时返回 router 自己的 JSON
  // {"error":"not found"}；没挂则是 Go mux 的纯文本 "404 page not found"。
  // 写成别的名字会让人误以为是产品缺陷去追。
  ['marketplace catch-all router mounted', 'GET', '/api/marketplace/agents', null],
  ['scheduled tasks',    'GET',  '/api/scheduled-tasks', null],
  ['email accounts',     'GET',  '/api/email/accounts', null],
  ['rss sources',        'GET',  '/api/rss/sources', null],
]

const rows = []
for (const [name, method, path, body] of CASES) {
  let status = 0, note = ''
  try {
    const res = await fetch(BASE + path, {
      method, headers: body ? J : H, body: body ? JSON.stringify(body) : undefined,
    })
    status = res.status
    const t = await res.text()
    note = t.slice(0, 90).replace(/\s+/g, ' ')
  } catch (e) { note = 'ERR ' + e.message }
  rows.push({ name, method, path, status, note })
  console.log(`${String(status).padEnd(4)} ${method.padEnd(4)} ${name.padEnd(20)} ${path}`)
  if (status >= 400) console.log(`      -> ${note}`)
}

console.log('\n=== SUMMARY ===')
const ok = rows.filter((r) => r.status < 400)
const blocked = rows.filter((r) => r.status >= 400)
console.log(`可用 ${ok.length} / ${rows.length}`)
console.log('可用:', ok.map((r) => r.name).join(', ') || '(none)')
console.log('不可用:', blocked.map((r) => `${r.name}(${r.status})`).join(', ') || '(none)')
process.exit(0)
