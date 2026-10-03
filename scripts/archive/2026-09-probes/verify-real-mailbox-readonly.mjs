// 真实邮箱**只读**连通性验证：建账户 → 只调同步接口（不跑流水线）。
//
// 为什么不跑流水线：流水线的第 2 步会把广告/垃圾邮件 MOVE 进真实邮箱的垃圾箱，
// 那是不可逆的真邮箱写操作。本脚本只做 IMAP 拉取（读），外加我们自己的落库。
// 凭证从环境变量读，不写进代码、不打日志。
//
// 用法：
//   $env:IMAP_USER='huangxutao@kxpms.cn'; $env:IMAP_PASS='...'; node scripts/verify-real-mailbox-readonly.mjs
const BASE = process.env.POCKET_API || 'http://127.0.0.1:8099'
const USER = process.env.IMAP_USER
const PASS = process.env.IMAP_PASS
const HOST = process.env.IMAP_HOST || 'imap.exmail.qq.com'
const PORT = Number(process.env.IMAP_PORT || 993)
const LABEL = process.env.IMAP_LABEL || 'real-mailbox'
const WORKSPACE = process.env.IMAP_WORKSPACE || ''

if (!USER || !PASS) { console.log('缺少 IMAP_USER / IMAP_PASS'); process.exit(2) }

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: process.env.POCKET_PASS }),
})
const lj = await login.json()
if (!login.ok) { console.log('登录失败', login.status); process.exit(1) }
const H = { Authorization: `Bearer ${lj.token || lj.access_token}`, 'Content-Type': 'application/json' }
let pass = 0, fail = 0
const check = (n, ok, d = '') => { console.log(`${ok ? 'PASS' : 'FAIL'}  ${n}${d ? '  → ' + d : ''}`); ok ? pass++ : fail++ }

// 1) 建账户（已存在就复用，不重复播种）
const accs = (await (await fetch(`${BASE}/api/email/accounts`, { headers: H })).json()).accounts || []
let acct = accs.find((a) => a.emailAddress === USER && a.imapHost === HOST)
if (!acct) {
  const r = await fetch(`${BASE}/api/email/accounts`, {
    method: 'POST', headers: H,
    body: JSON.stringify({
      displayName: LABEL, emailAddress: USER, imapHost: HOST, imapPort: PORT,
      authType: 'password', password: PASS, syncIntervalMin: 15, enabled: true,
    }),
  })
  const b = await r.json().catch(() => ({}))
  if (!r.ok) { console.log('建账户失败', r.status, JSON.stringify(b).slice(0, 300)); process.exit(1) }
  acct = b.account || b
  console.log(`已创建账户 ${acct.id} ${acct.emailAddress} @ ${HOST}:${PORT}`)
} else {
  console.log(`复用已有账户 ${acct.id} ${acct.emailAddress} @ ${HOST}:${acct.imapPort}`)
}

// 2) 只读同步：POST /api/emails/sync {account_id}
const t0 = Date.now()
const r = await fetch(`${BASE}/api/emails/sync`, {
  method: 'POST', headers: H, body: JSON.stringify({ account_id: acct.id }),
})
const rep = await r.json().catch(() => ({}))
console.log(`\nPOST /api/emails/sync → ${r.status}，耗时 ${Date.now() - t0}ms`)
console.log(JSON.stringify(rep, null, 2).slice(0, 900))

const failed = rep.failed || []
const imapOK = r.status === 200 && !failed.includes(USER)
check('真实 IMAP 连通并完成同步', imapOK,
  failed.includes(USER) ? `failed=${JSON.stringify(failed)}` : `mode=${rep.mode} synced=${rep.synced} new=${rep.new}`)

// 3) 拉回来的邮件：主题/日期/有无附件，确认 MIME 解析不是空的
const list = await (await fetch(`${BASE}/api/emails?limit=200`, { headers: H })).json()
const emails = (list.emails || []).filter((e) => e.accountId === acct.id)
console.log(`\n该账户邮件 ${emails.length} 封（最多列 15 封）`)
for (const e of emails.slice(0, 15)) {
  const d = new Date((e.date || 0) * 1000).toISOString().slice(0, 10)
  console.log(`  ${d}  ${(e.hasAttachments ? '[附件] ' : '       ')}${(e.subject || '(无主题)').slice(0, 46)}`)
}
check('至少拉到 1 封邮件', emails.length > 0, `${emails.length} 封`)
check('邮件带有效时间戳', emails.every((e) => (e.date || 0) > 1600000000),
  emails.map((e) => e.date).slice(0, 3).join(' '))
check('至少一封有主题', emails.some((e) => (e.subject || '').trim().length > 0))

// 4) 发票候选：有没有账单/发票类邮件被识别（不下载附件，只看是否建档）
const inv = await (await fetch(`${BASE}/api/emails/invoices?limit=50`, { headers: H })).json()
const invs = (inv.invoices || []).filter((i) => i.accountId === acct.id || emails.some((e) => e.id === i.emailId))
console.log(`\n该账户发票记录 ${invs.length} 条`)
for (const i of invs.slice(0, 10)) {
  console.log(`  ${i.status.padEnd(10)} ${(i.fileName || '(未落盘)').padEnd(46)} amount=${i.amount}`)
}
const cand = emails.filter((e) => /发票|账单|对账|收据|报销|invoice|receipt|bill/i.test(`${e.subject || ''} ${e.snippet || ''}`))
console.log(`命中发票/账单关键词的邮件：${cand.length} 封`)
cand.slice(0, 8).forEach((e) => console.log(`  · ${(e.subject || '(无主题)').slice(0, 50)}`))
if (cand.length === 0) {
  // 这个邮箱本来就没有账单/发票邮件（例如只用来收验证码的），此时
  // 「有没有建档」无从谈起——报 FAIL 会把「数据为空」说成「功能坏了」。
  console.log('SKIP  该邮箱没有发票/账单类邮件，不据此判定建档能力')
} else {
  check('有发票类邮件被建档', invs.length > 0, `${invs.length} 条`)
}

console.log(`\n合计 ${pass} PASS / ${fail} FAIL`)
process.exit(fail ? 1 : 0)

