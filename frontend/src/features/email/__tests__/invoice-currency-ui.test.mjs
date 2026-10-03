import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'

// invoice-currency-ui.test.mjs — 发票金额展示必须带币种，不能一律 ¥。
//
// 缺陷（2026-10-01 修）：后端已支持多币种（LedgerRows / WriteInvoiceSummaryDocs
// / InvoiceListStats 都已按币种分组），但前端三处硬编码 `¥`：
//   - InvoiceCard.vue  `<span class="inv-amount">¥{{ amount }}`
//   - InvoiceListView.vue 合计区 `¥{{ formatAmount(summary.amount) }}`
//   - use-invoice-list.ts 入账 toast `¥${formatAmount(inv.amount)}`
// 而 use-invoice-list 的 applySummary 还是把 list 里所有币种直接 reduce 成一个数。
//
// 后果：一张 100 USD 的发票在界面上显示成「¥100.00」，入账提示也是
// 「已入账 ¥100.00」——这是**错账**，且导出 CSV 连币种列都没有。
//
// 本文件是纯文本断言：直接检查源码里不能再出现硬编码的 ¥ 拼接。
// 真正的行为断言（分组求和）在 use-invoice-list 的 round2/summaryMoney 上，
// 通过下面的源码抽取直接跑真实实现，避免为了测试而重写一份逻辑。

const FEAT = path.resolve('src/features/email')

function readSrc(p) {
  return fs.readFileSync(path.join(FEAT, p), 'utf8')
}

test('发票卡片不再硬编码 ¥ 前缀', () => {
  const src = readSrc('InvoiceCard.vue')
  assert.ok(
    !/class="inv-amount">¥/.test(src),
    'InvoiceCard 仍把金额硬编码成 ¥——外币发票会显示成人民币',
  )
  // 金额必须由父组件传入（已格式化、带币种）
  assert.match(src, /class="inv-amount">\{\{ amount \}\}/)
})

test('列表页合计区不再硬编码 ¥，改用 summaryMoney()', () => {
  const src = readSrc('InvoiceListView.vue')
  assert.ok(
    !/summary-amount">¥/.test(src),
    '合计区仍硬编码 ¥——跨币种合计会被渲染成人民币总额',
  )
  assert.match(src, /summary-amount">\{\{ summaryMoney\(\) \}\}/)
})

test('卡片金额走 invoiceMoney()（按各自币种格式化）', () => {
  const src = readSrc('InvoiceListView.vue')
  assert.match(src, /:amount="invoiceMoney\(inv\)"/)
})

test('入账 toast 不再硬编码 ¥', () => {
  const src = readSrc('use-invoice-list.ts')
  assert.ok(
    !/toast\.success\(`\$\{res\.created[^`]*\} ¥/.test(src),
    '入账提示仍硬编码 ¥',
  )
  assert.match(
    src,
    /toast\.success\(`\$\{res\.created \? '已入账' : '该发票已入账'\} \$\{invoiceMoney\(inv\)\}`\)/,
  )
})

test('导出的 CSV 必须带币种列', () => {
  const src = readSrc('use-invoice-list.ts')
  assert.match(src, /'开票日期', '收到日期', '销售方', '金额', '币种'/)
  assert.match(src, /inv\.currency \|\| 'CNY'/)
})

test('合计按币种分组，而不是一个标量 amount', () => {
  const src = readSrc('use-invoice-list.ts')
  // summary 不再只有一个 amount 标量
  assert.ok(!/amount: totals\?\.amount \?\? list\.reduce/.test(src), '仍在把不同币种 reduce 成一个数')
  assert.match(src, /groups,/)
  assert.match(src, /singleAmount/)
})

test('入账按钮：外币发票禁用但**可见**，并带原因（不再凭空消失）', () => {
  const card = readSrc('InvoiceCard.vue')
  // 按钮的 v-if 不能再只看 canBook
  assert.ok(!/v-if="canBook"/.test(card), '按钮仍在外币时被整个藏掉——用户看不到原因')
  assert.match(card, /v-if="showBook"/)
  // 禁用态 + 悬浮原因
  assert.match(card, /:disabled="booking \|\| !canBook"/)
  assert.match(card, /:title="bookReason \|\| undefined"/)

  const list = readSrc('InvoiceListView.vue')
  assert.match(list, /:book-reason="bookBlockReason\(inv\)"/)
})

test('book() 自身也守外币（不能只靠按钮禁用）', () => {
  const src = readSrc('use-invoice-list.ts')
  const m = src.match(/async function book\([\s\S]*?\n  \}/)
  assert.ok(m, '未找到 book()')
  const body = m[0]
  assert.match(body, /bookBlockReason\(inv\)/, 'book() 没有兜底守卫：直接调用会把 USD 当 CNY 入账')
  assert.ok(
    /const blocked = bookBlockReason\(inv\)[\s\S]*?if \(blocked\)/.test(body),
    'book() 应在守卫后直接返回',
  )
})

