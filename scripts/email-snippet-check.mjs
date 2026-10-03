// 检查列表摘要是否还把 MIME 头当正文显示。
// 这是我 fixture 的 textSnippet 缺陷（单段邮件没有结束边界）留下的症状。
const BASE = process.env.POCKET_API || 'http://127.0.0.1:8098'
const PW = process.env.POCKET_PASS
const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: PW }),
})
const lj = await login.json()
const H = { Authorization: `Bearer ${lj.token || lj.access_token}` }
const j = await (await fetch(`${BASE}/api/emails?limit=50`, { headers: H })).json()
const ems = j.emails || []
console.log(`邮件 ${ems.length} 封\n`)
let bad = 0
for (const e of ems) {
  const s = (e.snippet || '').replace(/\s+/g, ' ').trim()
  const leaksHeader = /^(MIME-Version|Content-Type|Content-Transfer-Encoding)\s*:/i.test(s)
  if (leaksHeader) bad++
  console.log(`${leaksHeader ? '[泄漏]' : '[ OK ]'} ${(e.subject || '(空)').slice(0, 24).padEnd(26)} snippet=${JSON.stringify(s.slice(0, 70))}`)
}
console.log(`\n=> ${bad} 封摘要泄漏了 MIME 头`)
process.exit(bad ? 1 : 0)
