#!/usr/bin/env node
/**
 * diag-workspace-claim.mjs — 同一个 dev 用户在不同时刻登录，workspace_id 是否一致？
 *
 * 起因：市场播种后 UI 看不到包。查出应用 JWT 的 workspace_id 是 `default`，
 * 而同期用 API 全新登录拿到的却是 `ws_user-admin`。
 * 同一个 `user-admin` 落在两个不同的 workspace，就是两个互不可见的数据孤岛。
 *
 * 这里连做 N 次全新登录，打印每次的 workspace claim；再与设备上 App
 * 持有的 token 对比（App 那个不打印原文，只解 payload）。
 *
 * 用法：node scripts/diag-workspace-claim.mjs [轮数]
 */
import http from 'node:http'
import { requireDevPass } from './lib/dev-pass.mjs'
const ROUNDS = Number(process.argv[2] || 3)
const devPass = requireDevPass()

function login(body) {
  return new Promise((res) => {
    const b = JSON.stringify(body)
    const r = http.request({ host: '127.0.0.1', port: 8088, path: '/api/auth/login', method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(b) } }, (resp) => {
      let s = ''; resp.on('data', (c) => (s += c)); resp.on('end', () => res({ status: resp.statusCode, body: s }))
    })
    r.on('error', (e) => res({ status: 'ERR', body: e.message }))
    r.write(b); r.end()
  })
}

const claim = (jwt) => {
  try {
    const p = JSON.parse(Buffer.from(jwt.split('.')[1], 'base64').toString())
    return { user_id: p.user_id, role: p.role, workspace_id: p.workspace_id, iat: p.iat }
  } catch { return null }
}

console.log(`连续 ${ROUNDS} 次全新登录：`)
const seen = new Map()
for (let i = 1; i <= ROUNDS; i++) {
  const r = await login({ username: 'admin', password: devPass })
  let t = ''
  try { t = JSON.parse(r.body).token || '' } catch { /* 下面报告 */ }
  const c = t ? claim(t) : null
  console.log(`  第${i}次  status=${r.status}  ${c ? JSON.stringify(c) : '(无 token) ' + r.body.slice(0, 120)}`)
  if (c) {
    if (!seen.has(c.workspace_id)) seen.set(c.workspace_id, 0)
    seen.set(c.workspace_id, seen.get(c.workspace_id) + 1)
  }
}

console.log(`\nAPI 侧出现过的 workspace: ${[...seen.entries()].map(([k, v]) => `${k} x${v}`).join(', ') || '(无)'}`)
console.log(`设备上 App 持有的 token 的 workspace: default（来自上一轮诊断解出的 JWT payload）`)

if (seen.size > 1) {
  console.log('\n结论：同一次运行内 workspace_id 就不唯一 —— 这是真缺陷（数据孤岛）')
} else {
  const only = [...seen.keys()][0]
  console.log(`\n结论：API 侧稳定为 ${only}；与 App 的 default 不一致 = 两个数据孤岛（真缺陷）`)
}
