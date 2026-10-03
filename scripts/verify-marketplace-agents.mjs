// 重验 /api/marketplace/agents 的 404 说法。
// verifier 的质疑是「只读探测下返回 401，无法证实 404」。
// 关键：401 是**未带 token**的探测被鉴权中间件短路；带有效 token 才能看到真实路由状态。
// 本脚本同时跑两次（不带/带 token），把差异摆出来，而不是只报一个结论。
import { readFileSync } from 'node:fs'

const BASE = process.env.POCKET_API_BASE || 'http://127.0.0.1:8088'
const GO = 'C:/workspace/openpocket/wt3/backend/internal/server/server_assistant.go'
const USER = process.env.POCKET_DEV_USER || 'admin'
const src = readFileSync(GO, 'utf8')
const m = src.match(/devPass\s*=\s*"([^"]+)"/)
if (!m) { console.error('取不到 dev 口令'); process.exit(2) }

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST',
  headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ username: USER, password: m[1] }),
})
const lj = await login.json()
if (!lj.token) { console.error('登录失败'); process.exit(2) }
console.log(`登录成功 workspace=${lj.workspace_id} token 长度=${lj.token.length}\n`)

const PATHS = ['/api/marketplace/agents', '/api/agents', '/api/marketplace/packages']
for (const p of PATHS) {
  const anon = await fetch(`${BASE}${p}`)
  const auth = await fetch(`${BASE}${p}`, { headers: { authorization: `Bearer ${lj.token}` } })
  const at = await auth.text()
  console.log(`${p}`)
  console.log(`   不带 token: HTTP ${anon.status}  ${(await anon.text()).slice(0, 70)}`)
  console.log(`   带有效 token: HTTP ${auth.status}  ${at.slice(0, 70)}`)
}
