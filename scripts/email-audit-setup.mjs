// 建一个指向本地 IMAP stub（127.0.0.1:1143）的账户并触发同步。
// 这是绕开「拿不到真实邮箱账户」这个阻塞的关键一步：账户创建接口显式接受
// imapHost/imapPort，而 fetcher.go 的 isPlainIMAPPort 放行 1143 走明文，
// 因此整条抓取链路可以在本机跑通，且不需要任何生产配置改动。
import { readFileSync } from 'node:fs'

const BASE = process.env.POCKET_API || 'http://127.0.0.1:8088'
const PW = process.env.POCKET_PASS
const GW_KEY = readFileSync('logs/.gateway-key', 'utf8').trim()
const EMAIL = process.env.AUDIT_EMAIL || 'audit-poc@pocket-audit.test'

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: PW }),
})
const lj = await login.json()
if (!login.ok) { console.log('登录失败', login.status, JSON.stringify(lj).slice(0, 200)); process.exit(1) }
const H = { Authorization: `Bearer ${lj.token || lj.access_token}`, 'Content-Type': 'application/json' }
console.log('1) 登录 OK')

// 清理同名旧账户，避免 LastSyncedUID 残留导致搜不到新邮件
const accs = await (await fetch(`${BASE}/api/email/accounts`, { headers: H })).json()
const olds = (accs.accounts || []).filter((a) => a.emailAddress === EMAIL)
for (const a of olds) {
  const r = await fetch(`${BASE}/api/email/accounts/${a.id}`, { method: 'DELETE', headers: H })
  console.log(`   删除旧账户 ${a.id} -> ${r.status}`)
}

const body = {
  displayName: '审计桩邮箱',
  emailAddress: EMAIL,
  imapHost: '127.0.0.1',
  imapPort: 1143,
  authType: 'password',
  password: 'irrelevant-for-stub',
  syncIntervalMin: 15,
  rules: '',
  enabled: true,
}
const created = await fetch(`${BASE}/api/email/accounts`, { method: 'POST', headers: H, body: JSON.stringify(body) })
const cj = await created.json()
console.log(`2) 建账户 -> ${created.status}  ${JSON.stringify(cj).slice(0, 220)}`)
if (!created.ok) process.exit(2)
const accId = cj.id || cj.account?.id

const sync = await fetch(`${BASE}/api/emails/sync`, {
  method: 'POST', headers: H, body: JSON.stringify({ accountId: accId }),
})
const sj = await sync.json().catch(() => ({}))
console.log(`3) 同步 -> ${sync.status}  ${JSON.stringify(sj).slice(0, 300)}`)

const list = await (await fetch(`${BASE}/api/emails?limit=20`, { headers: H })).json()
const listArr = list.emails || list.data || list.items || []
console.log(`4) 邮件列表 = ${Array.isArray(listArr) ? listArr.length : JSON.stringify(list).slice(0, 200)} 封`)
if (Array.isArray(listArr)) {
  for (const e of listArr.slice(0, 8)) {
    console.log(`   uid=${e.uid} from=${JSON.stringify(e.fromName || e.sender || e.from)} subj=${JSON.stringify((e.subject || '').slice(0, 40))} bodyLen=${(e.body || e.snippet || '').length}`)
  }
  if (listArr[0]) {
    const d = await (await fetch(`${BASE}/api/emails/${listArr[0].id || listArr[0].uid}`, { headers: H })).json()
    console.log(`5) 首封详情键 = ${JSON.stringify(Object.keys(d))}`)
    console.log(`   bodyLen=${(d.body || d.content || '').length} htmlLen=${(d.htmlBody || d.html || '').length} attachments=${(d.attachments || []).length}`)
  }
}
console.log(`\naccountId=${accId}  email=${EMAIL}`)
