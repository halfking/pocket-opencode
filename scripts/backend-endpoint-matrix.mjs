#!/usr/bin/env node
// 后端端点矩阵：对比「无 PG」与「有 PG」两种 dev 后端下的可用性
// 目的是把「环境未配存储」和「代码缺陷」区分开，并为写操作验证选路。
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const BASE = process.env.POCKET_BASE || 'http://localhost:8088'
const src = readFileSync(join(ROOT, 'backend/internal/server/server_assistant.go'), 'utf8')
const pass = (src.match(/devPass\s*=\s*"([^"]+)"/) || [])[1]
if (!pass) { console.error('CANNOT_READ_DEV_PASS'); process.exit(3) }

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
  ['marketplace router',  'GET',  '/api/marketplace/agents', null],
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
