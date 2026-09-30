// 找出新夹具邮件（增值税发票）在库里长什么样：为什么没有主题？
const BASE = process.env.POCKET_API || 'http://127.0.0.1:8099'
const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: process.env.POCKET_PASS }),
})
const lj = await login.json()
const H = { Authorization: `Bearer ${lj.token || lj.access_token}` }
const list = await (await fetch(`${BASE}/api/emails?limit=30`, { headers: H })).json()
for (const e of list.emails || []) {
  const raw = await (await fetch(`${BASE}/api/emails/${e.id}/body`, { headers: H })).json().catch(() => ({}))
  const body = String(raw.body || '')
  console.log('─'.repeat(70))
  console.log(`id=${e.id} date=${e.date} hasAtt=${e.hasAttachments}`)
  console.log(`subject=${JSON.stringify(e.subject)}`)
  console.log(`snippet=${JSON.stringify((e.snippet || '').slice(0, 60))}`)
  console.log(`bodyLen=${body.length} bodyHead=${JSON.stringify(body.slice(0, 90))}`)
  const hasPdf = body.includes('application/pdf')
  console.log(`bodyMentionsPDF=${hasPdf} bodyMentionsInvoiceNo=${/25332000000123456789/.test(body)}`)
}
