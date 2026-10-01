// invoice-money.ts — 发票金额的币种感知格式化与分组求和。
//
// 抽成独立模块有两个原因：
//
//  1. **正确性**：跨币种直接相加不是金额。此前 InvoiceCard / InvoiceListView /
//     入账 toast 三处硬编码 `¥`，100 USD 的发票显示成「¥100.00」——错账。
//     金额格式化必须跟发票自己的币种走。
//  2. **可测**：原先这些逻辑埋在 use-invoice-list.ts 的闭包里，测试只能靠
//     正则从源码里刨代码（刨出来的片段带 TS 类型注解，`new Function` 直接
//     SyntaxError）。抽出来后测试直接 import 真实实现。
//
// 口径与后端一致（ledger.go / invoice_list.go）：
//   - 币种缺失按 CNY（currencyOrDefault 同源）
//   - 金额规整到分，避免 0.1+0.2 之类的浮点尾巴进账

export interface CurrencyAmount {
  currency: string
  amount: number
  count?: number
}

/** 把金额规整到分（2 位小数），消除二进制浮点表示误差。 */
export function round2(n: number): number {
  return Math.round((n + Number.EPSILON) * 100) / 100
}

/** 币种为空时按 CNY 处理——与后端 currencyOrDefault 同源。 */
export function normalizeCurrency(c?: string | null): string {
  return c && c.trim() ? c.trim() : 'CNY'
}

function formatNumber(n: number): string {
  return n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

/** 金额 + 币种标签。CNY 用 ¥、USD 用 $、其它用货币代码。 */
export function formatMoney(amount: number, currency?: string | null): string {
  const cur = normalizeCurrency(currency)
  if (cur === 'CNY') return `¥${formatNumber(amount)}`
  if (cur === 'USD') return `$${formatNumber(amount)}`
  return `${cur} ${formatNumber(amount)}`
}

/**
 * 按币种分组累加。整数分累加保证「逐行相加 == 合计」，与后端
 * LedgerRows / WriteInvoiceSummaryDocs / InvoiceListStats 同一口径。
 */
export function sumByCurrency(
  items: Array<{ amount: number | string; currency?: string | null }>,
): CurrencyAmount[] {
  const cents = new Map<string, number>()
  const counts = new Map<string, number>()
  const order: string[] = []
  for (const it of items) {
    const cur = normalizeCurrency(it.currency)
    if (!cents.has(cur)) order.push(cur)
    cents.set(cur, (cents.get(cur) || 0) + Math.round(round2(Number(it.amount) || 0) * 100))
    counts.set(cur, (counts.get(cur) || 0) + 1)
  }
  return order.map((cur) => ({
    currency: cur,
    amount: round2((cents.get(cur) || 0) / 100),
    count: counts.get(cur) || 0,
  }))
}

/** 合计区展示：单币种一个数，多币种逐币种拼，绝不给无币种的裸数字。 */
export function summaryMoney(groups: CurrencyAmount[]): string {
  if (groups.length === 0) return '¥0.00'
  if (groups.length === 1) return formatMoney(groups[0]!.amount, groups[0]!.currency)
  return groups.map((g) => formatMoney(g.amount, g.currency)).join(' + ')
}