test('bookBlockReason：外币与无金额分别给出可读原因', () => {
  const src = readSrc('use-invoice-list.ts')
  const m = src.match(/function bookBlockReason\([\s\S]*?\n  \}/)
  assert.ok(m, '未找到 bookBlockReason')
  const body = m[0]
  assert.match(body, /未解析出金额/)
  assert.match(body, /CNY/)
  assert.match(body, /只记人民币/)
  // 可入账时必须返回空串（bookable 就是靠它判空）
  assert.match(body, /return ''/)
})
// 直接 import 抽出来的 invoice-money.ts（生产代码与测试共用同一份），
// 不用正则从 use-invoice-list.ts 里刨代码——刨出来的片段会带 TS 类型注解。
// 抽模块的原因之一就是这个：原先逻辑埋在闭包里，只能靠文本断言，行为无法验证。

const money = await import('../invoice-money.ts')

test('formatMoney：CNY 用 ¥、USD 用 $、其它用货币代码', () => {
  assert.equal(money.formatMoney(100, 'CNY'), '¥100.00')
  assert.equal(money.formatMoney(100, 'USD'), '$100.00')
  assert.equal(money.formatMoney(100, 'EUR'), 'EUR 100.00')
  // 币种缺失时按 CNY 处理（与后端 currencyOrDefault 同源）
  assert.equal(money.formatMoney(100, ''), '¥100.00')
  assert.equal(money.formatMoney(100, undefined), '¥100.00')
})

test('round2：分组求和不会留下浮点尾巴', () => {
  let usd = 0
  for (let i = 0; i < 100; i++) usd = money.round2(usd + 0.07)
  assert.equal(usd, 7)
  assert.equal(money.round2(0.1 + 0.2), 0.3)
})

// 造一张「已核验」的发票行：downloaded + 有落盘文件。
// 这两个字段是必填的而不是可选默认值——没有它们时 sumByCurrency 会把
// 「构造不出凭证的行」也放行，那正是 61,500 那次错账的来源。
const ok = (amount, currency) => ({
  amount,
  currency,
  status: 'downloaded',
  filePath: 'email-invoices/x.pdf',
})

test('sumByCurrency：不同币种绝不相加', () => {
  const groups = money.sumByCurrency([ok(100, 'USD'), ok(50, 'CNY'), ok(50, 'CNY')])
  const byCur = Object.fromEntries(groups.map((g) => [g.currency, g.amount]))
  assert.equal(byCur.USD, 100)
  assert.equal(byCur.CNY, 100)
  // 绝不能出现 200 这种跨币种的数
  assert.equal(groups.some((g) => g.amount === 200), false)
})

test('sumByCurrency：币种为空归 CNY', () => {
  const groups = money.sumByCurrency([ok(10, ''), ok(5, 'CNY')])
  assert.equal(groups.length, 1)
  assert.equal(groups[0].currency, 'CNY')
  assert.equal(groups[0].amount, 15)
})

// 2026-10-02：真实库 2 行发票（3500 downloaded+有文件、58000 new+无文件）。
// 兜底重算此前对两行一视同仁，给出 61,500；后端 LedgerRows 给 3,500。
// 这条用例把该分歧钉在客户端侧——它是发票页在 totals 缺失时走的那条路。
test('sumByCurrency：未核验的行不进合计，但仍在列表里', () => {
  const list = [ok(3500, 'CNY'), { amount: 58000, currency: 'CNY', status: 'new' }]
  const groups = money.sumByCurrency(list)
  assert.equal(groups.length, 1)
  assert.equal(groups[0].amount, 3500, '合计必须排除没有凭证的那行')
  assert.equal(groups[0].count, 1)
  // 列表本身不受影响：两行都还在
  assert.equal(list.length, 2)
})

// 「状态是 downloaded 但服务端磁盘上没有凭证」这一档最容易被漏：
// 它长得和正常发票一模一样，却是采集流水线中途失败留下的残行。
test('sumByCurrency：downloaded 但无落盘文件同样不计入', () => {
  const groups = money.sumByCurrency([
    { amount: 100, currency: 'CNY', status: 'downloaded', filePath: '' },
    { amount: 20, currency: 'CNY', status: 'filed', filePath: 'a.pdf' },
  ])
  assert.equal(groups.length, 1)
  assert.equal(groups[0].amount, 20)
  assert.equal(groups[0].count, 1)
})

test('summaryMoney：单币种一个数，多币种逐个列出', () => {
  assert.equal(money.summaryMoney([]), '¥0.00')
  assert.equal(money.summaryMoney([{ currency: 'CNY', amount: 454.5 }]), '¥454.50')
  const multi = money.summaryMoney([
    { currency: 'CNY', amount: 100 },
    { currency: 'USD', amount: 50 },
  ])
  assert.equal(multi, '¥100.00 + $50.00')
  // 绝不能把跨币种的 150 渲染成 ¥150.00
  assert.ok(!multi.includes('¥150'))
})

