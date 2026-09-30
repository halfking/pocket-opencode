// probe-options.mjs — 一次性排查：OPTIONS 请求到底由谁返回 200。
// 2026-09-30 BUG-N 回归测试里 "unsupported method still 405" 意外拿到 200。
// 用法：node scripts/probe-options.mjs
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
const { token } = await login.json()

const probes = [
  ['OPTIONS', '/api/notes/n-1', true],
  ['OPTIONS', '/api/notes/n-1', false],
  ['OPTIONS', '/api/tasks/nonexistent-id', true],
  ['OPTIONS', '/api/flashcards/notes', true],
  ['TRACE', '/api/notes/n-1', true],
  ['GET', '/api/notes/n-1', true],
]
for (const [method, path, auth] of probes) {
  const res = await fetch(BASE + path, {
    method,
    headers: auth ? { Authorization: `Bearer ${token}` } : {},
  })
  const allow = res.headers.get('allow') || '-'
  const body = (await res.text()).slice(0, 70).replace(/\s+/g, ' ')
  console.log(`${String(res.status).padEnd(4)} ${method.padEnd(8)} auth=${String(auth).padEnd(5)} ${path.padEnd(28)} Allow=${allow.padEnd(22)} ${body}`)
}
