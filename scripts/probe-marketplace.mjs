// probe-marketplace.mjs — 核实「/api/marketplace/agents 404」的说法。
// 2026-09-30：审计反馈说该端点在只读探测下返回 401 而非 404，无法证实 404。
// 这里带有效 token 逐个探测 marketplace 三个路由，分清「未注册」与「需要鉴权」。
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const BASE = process.env.POCKET_BASE || 'http://localhost:8088'
const src = readFileSync(join(ROOT, 'backend/internal/server/server_assistant.go'), 'utf8')
const pass = (src.match(/devPass\s*=\s*"([^"]+)"/) || [])[1]

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: pass }),
})
if (!login.ok) { console.error('LOGIN_FAIL', login.status); process.exit(1) }
const { token } = await login.json()

const PATHS = [
  '/api/marketplace/packages',
  '/api/marketplace/releases',
  '/api/marketplace/agents',
  '/api/marketplace/installs',
  '/api/marketplace/router',
]
for (const p of PATHS) {
  for (const [tag, headers] of [
    ['auth', { Authorization: `Bearer ${token}` }],
    ['noauth', {}],
  ]) {
    const res = await fetch(BASE + p, { headers })
    const body = (await res.text()).slice(0, 80).replace(/\s+/g, ' ')
    const verdict =
      res.status === 404 ? '未注册(mux 404)'
      : res.status === 401 || res.status === 403 ? '存在但需鉴权'
      : res.status < 400 ? '存在且可用'
      : `其他(${res.status})`
    console.log(`${String(res.status).padEnd(4)} ${tag.padEnd(6)} ${p.padEnd(32)} ${verdict}  ${body}`)
  }
}

// 对照：后端源码里到底注册了哪些 marketplace 路由
const serverGo = readFileSync(join(ROOT, 'backend/internal/server/server.go'), 'utf8')
console.log('\n--- server.go 中 marketplace 相关注册 ---')
for (const line of serverGo.split('\n')) {
  if (line.includes('marketplace')) console.log('  ' + line.trim())
}
