// 构造覆盖易错 MIME 形态的邮件样本。
// 每一封都对应一个真实会出问题的点，用来在真机上复现/证伪用户反馈的
// 「邮件详情展示不正常，缺失图片或内容」。
import zlib from 'node:zlib'

/** 生成一张纯色 PNG（120x80），让详情页的内嵌图在截图里肉眼可辨 */
function solidPng(w = 120, h = 80, rgb = [37, 99, 235]) {
  const raw = Buffer.alloc((w * 3 + 1) * h)
  let o = 0
  for (let y = 0; y < h; y++) {
    raw[o++] = 0 // filter type 0
    for (let x = 0; x < w; x++) {
      // 画一个对角线渐变，避免纯色在截图里难以判断是否真的加载
      raw[o++] = (rgb[0] + x) % 256
      raw[o++] = (rgb[1] + y) % 256
      raw[o++] = rgb[2]
    }
  }
  const chunk = (type, data) => {
    const len = Buffer.alloc(4)
    len.writeUInt32BE(data.length)
    const td = Buffer.concat([Buffer.from(type, 'ascii'), data])
    const crc = Buffer.alloc(4)
    crc.writeUInt32BE(crc32(td) >>> 0)
    return Buffer.concat([len, td, crc])
  }
  const ihdr = Buffer.alloc(13)
  ihdr.writeUInt32BE(w, 0); ihdr.writeUInt32BE(h, 4)
  ihdr[8] = 8; ihdr[9] = 2; ihdr[10] = 0; ihdr[11] = 0; ihdr[12] = 0
  return Buffer.concat([
    Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk('IHDR', ihdr),
    chunk('IDAT', zlib.deflateSync(raw)),
    chunk('IEND', Buffer.alloc(0)),
  ])
}

let CRC_TABLE = null
function crc32(buf) {
  if (!CRC_TABLE) {
    CRC_TABLE = new Int32Array(256)
    for (let n = 0; n < 256; n++) {
      let c = n
      for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
      CRC_TABLE[n] = c
    }
  }
  let c = -1
  for (let i = 0; i < buf.length; i++) c = CRC_TABLE[(c ^ buf[i]) & 0xff] ^ (c >>> 8)
  return c ^ -1
}

const b64 = (b) => Buffer.from(b).toString('base64')
/** quoted-printable：中文正文常用，且长行需要软换行 */
const qp = (s) => Buffer.from(s, 'utf8').toString('binary')
  .replace(/=/g, '=3D').replace(/[^\x20-\x7e]/g, (c) => `=${Buffer.from(c, 'binary').toString('hex').toUpperCase()}`)

// ENVELOPE 的 date 与 INTERNALDATE 共用同一种 IMAP date-time：
//   date-day-fixed "-" date-month "-" date-year SP time SP zone
// 即 "30-Sep-2026 10:00:00 +0000"。注意 INTERNALDATE 不是 RFC 1123——
// 之前这里直接吐 toUTCString()（"... GMT"），go-imap 按
// _2-Jan-2006 15:04:05 -0700 解析会报 cannot parse "..." as "_2"，
// 整个账户进 failed 列表。
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec']
const imapDate = (offsetMin = 0) => {
  const t = new Date(Date.UTC(2026, 8, 30, 10, 0, 0) + offsetMin * 60000)
  const p2 = (n) => String(n).padStart(2, '0')
  return (
    `${p2(t.getUTCDate())}-${MONTHS[t.getUTCMonth()]}-${t.getUTCFullYear()} ` +
    `${p2(t.getUTCHours())}:${p2(t.getUTCMinutes())}:${p2(t.getUTCSeconds())} +0000`
  )
}

// RFC 1123 日期，用在邮件原文的 Date 头里（走标准邮件解析，与 IMAP 无关）
const rfcDate = (offsetMin = 0) => {
  const t = new Date(Date.UTC(2026, 8, 30, 10, 0, 0) + offsetMin * 60000)
  return t.toUTCString()
}
const D = rfcDate

const png = solidPng()


