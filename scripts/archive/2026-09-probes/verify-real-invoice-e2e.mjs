// 真实发票邮件的端到端复验：解析 → 落盘 → A4 网格导出。
// 用 QQ 私人邮箱里那封真发票（杭州创客家投资管理有限公司，3500.00）。
const B = process.env.POCKET_API || 'http://127.0.0.1:8099'
const l = await (await fetch(`${B}/api/auth/login`, {
  method: 'POST', headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ username: 'admin', password: process.env.POCKET_PASS }),
})).json()
const H = { Authorization: `Bearer ${l.token || l.access_token}`, 'Content-Type': 'application/json' }
let pass = 0, fail = 0
const check = (n, ok, d = '') => { console.log(`${ok ? 'PASS' : 'FAIL'}  ${n}${d ? '  → ' + d : ''}`); ok ? pass++ : fail++ }

const accs = (await (await fetch(`${B}/api/email/accounts`, { headers: H })).json()).accounts || []
const qq = accs.find((a) => a.emailAddress === '56551681@qq.com')
const emails = (await (await fetch(`${B}/api/emails?limit=500`, { headers: H })).json()).emails || []
const target = emails.find((e) => e.accountId === qq.id && /发票/.test(e.subject || ''))
if (!target) { console.log('找不到真实发票邮件'); process.exit(1) }
console.log(`目标邮件：${target.subject}\n`)

// 1) 重新解析（先删掉旧记录，验证新解析器的输出）
for (const inv of (await (await fetch(`${B}/api/emails/invoices?limit=100`, { headers: H })).json()).invoices || []) {
  if (inv.emailId === target.id) await fetch(`${B}/api/emails/invoices/${inv.id}`, { method: 'DELETE', headers: H })
}
const r = await fetch(`${B}/api/emails/invoices/extract`, {
  method: 'POST', headers: H, body: JSON.stringify({ emailId: target.id }),
})
const b = await r.json().catch(() => ({}))
const inv = b.invoice
console.log('解析结果：', JSON.stringify({
  amount: inv?.amount, seller: inv?.seller, invoiceNo: inv?.invoiceNo,
  invoiceDate: inv?.invoiceDate, kind: inv?.kind, status: inv?.status,
}))
check('发票号码抽到', inv?.invoiceNo === '26332000008261110741', String(inv?.invoiceNo))
check('金额抽到 3500.00（修复前是 0）', inv?.amount === 3500, String(inv?.amount))
check('销售方是开票公司（修复前是发件地址）',
  inv?.seller === '杭州创客家投资管理有限公司', String(inv?.seller))

// 2) 手动采集附件（只下载，不动邮箱）
const t0 = Date.now()
const hr = await fetch(`${B}/api/emails/invoices/harvest`, {
  method: 'POST', headers: H, body: JSON.stringify({ ids: [inv.id] }),
})
const hb = await hr.json().catch(() => ({}))
console.log(`\n采集 → ${hr.status}，耗时 ${((Date.now() - t0) / 1000).toFixed(1)}s  ${JSON.stringify(hb.result || hb)}`)
check('采集接口 200 且不挂死', hr.status === 200, `status=${hr.status}`)
const harvested = await (await fetch(`${B}/api/emails/invoices?limit=100`, { headers: H })).json()
const mine = (harvested.invoices || []).find((i) => i.id === inv.id)
console.log(`落盘状态：${mine?.status}  文件：${mine?.fileName || '(未落盘)'}  来源：${mine?.fileSource || '-'}`)
check('发票文件已落盘', mine?.status === 'downloaded' && !!mine?.fileName, `${mine?.status} ${mine?.fileName || ''}`)
check('文件名 = {费用类型}-{对方单位}-{金额}-{日期}.pdf',
  /其他-杭州创客家投资管理有限公司-3500\.00-\d{4}-\d{2}-\d{2}\.pdf$/.test(mine?.fileName || ''),
  String(mine?.fileName))

// 3) 汇总里能看到，金额计入合计
const sum = await (await fetch(`${B}/api/emails/invoices/summary`, { headers: H })).json()
const row = (sum.rows || []).find((x) => x.id === mine?.id)
console.log(`汇总：count=${sum.count} 合计=${sum.amountTotal}`)
console.log(`  该行：${JSON.stringify(row)}`)
check('汇总行含这张真发票', !!row && row.amount === 3500, JSON.stringify(row?.amount))
check('合计金额 ≥ 3500', sum.amountTotal >= 3500, String(sum.amountTotal))
check('生成了共享汇总文档', !!sum.shareDocCsv, `${sum.shareDocCsv}`)
check('飞书未配置时不给假链接', !sum.shareDocUrl, String(sum.shareDocUrl || '(空)'))

// 4) A4 网格导出（把真发票和夹具发票一起导）
const withFile = (harvested.invoices || []).filter((i) => i.filePath)
if (withFile.length) {
  for (const grid of [2, 3]) {
    const e = await fetch(`${B}/api/emails/invoices/export`, {
      method: 'POST', headers: H, body: JSON.stringify({ ids: withFile.map((i) => i.id), grid }),
    })
    const eb = await e.json().catch(() => ({}))
    let pdf = false, bytes = 0
    if (e.status === 200 && eb.file) {
      const dl = await fetch(`${B}${eb.url}`, { headers: { Authorization: H.Authorization } })
      const buf = Buffer.from(await dl.arrayBuffer())
      pdf = buf.subarray(0, 4).toString('latin1') === '%PDF'
      bytes = buf.length
    }
    check(`A4 ${grid}x${grid} 导出（含真发票）`, e.status === 200 && pdf,
      `status=${e.status} count=${eb.count} skipped=${JSON.stringify(eb.skipped)} ${bytes}B`)
  }
}
console.log(`\n合计 ${pass} PASS / ${fail} FAIL`)
process.exit(fail ? 1 : 0)
