#!/usr/bin/env node
/**
 * api-call.mjs — 带自动登录的通用后端调用器。
 *
 * 反复用内联 `node -e` 探 API 会踩两个坑：PowerShell 5.1 会把 JS 里的
 * `[...]` 当成 PowerShell 语法解析；dev 口令又绝不能出现在命令行里
 * （会进 PSReadLine 历史）。这个脚本两个都解决：口令在**进程内**从源码
 * 常量读取，不经过命令行、不回显。
 *
 * 用法：
 *   node scripts/api-call.mjs GET  /api/flashcards
 *   node scripts/api-call.mjs POST /api/flashcards/decks '{"name":"x"}'
 *   node scripts/api-call.mjs GET  /api/flashcards "" --noauth
 */
import http from 'node:http'
import { requireDevPass } from './lib/dev-pass.mjs'
const HOST = process.env.POCKET_API_HOST || '127.0.0.1'
const PORT = Number(process.env.POCKET_API_PORT || 8088)

const [method = 'GET', path = '/', body = '', ...flags] = process.argv.slice(2)
const noauth = flags.includes('--noauth')

function req(p, token, m = 'GET', b = '') {
  return new Promise((res) => {
    const h = {}
    if (token) h.Authorization = 'Bearer ' + token
    if (b) {
      h['Content-Type'] = 'application/json'
      h['Content-Length'] = Buffer.byteLength(b)
    }
    const r = http.request({ host: HOST, port: PORT, path: p, method: m, headers: h }, (resp) => {
      let s = ''
      resp.on('data', (c) => (s += c))
      resp.on('end', () => res({ status: resp.statusCode, body: s }))
    })
    r.on('error', (e) => res({ status: 'ERR', body: e.message }))
    if (b) r.write(b)
    r.end()
  })
}

let token = ''
if (!noauth) {
  const devPass = requireDevPass()
  const login = await req('/api/auth/login', '', 'POST', JSON.stringify({ username: 'admin', password: devPass }))
  try {
    token = JSON.parse(login.body).token || ''
  } catch {
    /* 下面统一报告 */
  }
  if (!token) {
    console.error(`登录失败 status=${login.status} body=${login.body.slice(0, 200)}`)
    process.exit(1)
  }
}

const r = await req(path, token, method.toUpperCase(), body)
let pretty = r.body
try {
  pretty = JSON.stringify(JSON.parse(r.body))
} catch { /* 非 JSON 原样输出 */ }
console.log(`${method.toUpperCase()} ${path} -> ${r.status}`)
console.log(pretty.length > 3000 ? pretty.slice(0, 3000) + ` …(+${pretty.length - 3000} 字符)` : pretty)
