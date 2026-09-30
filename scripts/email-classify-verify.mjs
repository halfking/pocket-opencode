// 验证网关兜底分类器：在 kxmemory 未配置（kxmemory=false）的条件下，
// /api/emails/classify 应该不再 503，而是真的把邮件归类并落库。
const BASE = process.env.POCKET_API || 'http://127.0.0.1:8098'
const PW = process.env.POCKET_PASS

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: PW }),
})
const lj = await login.json()
if (!login.ok) { console.log('登录失败', login.status); process.exit(1) }
const H = { Authorization: `Bearer ${lj.token || lj.access_token}`, 'Content-Type': 'application/json' }

const before = await (await fetch(`${BASE}/api/emails?limit=50`, { headers: H })).json()
const ems = before.emails || []
console.log(`待归类邮件 ${ems.length} 封，归类前：`)
for (const e of ems) console.log(`  ${(e.subject || '(空)').slice(0, 24).padEnd(26)} category=${JSON.stringify(e.category)} importance=${JSON.stringify(e.importance)}`)

const t0 = Date.now()
const r = await fetch(`${BASE}/api/emails/classify`, {
  method: 'POST', headers: H, body: JSON.stringify({ limit: 20 }),
})
const j = await r.json().catch(() => ({}))
console.log(`\nPOST /api/emails/classify -> ${r.status}  耗时 ${((Date.now() - t0) / 1000).toFixed(1)}s`)
if (r.status !== 200) {
  console.log('响应体 =', JSON.stringify(j).slice(0, 300))
  process.exit(1)
}
console.log(`classified=${j.classified}  remaining=${j.remaining}`)
for (const row of (j.results || [])) {
  console.log(`  ${(row.emailId || '').slice(0, 16).padEnd(18)} cat=${String(row.category).padEnd(13)} imp=${String(row.importance).padEnd(7)} sum=${JSON.stringify(row.summary || '')} ${row.error ? 'ERR=' + row.error : ''}`)
}

const after = await (await fetch(`${BASE}/api/emails?limit=50`, { headers: H })).json()
console.log('\n归类后（确认真的落库了，不只是接口返回）：')
for (const e of (after.emails || [])) {
  console.log(`  ${(e.subject || '(空)').slice(0, 24).padEnd(26)} category=${String(e.category).padEnd(13)} importance=${String(e.importance).padEnd(7)} summary=${JSON.stringify((e.aiSummary || '').slice(0, 30))}`)
}
const classified = (after.emails || []).filter((e) => e.category).length
console.log(`\n=> 已归类 ${classified}/${(after.emails || []).length} 封`)
process.exit(classified > 0 ? 0 : 2)