// 一张**真格式**的发票 PDF（base64 由 backend/internal/email/gen_fixture_invoice_test.go 生成）。
// 之前这里用的是退化 PDF（只有 Catalog、无页树）：采集器能落盘，但 A4 网格导出会被
// pdfcpu 拒绝，导致「发票导出」这条需求在夹具环境里永远拿不到真实产物。
const validInvoicePdfB64 =
  'JVBERi0xLjMKMyAwIG9iago8PC9UeXBlIC9QYWdlCi9QYXJlbnQgMSAwIFIKL1Jlc291cmNlcyAyIDAgUgovQ29udGVudHMg' +
  'NCAwIFI+PgplbmRvYmoKNCAwIG9iago8PC9GaWx0ZXIgL0ZsYXRlRGVjb2RlIC9MZW5ndGggNDIwPj4Kc3RyZWFtCngBhNLN' +
  'btpKGMbxva/iWYJ0mLzzPbMMxBxRtaQSo0iV2Die1xWU4ghMmt59pWBES1VYeeW/fvPMED4UhHVBwnr8KEgQEf7vv1+LccLd' +
  'tLFZZbaNrrx0MVZsnlUOWvrITmWK7JraSOUggyBCalCmYpwgPQmp4UMQJiJlDJ7uE8rRbP70OJuUWA5mn+4/o1m9dYcdL4fD' +
  'tEaZinHC3ZQq7zzZLAOTiSZnZepn6zVbXVVUhdpL4jpGSCWIkBqUqTAkiOC9FyFCWhJEGEkpiLBjLDBOMEoEA++C8BEpYzDb' +
  'vrarmjFvh2mNMhUyvv/XV7SzQoXLioyXGWW1VkRERFJpY50PsT9Pr3Lupsp64Y9Dzfb7A+Oh6vhCdaxcV50zipQbURyp0Gd6' +
  'i7U3LcadLAvebHjXB2T8vXDdcU5MV2/dYceYbNpDxoJ3r6ua95i04j987LL4cyljbi6l7Wmp8eHnX7hj4DruXHh84e1LW3/j' +
  'Dg/8vf2XSuubKmVOqtR21QbL9we/2tYbsRz2N3Aa8Ji7bjz3JvMvkCqQIBqmNcpU/BoAg7LcfwplbmRzdHJlYW0KZW5kb2Jq' +
  'CjEgMCBvYmoKPDwvVHlwZSAvUGFnZXMKL0tpZHMgWzMgMCBSIF0KL0NvdW50IDEKL01lZGlhQm94IFswIDAgNTk1LjI4IDg0' +
  'MS44OV0KPj4KZW5kb2JqCjUgMCBvYmoKPDwvVHlwZSAvRm9udAovQmFzZUZvbnQgL0hlbHZldGljYS1Cb2xkCi9TdWJ0eXBl' +
  'IC9UeXBlMQovRW5jb2RpbmcgL1dpbkFuc2lFbmNvZGluZwo+PgplbmRvYmoKNiAwIG9iago8PC9UeXBlIC9Gb250Ci9CYXNl' +
  'Rm9udCAvSGVsdmV0aWNhCi9TdWJ0eXBlIC9UeXBlMQovRW5jb2RpbmcgL1dpbkFuc2lFbmNvZGluZwo+PgplbmRvYmoKMiAw' +
  'IG9iago8PAovUHJvY1NldCBbL1BERiAvVGV4dCAvSW1hZ2VCIC9JbWFnZUMgL0ltYWdlSV0KL0ZvbnQgPDwKL0ZmNWQyZGU1' +
  'ZjNhNzE2OTlhZTRiMmQ4MzE3OWU2MmQwOWU2ZmM0MTI2IDUgMCBSCi9GMGE3NjcwNWQxOGUwNDk0ZGQyNGNiNTczZTUzYWEw' +
  'YThjNzEwZWM5OSA2IDAgUgo+PgovWE9iamVjdCA8PAo+PgovQ29sb3JTcGFjZSA8PAo+Pgo+PgplbmRvYmoKNyAwIG9iago8' +
  'PAovUHJvZHVjZXIgKP7/AEYAUABEAEYAIAAxAC4ANykKL0NyZWF0aW9uRGF0ZSAoRDoyMDI2MDkzMDIzMjcyOSkKL01vZERh' +
  'dGUgKEQ6MjAyNjA5MzAyMzI3MjkpCj4+CmVuZG9iago4IDAgb2JqCjw8Ci9UeXBlIC9DYXRhbG9nCi9QYWdlcyAxIDAgUgov' +
  'TmFtZXMgPDwKL0VtYmVkZGVkRmlsZXMgPDwgL05hbWVzIFsKICAKXSA+Pgo+Pgo+PgplbmRvYmoKeHJlZgowIDkKMDAwMDAw' +
  'MDAwMCA2NTUzNSBmIAowMDAwMDAwNTc3IDAwMDAwIG4gCjAwMDAwMDA4NjEgMDAwMDAgbiAKMDAwMDAwMDAwOSAwMDAwMCBu' +
  'IAowMDAwMDAwMDg3IDAwMDAwIG4gCjAwMDAwMDA2NjQgMDAwMDAgbiAKMDAwMDAwMDc2NSAwMDAwMCBuIAowMDAwMDAxMDcx' +
  'IDAwMDAwIG4gCjAwMDAwMDExODQgMDAwMDAgbiAKdHJhaWxlcgo8PAovU2l6ZSA5Ci9Sb290IDggMCBSCi9JbmZvIDcgMCBS' +
  'Cj4+CnN0YXJ0eHJlZgoxMjgxCiUlRU9GCg==' +
  ''
