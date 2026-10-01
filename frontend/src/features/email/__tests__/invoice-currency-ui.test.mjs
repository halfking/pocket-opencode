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

// --- 真实实现的行为断言 ---
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

test('sumByCurrency：不同币种绝不相加', () => {
  const groups = money.sumByCurrency([
    { amount: 100, currency: 'USD' },
    { amount: 50, currency: 'CNY' },
    { amount: 50, currency: 'CNY' },
  ])
  const byCur = Object.fromEntries(groups.map((g) => [g.currency, g.amount]))
  assert.equal(byCur.USD, 100)
  assert.equal(byCur.CNY, 100)
  // 绝不能出现 200 这种跨币种的数
  assert.equal(groups.some((g) => g.amount === 200), false)
})

test('sumByCurrency：币种为空归 CNY', () => {
  const groups = money.sumByCurrency([{ amount: 10, currency: '' }, { amount: 5, currency: 'CNY' }])
  assert.equal(groups.length, 1)
  assert.equal(groups[0].currency, 'CNY')
  assert.equal(groups[0].amount, 15)
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