// ---------------------------------------------------------------------------
// 本地镜像的发票行没有 filePath（2026-10-02）
// ---------------------------------------------------------------------------
//
// 判据收紧成「filePath 非空」之后，**设备本地模式**下每一行都会被滤掉：
// 本地表 local_email_invoices 没有 file_path 列（schema.ts 的建表与
// local-db.ts 的迁移清单里都没有），rowToInvoice（invoices-store.ts）也只
// 产出 fileName。实测过：本地镜像行合计 ¥0.00，同一行带 filePath 是 ¥3,500.00。
//
// 关键在下面这个夹具的造法：**字段名从 rowToInvoice 的真实源码里正则提取**，
// 不是我手写一份。所以哪天有人给映射器补上 filePath，这些用例会因为
// 「夹具形状变了」而需要重新审视，而不会继续绿着掩盖一个已经修好的前提。

const storeSrc = fs.readFileSync(path.join(FEAT, 'invoices-store.ts'), 'utf8')
const mapperBody = storeSrc.match(/function rowToInvoice\([^)]*\)\s*:\s*LocalInvoice\s*\{([\s\S]*?)\n\}/)?.[1]
const localRowKeys = mapperBody
  ? [...mapperBody.matchAll(/^\s{4}([A-Za-z0-9_]+):/gm)].map((m) => m[1])
  : []

/** 按 rowToInvoice 真实产出的字段造一行设备本地镜像发票。 */
const localMirrorRow = (over = {}) => {
  assert.ok(localRowKeys.length > 0, '未能在 invoices-store.ts 里定位 rowToInvoice')
  const sample = {
    id: 'inv-1', emailId: 'e-1', accountId: 'a-1', kind: 'bill', category: '其他',
    title: '', seller: '杭州创客家投资管理有限公司', amount: 3500, currency: 'CNY',
    invoiceNo: 'INV-001', invoiceDate: '2026-09-01', emailDate: 1756684800,
    subject: '发票', status: 'downloaded', extractedBy: 'rule',
    createdAt: 1756684800, updatedAt: 1756684800,
    fileName: '其他-杭州创客家投资管理有限公司-3500.00-20260901.pdf',
    fileSource: 'attachment', attempts: 1, lastError: '', feishuSentAt: 0,
    dirty: false, clientId: '',
    ...over,
  }
  const row = {}
  for (const k of localRowKeys) row[k] = sample[k]
  return row
}

test('本地镜像行（无 filePath）必须照样计入合计', () => {
  // 前提钉死：本地镜像确实不产出 filePath。哪天它产出了，这条会提醒重看判据。
  assert.equal(localRowKeys.includes('filePath'), false,
    'rowToInvoice 现在产出 filePath 了——判据可以收回单字段，本组用例需重写')
  const groups = money.sumByCurrency([localMirrorRow()])
  assert.equal(groups.length, 1, '设备本地模式下合计被整条滤空了')
  assert.equal(groups[0].amount, 3500)
  assert.equal(money.summaryMoney(groups), '¥3,500.00')
})

test('真实库两行：服务端行与本地镜像行给出同一个合计', () => {
  // 真实库 email_invoices 就是这两行（3500 downloaded+有文件 / 58000 new+无文件）。
  // 两个数据源形状不同（filePath vs fileName），判据必须收敛到同一个数。
  const serverRows = [
    { amount: 3500, currency: 'CNY', status: 'downloaded', filePath: 'data/email-invoices/a.pdf' },
    { amount: 58000, currency: 'CNY', status: 'new' },
  ]
  const mirrorRows = [
    localMirrorRow({ amount: 3500, status: 'downloaded' }),
    localMirrorRow({ id: 'inv-2', amount: 58000, status: 'new', fileName: undefined }),
  ]
  const fromServer = money.summaryMoney(money.resolveSummaryGroups(undefined, serverRows))
  const fromMirror = money.summaryMoney(money.resolveSummaryGroups(undefined, mirrorRows))
  assert.equal(fromMirror, '¥3,500.00')
  assert.equal(fromMirror, fromServer, '两个数据源给出不同合计 = 又一次两套口径')
})

test('fileName 为空串时不算有凭证', () => {
  // 放行 fileName 之后最容易漏的一档：字段在、值是空串。
  assert.equal(
    money.invoiceCountsTowardTotal({ status: 'downloaded', fileName: '' }),
    false,
  )
  assert.equal(
    money.invoiceCountsTowardTotal({ status: 'new', fileName: 'a.pdf' }),
    false,
    '状态不是 downloaded/filed 时，凭证字段非空也不能放行',
  )
  assert.equal(
    money.invoiceCountsTowardTotal({ status: 'filed', fileName: 'a.pdf' }),
    true,
  )
})
