#!/usr/bin/env node
// 宿主侧 WebSocket 端到端回归：登录取 token -> ws://localhost:8088/ws 握手 -> 收一帧
//
// 用途：把「后端 /ws 鉴权 + 升级」与「设备 WebView 网络」彻底解耦。
// 若本脚本 OPEN，说明后端 WS 链路完好；真机侧失败只剩网络/构建配置问题。
//
// 凭据处理：dev 口令从后端源码读取，只在内存使用，不打印、不落盘。
import { readFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..')
const BASE = process.env.POCKET_BASE || 'http://localhost:8088'

const src = readFileSync(join(ROOT, 'backend/internal/server/server_assistant.go'), 'utf8')
const pass = (src.match(/devPass\s*=\s*"([^"]+)"/) || [])[1]
if (!pass) { console.error('CANNOT_READ_DEV_PASS'); process.exit(3) }

const loginRes = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: process.env.POCKET_DEV_USER || 'admin', password: pass }),
})
if (!loginRes.ok) { console.error('LOGIN_FAIL', loginRes.status, await loginRes.text()); process.exit(1) }
const { token, user } = await loginRes.json()
console.log(`login ok: user=${user} tokenLen=${token.length}`)

const meRes = await fetch(`${BASE}/api/auth/me`, { headers: { Authorization: `Bearer ${token}` } })
console.log(`GET /api/auth/me -> ${meRes.status} ${meRes.status === 200 ? 'OK' : await meRes.text()}`)

const wsUrl = `${BASE.replace(/^http/, 'ws')}/ws?token=${encodeURIComponent(token)}`
console.log('opening', wsUrl.replace(token, '<redacted>'))

const verdict = await new Promise((resolve) => {
  const ws = new WebSocket(wsUrl)
  const t = setTimeout(() => { try { ws.close() } catch {} ; resolve('TIMEOUT(6s)') }, 6000)
  ws.addEventListener('open', () => {
    console.log('WS OPEN (handshake 101)')
    // 发一个 ping 帧看是否有回包
    try { ws.send(JSON.stringify({ type: 'ping', payload: {} })) } catch {}
    setTimeout(() => { clearTimeout(t); try { ws.close() } catch {} ; resolve('OPEN') }, 1200)
  })
  ws.addEventListener('message', (ev) => console.log('WS FRAME:', String(ev.data).slice(0, 160)))
  ws.addEventListener('error', (e) => console.log('WS ERROR event:', e.message || '(opaque)'))
  ws.addEventListener('close', (e) => {
    clearTimeout(t)
    if (verdict === 'TIMEOUT(6s)') return
    resolve(`CLOSED code=${e.code} reason=${e.reason || '-'}`)
  })
})

console.log('VERDICT =', verdict)
process.exit(verdict === 'OPEN' ? 0 : 4)
