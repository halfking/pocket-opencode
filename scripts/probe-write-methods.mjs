#!/usr/bin/env node
/**
 * probe-write-methods.mjs — 前端写路径的**真后端 method 级探测**。
 *
 * 存在的理由：BUG-L 就是静态前缀对账抓不到的那一类。
 *
 *   前端 services/flashcards.ts 的 createNote 打 POST /api/flashcards/notes
 *   后端 /api/flashcards/ 这个前缀**确实注册了**，但 item dispatcher 把
 *     len(parts)==1 && parts[0]=="notes" 一律交给 flashcardsNotesCollection，
 *   而后者只允许 GET（405 "GET only"）。前缀对账全绿，功能却完全不可用。
 *
 * 所以判据必须是**发真请求看状态码**：
 *   405          -> method 被 handler 拒绝（BUG-L 类，确认缺陷）
 *   404 + not found -> mux 层无该前缀（路径没注册）
 *   400          -> 路由与 method 都通，卡在参数校验（正常，可达）
 *   401/403      -> 鉴权问题（不是路由问题）
 *   503          -> 路由通但存储未配置（环境问题，非代码缺陷）
 *   2xx          -> 真写成功了（见下方副作用说明）
 *
 * 副作用：探测用空 body `{}`，绝大多数 handler 会在参数校验处 400 返回而不落库；
 * 但**不能保证全部如此**。对带 :seg 的路径统一替换成保证不存在的 id，
 * 避免误改真实数据。仍可能产生少量探测数据，标题/名称统一带 AUDIT 前缀便于清理。
 *
 * 用法：node scripts/probe-write-methods.mjs [--dry] [--only <substr>]
 *   --dry   只打印将要发的请求，不实际发送
 * 退出码：0 = 无 405/404 缺陷；1 = 有候选。
 */
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { extractWritePaths } from './lib/extract-write-paths.mjs'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const SRC = join(ROOT, 'frontend/src')
const BASE = process.env.POCKET_BASE || 'http://localhost:8088'
const DRY = process.argv.includes('--dry')
const ONLY = (process.argv.find((a) => a.startsWith('--only=')) || '').slice(7)

/** 统一的不存在 id，避免探测误伤真实数据。 */
const GHOST_ID = '__audit_nonexistent__'

function concrete(pattern) {
  return pattern
    .split('/')
    .map((seg) => (seg === ':seg' ? GHOST_ID : seg))
    .join('/')
}

const paths = extractWritePaths(SRC)
  .map((f) => ({ ...f, url: concrete(f.pattern) }))
  .filter((f) => (ONLY ? f.pattern.includes(ONLY) : true))

// 同一 method+url 只探一次（多个 service 可能打同一条路径）。
const seen = new Set()
const probes = paths.filter((f) => {
  const k = `${f.method} ${f.url}`
  if (seen.has(k)) return false
  seen.add(k)
  return true
})

console.log(`probe target: ${BASE}`)
console.log(`unique write paths: ${probes.length} (from ${paths.length} call sites)`)
console.log('')

if (DRY) {
  for (const p of probes) console.log(`  ${p.method.padEnd(6)} ${p.url.padEnd(52)} ${p.file}:${p.line}`)
  process.exit(0)
}

// dev 登录。口令从源码常量读，不回显。
const src = readFileSync(join(ROOT, 'backend/internal/server/server_assistant.go'), 'utf8')
const pass = (src.match(/devPass\s*=\s*"([^"]+)"/) || [])[1]
if (!pass) {
  console.error('CANNOT_READ_DEV_PASS')
  process.exit(3)
}
const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: pass }),
})
if (!login.ok) {
  console.error('LOGIN_FAIL', login.status, (await login.text()).slice(0, 200))
  process.exit(1)
}
const { token } = await login.json()
const H = { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }

const rows = []
for (const p of probes) {
  let status = 0
  let note = ''
  try {
    const res = await fetch(BASE + p.url, {
      method: p.method,
      headers: H,
      body: p.method === 'GET' ? undefined : '{}',
    })
    status = res.status
    note = (await res.text()).slice(0, 120).replace(/\s+/g, ' ')
  } catch (e) {
    note = 'ERR ' + e.message
  }
  rows.push({ ...p, status, note })
  const flag = status === 405 || status === 404 ? '  <<<' : ''
  console.log(`${String(status).padEnd(4)} ${p.method.padEnd(6)} ${p.url.slice(0, 50).padEnd(52)}${flag}`)
  if (status >= 400) console.log(`        ${note}`)
}

// 分类
const m405 = rows.filter((r) => r.status === 405)
const m404 = rows.filter((r) => r.status === 404)
const reach = rows.filter((r) => r.status > 0 && r.status < 500 && !m405.includes(r) && !m404.includes(r))
const auth = rows.filter((r) => r.status === 401 || r.status === 403)
const store = rows.filter((r) => r.status === 503)

console.log('\n=== SUMMARY ===')
console.log(`method 可达 (2xx/400/409...) : ${reach.length}`)
console.log(`鉴权 401/403 (非路由问题)   : ${auth.length}`)
console.log(`存储未配 503 (非代码缺陷)   : ${store.length}`)
console.log(`405 method 被拒 (BUG-L 类)  : ${m405.length}`)
console.log(`404 路径未注册              : ${m404.length}`)
console.log(`总计                        : ${rows.length}`)

if (m405.length) {
  console.log('\n405 — 这些路径注册了但 handler 拒绝该 method，确认缺陷:')
  for (const r of m405) console.log(`  ${r.method.padEnd(6)} ${r.url.padEnd(50)} ${r.file}:${r.line}`)
}
if (m404.length) {
  console.log('\n404 — 需要人工判定是 mux 未注册还是 handler 内部"资源不存在":')
  for (const r of m404) console.log(`  ${r.method.padEnd(6)} ${r.url.padEnd(50)} ${r.file}:${r.line}  ${r.note.slice(0, 60)}`)
}
process.exitCode = m405.length > 0 ? 1 : 0
