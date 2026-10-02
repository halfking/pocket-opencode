// jwt-secret-drift.mjs — 检出手上跑着的多个 pocketd 用了**不同的 JWT 密钥**。
//
// 真机事故（2026-10-03）：真机 App 的 localStorage 指向 127.0.0.1:18099
// （adb reverse 映射到主机 18099），healthz 200、一切"通"，
// 但 App 存的 token 在 18099 上是 401 invalid or expired。
//
// 排查掉的是：token 没过期（exp 还在 14 小时后），是**两个后端的签名密钥
// 不同**——18099 那个裸跑 config.go 的 DevDefaultJWTSecret，
// 而 App 的会话是用 start-local-backend.ps1 的 $JwtSecret 签的。
//
// 为什么值得做成护栏：healthz 200 **完全掩盖**这件事。症状是
// "登录态莫名失效"，真因在两台进程的启动环境里。任何只探 healthz 的
// 真机装置都会报绿。
//
// 用法：
//   node scripts/jwt-secret-drift.mjs 18099 18100 8088
//   不给端口则扫 18099 18100 18101 8088
//
// 退出码：0 = 全部一致或只有一个；1 = 发现漂移；2 = 端口不可达。

import { createHmac } from 'node:crypto'

// 与 backend/internal/config/config.go:17 一致
const DEV_DEFAULT = 'pocket-dev-insecure-secret-0000000000'
// 与 scripts/start-local-backend.ps1:29 的默认 $JwtSecret 一致
const LOCAL_DEV = 'pocket-local-dev-jwt-secret-do-not-use-in-shared-env'

const ports = process.argv.slice(2).length
  ? process.argv.slice(2).map(Number)
  : [18099, 18100, 18101, 8088]

function b64url(o) {
  return Buffer.from(JSON.stringify(o)).toString('base64url')
}

function mint(secret) {
  const now = Math.floor(Date.now() / 1000)
  const h = b64url({ alg: 'HS256', typ: 'JWT' })
  const p = b64url({
    user_id: 'user-admin',
    role: 'admin',
    workspace_id: 'ws_user-admin',
    iss: 'pocket',
    aud: ['pocket-api'],
    exp: now + 3600,
    nbf: now - 60,
    iat: now
  })
  const s = createHmac('sha256', secret).update(`${h}.${p}`).digest('base64url')
  return `${h}.${p}.${s}`
}

async function probe(port, secret) {
  const r = await fetch(`http://127.0.0.1:${port}/api/notifications?limit=1`, {
    headers: { Authorization: `Bearer ${mint(secret)}` },
    signal: AbortSignal.timeout(6000)
  })
  await r.text()
  return r.status
}

const rows = []
for (const port of ports) {
  const verdicts = {}
  let reachable = false
  for (const [name, secret] of [['local-dev', LOCAL_DEV], ['dev-default', DEV_DEFAULT]]) {
    try {
      verdicts[name] = await probe(port, secret)
      if (verdicts[name] !== 0) reachable = true
    } catch {
      verdicts[name] = 0 // unreachable
    }
  }
  if (!reachable) {
    console.log(`:${port} not reachable (skipped)`)
    continue
  }
  const accepted = Object.entries(verdicts).filter(([, s]) => s === 200).map(([n]) => n)
  rows.push({ port, verdicts, accepted })
  console.log(`:${port}  local-dev=${verdicts['local-dev']}  dev-default=${verdicts['dev-default']}  -> accepts: ${accepted.join(',') || '(none)'}`)
}

if (rows.length < 2) {
  console.log('OK: fewer than two reachable backends, nothing to compare')
  process.exit(0)
}

const signatures = new Set(rows.map((r) => r.accepted.sort().join('+')))
if (signatures.size > 1) {
  console.log('')
  console.log('DRIFT DETECTED: reachable backends accept DIFFERENT JWT secrets.')
  console.log('A device whose token was issued by one of them will get 401 on the others,')
  console.log('while /healthz stays 200 on all of them. Restart the drifted backend via')
  console.log('scripts/start-local-backend.ps1 so every instance uses the same $JwtSecret.')
  process.exit(1)
}

console.log('')
console.log(`OK: all ${rows.length} reachable backends accept the same secret set`)
process.exit(0)
