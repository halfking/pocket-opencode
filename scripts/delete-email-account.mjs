// 删掉指定邮箱账户（只删我们自己库里的配置行，不动真实邮箱内容）。
// 用于排查凭证解密失败：同一地址可能是别的实例用不同 master key 写进去的。
const BASE = process.env.POCKET_API || 'http://127.0.0.1:8099'
const TARGET = process.argv[2]
if (!TARGET) { console.log('用法: node scripts/delete-email-account.mjs <email> [imapHost]'); process.exit(2) }
const host = process.argv[3] || ''

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: process.env.POCKET_PASS }),
})
const lj = await login.json()
if (!login.ok) { console.log('登录失败', lj.status); process.exit(1) }
const H = { Authorization: `Bearer ${lj.token || lj.access_token}` }

const accs = (await (await fetch(`${BASE}/api/email/accounts`, { headers: H })).json()).accounts || []
const hit = accs.filter((a) => a.emailAddress === TARGET && (!host || a.imapHost === host))
console.log(`匹配 ${hit.length} 个账户`)
for (const a of hit) console.log(`  ${a.id}  ${a.emailAddress} @ ${a.imapHost}:${a.imapPort}`)
for (const a of hit) {
  const r = await fetch(`${BASE}/api/email/accounts/${a.id}`, { method: 'DELETE', headers: H })
  console.log(`DELETE ${a.id} → ${r.status}`)
}
