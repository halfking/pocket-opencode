// 读真机 WebView leveldb 里存的 pocket_token，打各个后端端口，报告状态。
// 写文件而不是 node -e：PowerShell 会吞掉内联脚本里的正则转义（本轮又踩一次）。
import { readFileSync } from 'node:fs'

const dumpPath = process.argv[2]
const ports = process.argv.slice(3).map(Number)
if (!ports.length) {
  console.error('usage: node check-device-token.mjs <dumpfile> <port...>')
  process.exit(1)
}

const lines = readFileSync(dumpPath, 'latin1').split(/\r?\n/)
const RE = /^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/

let token = null
outer: for (let i = 0; i < lines.length; i++) {
  if (!lines[i].includes('pocket_token')) continue
  for (let j = i; j < Math.min(i + 4, lines.length); j++) {
    const v = lines[j].trim()
    if (RE.test(v)) { token = v; break outer }
  }
}

if (!token) {
  console.log('TOKEN NOT FOUND in dump (App may be logged out)')
  process.exit(2)
}

const payload = JSON.parse(Buffer.from(token.split('.')[1], 'base64url'))
console.log(`device token: ws=${payload.workspace_id} exp=${new Date(payload.exp * 1000).toISOString()}`)

let anyOk = false
for (const port of ports) {
  try {
    const r = await fetch(`http://127.0.0.1:${port}/api/notifications?limit=200`, {
      headers: { Authorization: `Bearer ${token}` }
    })
    const body = await r.text()
    let n = 'n/a'
    try {
      const b = JSON.parse(body)
      if (Array.isArray(b.notifications)) n = b.notifications.length
    } catch {}
    console.log(`  :${port} -> ${r.status} count=${n}`)
    if (r.status === 200) anyOk = true
  } catch (e) {
    console.log(`  :${port} -> ERR ${e.message}`)
  }
}
process.exit(anyOk ? 0 : 1)
