// since 参数的单位一致性检查。
// 前端 maxEmailUpdatedAt() 拿的是本地 updated_at（毫秒），而后端 date 是
// Unix 秒、updatedAt 是毫秒。若后端拿 since 去和「秒」列比，客户端一旦同步过
// 一轮就会永久拉不到新邮件。
const BASE = process.env.POCKET_API || 'http://127.0.0.1:8099'
const PW = process.env.POCKET_PASS

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: PW }),
})
const lj = await login.json()
const H = { Authorization: `Bearer ${lj.token || lj.access_token}` }

const base = await (await fetch(`${BASE}/api/emails?limit=200`, { headers: H })).json()
const ems = base.emails || []
console.log('样本邮件:')
for (const e of ems.slice(0, 3)) {
  console.log(`  ${e.id.slice(0, 12)}  date=${e.date}  updatedAt=${e.updatedAt}`)
}
const maxUpdated = Math.max(...ems.map((e) => e.updatedAt || 0))
const maxDate = Math.max(...ems.map((e) => e.date || 0))
console.log(`\nmax(updatedAt)=${maxUpdated}  max(date)=${maxDate}\n`)

const cases = [
  ['since=0（首次全量）', 0],
  [`since=${maxDate}（秒值，恰好等于最新一封的 date）`, maxDate],
  [`since=${maxUpdated}（毫秒值，本地 updated_at 会传这个）`, maxUpdated],
  [`since=${maxUpdated + 1}`, maxUpdated + 1],
  [`since=${maxDate * 1000}`, maxDate * 1000],
]
for (const [label, v] of cases) {
  const r = await (await fetch(`${BASE}/api/emails?limit=200&since=${v}`, { headers: H })).json()
  const arr = r.emails || []
  console.log(`${label.padEnd(46)} -> ${arr.length} 封`)
}
