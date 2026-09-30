// 发票链路端到端：真实 IMAP 夹具 → 收信 → 抽取发票 → 落盘命名 → A4 网格导出。
//
// 覆盖需求 3（{费用类型}-{对方单位}-{金额}-{日期}.pdf、多次下载）与
// 需求 5（单 PDF 多张发票、A4 网格、可剪裁）。夹具邮件里带真实 PDF 附件，
// 走的是 harvestOne 的「PDF 附件」分支，不做任何桩替换。
const BASE = process.env.POCKET_API || 'http://127.0.0.1:8099'
const PW = process.env.POCKET_PASS
const IMAP_PORT = Number(process.env.IMAP_PORT || 1143)

const login = await fetch(`${BASE}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: PW }),
})
const lj = await login.json()
if (!login.ok) { console.log('登录失败', lj.status, JSON.stringify(lj).slice(0, 200)); process.exit(1) }
const H = { Authorization: `Bearer ${lj.token || lj.access_token}`, 'Content-Type': 'application/json' }
let pass = 0, fail = 0
const check = (name, ok, detail = '') => {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? '  → ' + detail : ''}`)
  ok ? pass++ : fail++
}

// 0) 指向本机 IMAP 夹具的账户（stub 接受任意口令）。
//    固定用一个**专用**地址：lastSyncedUID 是按账户记的，复用别的账户会带上
//    之前几轮遗留的 UID 状态，夹具一改就会把旧行和新邮件搅在一起。
const FIXTURE_ADDR = process.env.FIXTURE_ADDR || 'invoice-fixture@example.com'
const accRes = await (await fetch(`${BASE}/api/email/accounts`, { headers: H })).json()
let acct = (accRes.accounts || []).find((a) => a.emailAddress === FIXTURE_ADDR)
if (!acct) {
  const created = await fetch(`${BASE}/api/email/accounts`, {
    method: 'POST', headers: H,
    body: JSON.stringify({
      displayName: 'IMAP 发票夹具',
      emailAddress: FIXTURE_ADDR,
      imapHost: '127.0.0.1',
      imapPort: IMAP_PORT,
      authType: 'password',
      password: 'fixture-pass',
      syncIntervalMin: 5,
      enabled: true,
    }),
  })
  const cb = await created.json().catch(() => ({}))
  if (!created.ok) { console.log('建账户失败', created.status, JSON.stringify(cb).slice(0, 200)); process.exit(1) }
  acct = cb.account || cb
}
console.log(`夹具账户 ${acct.id} ${acct.emailAddress} → ${acct.imapHost}:${acct.imapPort}\n`)

// 1) 跑一轮流水线（收信 + 垃圾清理 + 提醒 + 发票采集 + 推送/汇总）
const run = await fetch(`${BASE}/api/email/pipeline/run`, { method: 'POST', headers: H, body: '{}' })
const rep = await run.json().catch(() => ({}))
console.log(`流水线 → ${run.status}：synced=${rep.accountsSynced} new=${rep.newEmails} spam=${rep.spamMoved} 发票=${JSON.stringify(rep.invoices)}`)
check('流水线 200', run.status === 200, `status=${run.status}`)
check('同步到夹具邮件', (rep.newEmails || 0) > 0, `newEmails=${rep.newEmails}`)
check('发票被处理（>0）', (rep.invoices?.Processed || 0) > 0, JSON.stringify(rep.invoices))

// 2) 发票列表 + 命名规范
const invRes = await (await fetch(`${BASE}/api/emails/invoices?limit=200`, { headers: H })).json()
const invs = invRes.invoices || []
console.log(`\n发票 ${invs.length} 条`)
for (const i of invs.slice(0, 6)) {
  console.log(`  ${i.status.padEnd(10)} ${(i.fileName || '(无文件)').padEnd(52)} amount=${i.amount} seller=${i.seller} src=${i.fileSource || '-'}`)
}
const NAMED = /^.+\-.+\-\d+(\.\d+)?\-\d{4}-\d{2}-\d{2}\.(pdf|jpg|png)$/i
const withFile = invs.filter((i) => i.filePath)
check('存在已落盘的发票', withFile.length > 0, `${withFile.length}/${invs.length}`)
const badName = withFile.filter((i) => !NAMED.test(i.fileName || ''))
check('文件名为 {费用类型}-{对方单位}-{金额}-{日期}', badName.length === 0,
  badName.length ? badName.map((i) => i.fileName).join(' | ') : withFile.map((i) => i.fileName).join(' | '))

// 3) 原件可下载（单张发票文件）
if (withFile[0]) {
  const r = await fetch(`${BASE}/api/emails/invoices/${withFile[0].id}/file`, { headers: { Authorization: H.Authorization } })
  const buf = Buffer.from(await r.arrayBuffer())
  check('单张发票原件可下载', r.status === 200 && buf.length > 0, `status=${r.status} ${buf.length}B ${buf.subarray(0, 4).toString('latin1')}`)
}

// 4) A4 网格导出：2x2 与 3x3 都要能出可下载 PDF
if (withFile.length) {
  const ids = withFile.slice(0, 5).map((i) => i.id)
  for (const grid of [2, 3]) {
    const e = await fetch(`${BASE}/api/emails/invoices/export`, {
      method: 'POST', headers: H, body: JSON.stringify({ ids, grid }),
    })
    const eb = await e.json().catch(() => ({}))
    check(`A4 ${grid}x${grid} 导出接口 200`, e.status === 200 && !!eb.file, `status=${e.status} ${JSON.stringify(eb).slice(0, 120)}`)
    if (e.status === 200 && eb.file) {
      const dl = await fetch(`${BASE}${eb.url}`, { headers: { Authorization: H.Authorization } })
      const buf = Buffer.from(await dl.arrayBuffer())
      const isPdf = buf.subarray(0, 4).toString('latin1') === '%PDF'
      const pages = (buf.toString('latin1').match(/\/Type\s*\/Page[^s]/g) || []).length
      check(`导出文件是 PDF（${grid}x${grid}，${buf.length}B）`, dl.status === 200 && isPdf, `status=${dl.status} pdf=${isPdf}`)
      console.log(`      粗略页对象计数 = ${pages}（源发票 ${ids.length} 张，${grid}x${grid} 每页 ${grid * grid} 格）`)
    }
  }
} else {
  console.log('\nSKIP  网格导出：没有已落盘发票')
}

// 5) 汇总列表 + 合计金额
const sum = await (await fetch(`${BASE}/api/emails/invoices/summary`, { headers: H })).json()
console.log(`\n汇总：count=${sum.count} 合计=${sum.amountTotal} 已下载=${sum.downloaded} 待整理=${sum.pending} 失败=${sum.failed}`)
console.log(`共享文档：${sum.shareDocCsv} / ${sum.shareDocMd}`)
check('汇总有记录与合计', sum.count > 0 && typeof sum.amountTotal === 'number', `count=${sum.count} total=${sum.amountTotal}`)
check('生成了共享汇总文档', !!sum.shareDocCsv && !!sum.shareDocMd, `${sum.shareDocCsv} / ${sum.shareDocMd}`)

console.log(`\n合计 ${pass} PASS / ${fail} FAIL`)
process.exit(fail ? 1 : 0)