export function buildMails() {
  const mails = []
  // 派生字段必须放在 ...m 之后：之前 ...m 写在最后，把上面算好的
  // from/to 数组整个覆盖回裸字符串，stub 的 ENVELOPE 生成直接
  // TypeError: a.map is not a function 崩在第一条 FETCH 上。
  const push = (m) => {
    const { from, fromName, to, subject, ...rest } = m
    const idx = mails.length + 1
    mails.push({
      ...rest,
      from,
      // ⑥ 故意保留空显示名，覆盖「无 From 显示名」的空态渲染
      fromName: fromName ?? '',
      to: to || 'me@pocket-audit.test',
      subject: subject ?? '',
      date: imapDate(m.offset ?? 0),
      internaldate: imapDate(m.offset ?? 0),
      messageId: `<audit-${idx}@pocket-audit.test>`,
    })
  }

  // ① multipart/related + cid 内嵌图 —— 直接对应「详情缺失图片」
  push({
    label: 'cid 内嵌图（multipart/related）',
    from: 'design@pocket-audit.test', fromName: '设计中心',
    subject: '季度视觉规范 v3（含内嵌示意图）',
    offset: 0,
    raw: [
      'MIME-Version: 1.0',
      'Content-Type: multipart/related; boundary="REL1"',
      '',
      '--REL1',
      'Content-Type: text/html; charset="utf-8"',
      'Content-Transfer-Encoding: 8bit',
      '',
      '<html><body>',
      '<h2>季度视觉规范 v3</h2>',
      '<p>下面的示意图请查收，这是本次更新的主色板：</p>',
      '<img src="cid:logo-v3@audit" alt="主色板" width="120" height="80">',
      '<p>规范文档编号 QA-2026-0930，评审截止本周五。</p>',
      '</body></html>',
      '',
      '--REL1',
      'Content-Type: image/png; name="palette.png"',
      'Content-Transfer-Encoding: base64',
      'Content-ID: <logo-v3@audit>',
      'Content-Disposition: inline; filename="palette.png"',
      '',
      b64(png),
      '',
      '--REL1--',
      '',
    ].join('\r\n'),
  })

  // ② multipart/alternative（quoted-printable 中文 + HTML）—— 对应「内容缺失/乱码」
  push({
    label: 'alternative + quoted-printable 中文',
    from: 'ops@pocket-audit.test', fromName: '运维组',
    subject: '生产环境变更通知（请确认）',
    offset: 5,
    raw: [
      'MIME-Version: 1.0',
      'Content-Type: multipart/alternative; boundary="ALT1"',
      '',
      '--ALT1',
      'Content-Type: text/plain; charset="utf-8"',
      'Content-Transfer-Encoding: quoted-printable',
      '',
      qp('各位同学：\n\n本周六 02:00-04:00 将进行生产环境数据库主从切换，期间只读。\n请提前保存工作。\n\n运维组'),
      '',
      '--ALT1',
      'Content-Type: text/html; charset="utf-8"',
      'Content-Transfer-Encoding: quoted-printable',
      '',
      qp('<html><body><h2>生产环境变更通知</h2><p>本周六 <b>02:00-04:00</b> 进行数据库主从切换，期间只读。</p><p>请提前保存工作。</p></body></html>'),
      '',
      '--ALT1--',
      '',
    ].join('\r\n'),
  })

  // ③ 嵌套 mixed > related > alternative + base64 附件 —— 最复杂的真实形态
  push({
    label: '嵌套 mixed>related>alternative + 附件',
    from: 'finance@pocket-audit.test', fromName: '财务部',
    subject: '9 月度对账单（附件 + 内嵌图表）',
    offset: 12,
    raw: [
      'MIME-Version: 1.0',
      'Content-Type: multipart/mixed; boundary="OUTER"',
      '',
      '--OUTER',
      'Content-Type: multipart/related; boundary="MID"',
      '',
      '--MID',
      'Content-Type: multipart/alternative; boundary="INNER"',
      '',
      '--INNER',
      'Content-Type: text/plain; charset="utf-8"',
      '',
      '9 月度对账单见附件，图表见正文。',
      '',
      '--INNER',
      'Content-Type: text/html; charset="utf-8"',
      '',
      '<html><body><h3>9 月度对账单</h3><p>图表：</p><img src="cid:chart@audit" width="120" height="80" alt="柱状图"></body></html>',
      '',
      '--INNER--',
      '',
      '--MID',
      'Content-Type: image/png',
      'Content-Transfer-Encoding: base64',
      'Content-ID: <chart@audit>',
      '',
      b64(png),
      '',
      '--MID--',
      '',
      '--OUTER',
      'Content-Type: application/pdf; name="statement.pdf"',
      'Content-Transfer-Encoding: base64',
      'Content-Disposition: attachment; filename="statement.pdf"',
      '',
      b64(Buffer.from('%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n')),
      '',
      '--OUTER--',
      '',
    ].join('\r\n'),
  })

  // ⑦（必须放在最后）增值税电子发票：真格式 PDF 附件 + 正文带发票号/开票日期/价税合计。
// ⚠️ 只能追加在末尾：IMAP 的 UID 一旦分配就不可变，插在中间会让后面所有邮件的
  // UID 位移，客户端按 last_synced_uid 增量拉取时会把新邮件的正文写进旧行
  // （实测：主题与正文错位）。这封是「发票链路」的验收邮件：规则层要抽出金额与发票号（文件名
  // ④ 纯文本 + 非 UTF-8 声明（GB2312 声明但内容是 UTF-8，常见于国内邮件）
  push({
    label: 'GB2312 声明 + 无 HTML',
    from: 'hr@pocket-audit.test', fromName: '人力资源',
    subject: '入职材料清单',
    offset: 20,
    raw: [
      'MIME-Version: 1.0',
      'Content-Type: text/plain; charset="GB2312"',
      'Content-Transfer-Encoding: 8bit',
      '',
      '请携带以下材料办理入职：身份证原件、学历证书、离职证明、体检报告。',
      '',
    ].join('\r\n'),
  })

  // ⑤ 触发「自动归纳整理」的长 HTML 通讯（多区块，考验正文截断与清洗）
  push({
    label: '长 HTML 通讯（多区块）',
    from: 'digest@pocket-audit.test', fromName: '技术周报',
    subject: '技术周报：本周 7 个项目进展与 3 个风险',
    offset: 30,
    raw: [
      'MIME-Version: 1.0',
      'Content-Type: text/html; charset="utf-8"',
      'Content-Transfer-Encoding: 8bit',
      '',
      '<html><head><style>.x{color:#333}</style></head><body>',
      '<h1>技术周报</h1>',
      ...Array.from({ length: 7 }, (_, i) =>
        `<h2>${i + 1}. 项目 Alpha-${i + 1} 进展</h2><p>本周完成模块 ${i + 1} 的联调，代码评审通过 ${i * 3 + 4} 个 PR，遗留问题 ${i} 个。下周计划推进压测与灰度。</p>`),
      '<h2>风险与阻塞</h2>',
      '<ul><li>网关上游偶发超时，已提工单</li><li>测试环境数据污染，待重建</li><li>移动端首屏在低端机偏慢</li></ul>',
      '<p>本周共合并 32 个 PR，关闭 issue 18 个。</p>',
      '</body></html>',
      '',
    ].join('\r\n'),
  })

  // ⑥ 无 From 显示名 + 空正文（覆盖空态渲染）
  push({
    label: '空正文 / 无显示名',
    from: 'noreply@pocket-audit.test', fromName: '',
    subject: '',
    offset: 40,
    raw: [
      'MIME-Version: 1.0',
      'Content-Type: text/plain; charset="utf-8"',
      '',
      ' ',
      '',
    ].join('\r\n'),
  })

  // {费用类型}-{对方单位}-{金额}-{日期}.pdf 的金额/日期就来自这里），
  // 采集器要把附件落盘，A4 网格导出要能用它拼出可剪裁的凭证页。
  push({
    label: '增值税电子发票（真格式 PDF 附件）',
    from: 'billing@vendor-payments.test', fromName: '云服务开票中心',
    subject: '增值税电子普通发票已开具，请查收',
    offset: 50,
    raw: [
      'MIME-Version: 1.0',
      'Content-Type: multipart/mixed; boundary="INV"',
      '',
      '--INV',
      'Content-Type: text/plain; charset="utf-8"',
      'Content-Transfer-Encoding: 8bit',
      '',
      '发票号码：25332000000123456789',
      '开票日期：2026-09-28',
      '销售方名称：云服务开票中心',
      '发票抬头：Openpocket Demo',
      '价税合计：￥1280.00',
      '发票文件见附件。',
      '',
      '--INV',
      'Content-Type: application/pdf; name="invoice-25332000000123456789.pdf"',
      'Content-Transfer-Encoding: base64',
      'Content-Disposition: attachment; filename="invoice-25332000000123456789.pdf"',
      '',
      validInvoicePdfB64,
      '',
      '--INV--',
      '',
    ].join('\r\n'),
  })


  return mails
}
