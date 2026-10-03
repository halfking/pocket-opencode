#!/usr/bin/env node
/**
 * probe-marketplace-auth.mjs — 判定 /api/marketplace/* 在**带合法 token** 时
 * 到底是 401、404 还是 200。
 *
 * ## 为什么需要这个探针
 *
 * 之前的结论互相矛盾：
 *   - 一轮记录：`/api/marketplace/{agents,installs,router}` 带 token 后返回 404
 *   - 审计方只读探测：无 token 时返回 401
 * 这两条**都可能是对的**，因为它们测的不是同一件事：
 * **认证中间件在路由之前，缺 token 时根本走不到路由**，所以必然是 401；
 * 只有带合法 token 才会暴露「路由是否存在」。
 *
 * 401 绝不能用来论证「端点不存在」—— 这正是本探针要避免的误判。
 *
 * dev 口令从源码常量在**内存中**读取，绝不回显。
 *
 * 用法：node scripts/probe-marketplace-auth.mjs [baseURL]
 */
import http from 'node:http'
import { readFileSync } from 'node:fs'

const BASE = process.argv[2] || '127.0.0.1'
const PORT = 8088

// 内存读取，不打印
const devPass = (readFileSync('backend/internal/server/server_assistant.go', 'utf8')
  .match(/devPass\s*=\s*"([^"]+)"/) || [])[1] || ''

function req(path, token, method = 'GET', body = '') {
  return new Promise((res) => {
    const h = {}
    if (token) h.Authorization = 'Bearer ' + token
    if (body) {
      h['Content-Type'] = 'application/json'
      h['Content-Length'] = Buffer.byteLength(body)
    }
    const r = http.request({ host: BASE, port: PORT, path, method, headers: h }, (resp) => {
      let b = ''
      resp.on('data', (c) => (b += c))
      resp.on('end', () => res({ status: resp.statusCode, body: b }))
    })
    r.on('error', (e) => res({ status: 'ERR', body: e.message }))
    if (body) r.write(body)
    r.end()
  })
}

// 前端 features/marketplace/api.ts 实际调用的端点集合（base='/api/marketplace'）
// —— 只探「前端真的调的那些」。/agents /installs /router /skills 前端零调用，
// 它们的 404 不影响功能（它们是旧契约的残留路径）。
const FRONTEND_GET = [
  '/api/marketplace/packages',
  '/api/marketplace/packages?kind=skill',
  '/api/marketplace/packages?kind=agent',
  '/api/marketplace/releases',
  '/api/marketplace/packages/nonexistent/versions',
]
const FRONTEND_POST = [
  '/api/marketplace/submit',
  '/api/marketplace/review',
  '/api/marketplace/publish',
  '/api/marketplace/install',
  '/api/marketplace/revoke',
  '/api/marketplace/rate',
]
// 无前端调用方的旧契约残留
const ORPHAN_GET = [
  '/api/marketplace/agents',
  '/api/marketplace/installs',
  '/api/marketplace/router',
  '/api/marketplace/skills',
]

const login = await req('/api/auth/login', '', 'POST', JSON.stringify({ username: 'admin', password: devPass }))
let token = ''
try {
  const j = JSON.parse(login.body)
  token = j.token || j.access_token || ''
} catch { /* 下面统一报告 */ }

console.log(`login status = ${login.status}  token 长度 = ${token.length}`)
if (!token) {
  console.log('拿不到 token，本轮无法判定 401/404 —— 记为未验证，不要外推。')
  process.exit(1)
}

function row(r) {
  return `${r.p.padEnd(46)} 无token=${r.noTok}  带token=${r.tok}  ${r.body.slice(0, 60).replace(/\s+/g, ' ')}`
}

console.log('\n=== 前端 GET 端点（功能关键）===')
const getRows = []
for (const p of FRONTEND_GET) {
  const noTok = await req(p, '')
  const tok = await req(p, token)
  getRows.push({ p, noTok: noTok.status, tok: tok.status, body: tok.body })
  console.log(row(getRows[getRows.length - 1]))
}

console.log('\n=== 前端 POST 端点（空 body，只看路由是否注册）===')
const postRows = []
for (const p of FRONTEND_POST) {
  const noTok = await req(p, '', 'POST', '{}')
  const tok = await req(p, token, 'POST', '{}')
  postRows.push({ p, noTok: noTok.status, tok: tok.status, body: tok.body })
  console.log(row(postRows[postRows.length - 1]))
}

console.log('\n=== 无前端调用方的旧路径（404 不影响功能）===')
const orphanRows = []
for (const p of ORPHAN_GET) {
  const tok = await req(p, token)
  orphanRows.push({ p, tok: tok.status })
  console.log(`${p.padEnd(46)} 带token=${tok.status}`)
}

const key = [...getRows, ...postRows]
const bad = key.filter((r) => r.tok === 404 || r.tok === 405)
console.log('\n判读：')
console.log('  带 token 401  -> token 不被该路由接受')
console.log('  带 token 404  -> 路由未注册（真 404）')
console.log('  带 token 405  -> 路径注册了但 method 被拒')
console.log('  带 token 4xx/5xx 其它 -> 路由存在，语义错误（空 body 属预期）')
console.log(`\n前端关键端点中 404/405 的数量: ${bad.length}/${key.length}`)
if (bad.length) console.log('  ' + bad.map((r) => r.p).join('\n  '))
const okCount = key.length - bad.length
console.log(`路由可达: ${okCount}/${key.length}   旧路径 404: ${orphanRows.filter((r) => r.tok === 404).length}/${orphanRows.length}`)
