// 数据一致性检查：邮件的 accountId 是否都能在账户列表里找到。
// upsertEmail 把邮件写进本地 SQLCipher 时带 account_id 外键；若
// syncAccountsFromServer 没把对应账户写进 local_email_accounts，插入会
// 因外键失败被 upsertEmail 的裸 catch 吞掉（return false），列表就永远是空的
// 且没有任何用户可见报错。
const BASE = process.env.POCKET_API || 'http://127.0.0.1:8099'
const PW = process.env.POCKET_PASS

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: PW }),
})
const lj = await login.json()
if (!login.ok) { console.log('登录失败', login.status, JSON.stringify(lj).slice(0, 200)); process.exit(1) }
const H = { Authorization: `Bearer ${lj.token || lj.access_token}` }

const accRes = await (await fetch(`${BASE}/api/email/accounts`, { headers: H })).json()
const accs = accRes.accounts || accRes.items || accRes.data || []
console.log('账户数 =', Array.isArray(accs) ? accs.length : JSON.stringify(accRes).slice(0, 200))
if (Array.isArray(accs)) {
  for (const a of accs) {
    console.log(`  ${a.id}  ${a.emailAddress}  enabled=${a.enabled} ws=${a.workspaceId || a.workspace_id || '?'}`)
  }
}

const emRes = await (await fetch(`${BASE}/api/emails?limit=200`, { headers: H })).json()
const ems = emRes.emails || []
console.log('\n邮件数 =', ems.length)

const accIds = new Set((Array.isArray(accs) ? accs : []).map((a) => a.id))
const orphan = []
for (const e of ems) {
  if (!accIds.has(e.accountId)) orphan.push({ id: e.id, accountId: e.accountId, subject: (e.subject || '').slice(0, 20) })
}
console.log('\n孤儿邮件（accountId 在账户列表里找不到）=', orphan.length)
for (const o of orphan) console.log('  ', JSON.stringify(o))

// 每封邮件被 upsert 时会用到的字段，逐一检查有没有 null/undefined
// ——本地表这些列大多是 NOT NULL，任一为 null 都会让 INSERT 失败并被静默吞掉
console.log('\n字段完整性（本地 INSERT 需要的列）:')
const need = ['id', 'accountId', 'fromAddress', 'date']
for (const e of ems) {
  const bad = need.filter((k) => e[k] === null || e[k] === undefined)
  if (bad.length) console.log(`  ${e.id} 缺: ${bad.join(',')}`)
}
console.log('  日期样本:', ems.slice(0, 3).map((e) => `${e.id.slice(0, 10)}=${e.date}`).join(' '))
console.log('\nupdatedAt 样本:', ems.slice(0, 3).map((e) => e.updatedAt).join(' '))
