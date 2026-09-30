// 手动抽取端点探针：为什么流水线没给「对账单」建发票档？
const BASE = process.env.POCKET_API || 'http://127.0.0.1:8099'
const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: process.env.POCKET_PASS }),
})
const lj = await login.json()
const H = { Authorization: `Bearer ${lj.token || lj.access_token}`, 'Content-Type': 'application/json' }

const list = await (await fetch(`${BASE}/api/emails?limit=30`, { headers: H })).json()
const emails = list.emails || []
console.log(`邮件 ${emails.length} 封`)
for (const e of emails) {
  console.log(`  ${e.id}  uid=${e.uid ?? '-'}  date=${e.date}  ${e.subject || '(无主题)'}  snip=${(e.snippet || '').slice(0, 30)}`)
}
const target = emails.find((e) => /对账单|账单|发票|invoice/i.test(e.subject || ''))
if (!target) { console.log('\n没有账单/发票类主题邮件'); process.exit(0) }
console.log(`\n对目标邮件走手动抽取：${target.id} (${target.subject})`)
const r = await fetch(`${BASE}/api/emails/invoices/extract`, {
  method: 'POST', headers: H, body: JSON.stringify({ emailId: target.id }),
})
const body = await r.json().catch(() => ({}))
console.log(`HTTP ${r.status}`)
console.log(JSON.stringify(body, null, 2).slice(0, 1500))

const inv = await (await fetch(`${BASE}/api/emails/invoices?limit=20`, { headers: H })).json()
console.log(`\n发票列表 ${(inv.invoices || []).length} 条`)
for (const i of inv.invoices || []) {
  console.log(`  ${i.status}  ${i.fileName || '(无文件)'}  amount=${i.amount} seller=${i.seller} src=${i.fileSource || '-'}`)
}
