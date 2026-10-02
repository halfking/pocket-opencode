#!/usr/bin/env node
/**
 * verify-bug-z.mjs — 验证 BUG-Z：重复提交同名同版本返回 **409**，不再是 500。
 *
 * ## 缺陷
 *
 * 同一 workspace 内对**同名包**重复提交**同一版本号**，会撞
 * `marketplace_versions_pkey` 唯一约束。修之前原始 pgx 错误一路冒泡到
 * server 层 `writeMarketplaceError` 的 default 分支，被写成
 * **500 Internal Server Error**。
 *
 * 但这是客户端重复提交造成的（换个版本号就能继续），不该被当成服务端故障 ——
 * 而且前端会把 5xx 当成可重试**反复重试**。与已修的 BUG-M 同一类。
 *
 * ## 三段判据
 *
 *   1. 首次 submit → 201
 *   2. **重复** submit（同名同版本）→ **409**（修前是 500）
 *   3. 对照：**换版本号** submit → 仍 201
 *
 * 第 3 条是必需的对照：没有它，一个「只要包已存在就返回 409」的错误实现
 * 也能让第 2 条通过。
 *
 * 另外校验错误文案里**不再泄漏** `23505` / `duplicate key` 原始串。
 *
 * 用法：node scripts/verify-bug-z.mjs
 */
import http from 'node:http'
import { requireDevPass } from './lib/dev-pass.mjs'
const HOST = '127.0.0.1'
const PORT = 8088

const devPass = requireDevPass()

function api(path, token, method = 'GET', body) {
  return new Promise((res) => {
    const payload = body ? JSON.stringify(body) : ''
    const h = {}
    if (token) h.Authorization = 'Bearer ' + token
    if (payload) {
      h['Content-Type'] = 'application/json'
      h['Content-Length'] = Buffer.byteLength(payload)
    }
    const r = http.request({ host: HOST, port: PORT, path, method, headers: h }, (resp) => {
      let s = ''
      resp.on('data', (c) => (s += c))
      resp.on('end', () => res({ status: resp.statusCode, body: s }))
    })
    r.on('error', (e) => res({ status: 'ERR', body: e.message }))
    if (payload) r.write(payload)
    r.end()
  })
}

const checks = []
const check = (name, pass, detail) => {
  checks.push({ name, pass, detail })
  console.log(`${pass ? 'PASS' : 'FAIL'}  ${name}${detail ? '  — ' + detail : ''}`)
}

const login = await api('/api/auth/login', '', 'POST', { username: 'admin', password: devPass })
let token = ''
try { token = JSON.parse(login.body).token || '' } catch { /* 下面报告 */ }
if (!token) {
  console.log(`登录失败 status=${login.status} body=${login.body.slice(0, 160)}`)
  process.exit(1)
}

const name = `BUGZ 冲突验证 ${Date.now()}`
const base = { name, kind: 'skill', version: '9.9.9', digest: 'sha256:z1' }

const first = await api('/api/marketplace/submit', token, 'POST', base)
check('首次 submit 返回 201', first.status === 201, `status=${first.status} ${first.body.slice(0, 110)}`)

const dup = await api('/api/marketplace/submit', token, 'POST', base)
check('重复 submit（同名同版本）返回 409 而非 500', dup.status === 409,
  `status=${dup.status} ${dup.body.slice(0, 140)}`)
check('错误文案不泄漏原始 pgx 串（23505 / duplicate key）',
  !/23505|duplicate key/i.test(dup.body), `body=${dup.body.slice(0, 140)}`)

const next = { ...base, version: '9.9.10', digest: 'sha256:z2' }
const okNext = await api('/api/marketplace/submit', token, 'POST', next)
check('对照组：换版本号仍返回 201（409 只针对真正的重复）', okNext.status === 201,
  `status=${okNext.status} ${okNext.body.slice(0, 110)}`)

console.log('\n=== 汇总 ===')
const passed = checks.filter((c) => c.pass).length
console.log(`${passed}/${checks.length} 通过`)
checks.filter((c) => !c.pass).forEach((c) => console.log(`  FAIL: ${c.name} — ${c.detail || ''}`))
process.exit(passed === checks.length ? 0 : 1)
