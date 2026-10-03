// 清理审计遗留：删掉所有 *.local 桩账户（它们会被 isLocalTestAddress 跳过
// 镜像写入，留在库里只会让收件箱出现无法落地的孤儿邮件），只保留 *.test 账户。
const BASE = process.env.POCKET_API || 'http://127.0.0.1:8099'
const PW = process.env.POCKET_PASS
const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: PW }),
})
const lj = await login.json()
const H = { Authorization: `Bearer ${lj.token || lj.access_token}`, 'Content-Type': 'application/json' }

const accs = await (await fetch(`${BASE}/api/email/accounts`, { headers: H })).json()
for (const a of accs.accounts || []) {
  if (a.emailAddress.endsWith('.local')) {
    const r = await fetch(`${BASE}/api/email/accounts/${a.id}`, { method: 'DELETE', headers: H })
    console.log(`删除 .local 账户 ${a.emailAddress} (${a.id}) -> ${r.status}`)
  }
}
// 顺带删掉并行会话留下的 DNS 不可达测试账户，减少日志噪声
for (const a of accs.accounts || []) {
  if (/^(probe\d+|uidemo\d+)@/.test(a.emailAddress)) {
    const r = await fetch(`${BASE}/api/email/accounts/${a.id}`, { method: 'DELETE', headers: H })
    console.log(`删除失效测试账户 ${a.emailAddress} -> ${r.status}`)
  }
}
const after = await (await fetch(`${BASE}/api/email/accounts`, { headers: H })).json()
console.log('剩余账户:', (after.accounts || []).map((a) => a.emailAddress).join(', ') || '(无)')
const ems = await (await fetch(`${BASE}/api/emails?limit=200`, { headers: H })).json()
console.log('邮件数:', (ems.emails || []).length)
