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
 * 一张发票是否计入合计。**必须**与后端 `InvoiceCountsTowardTotal`
 * （backend/internal/email/ledger.go）逐项对应。
 *
 * 2026-10-02：此前这个函数**完全不过滤**，而它上面的注释却写着「与后端
 * LedgerRows / WriteInvoiceSummaryDocs / InvoiceListStats 同一口径」——
 * 注释是假的，InvoiceListStats 那时也在无过滤求和。真实库 2 行发票下，
 * 飞书台账写 3,500、列表 API 与这个兜底重算都给 61,500。
 *
 * `status` 设为必填而不是给个默认值：默认值会把「构造不出凭证的行」
 * 悄悄放行，那正是这次要消灭的歧义。造「未核验」行请显式传
 * `filePath: undefined`。
 *
 * 2026-10-02 补：`filePath` / `fileName` **两个都读**，规则仍然只有一条。
 * 这不是把判据放松成「两个都行」——是同一张发票在两个数据源里字段名不同：
 *
 *   - 服务端列表：`file_path`（email.ts:675；invoice_list.go:29 真的 SELECT 出来）
 *   - 设备本地镜像：本地表 `local_email_invoices` **没有 file_path 列**
 *     （schema.ts:244-269 的建表与 local-db.ts 的迁移清单里都没有），
 *     `rowToInvoice`（invoices-store.ts:18）也只产出 `fileName`。
 *
 * 只读 filePath 时，设备本地模式（需求 6 的默认姿态）下**每一行**都会被判成
 * 「没凭证」而滤掉，合计恒为 ¥0.00。实测：本地镜像行 ¥0.00，同一行带 filePath
 * 是 ¥3,500.00。
 *
 * 之所以不改成给本地表补 file_path 列：那是**设备 DB 迁移**，而真机从未验证过，
 * 存量设备上迁不迁得成没有证据。判据读两个字段名是把「数据源形状差异」留在
 * 判据里，迁移风险留在原地——后者无法验证，前者可以测。
 */
export function invoiceCountsTowardTotal(it: {
  status: string
  filePath?: string | null
  fileName?: string | null
}): boolean {
  return (it.status === 'downloaded' || it.status === 'filed') && !!(it.filePath || it.fileName)
}

/**
 * 按币种分组累加。整数分累加保证「逐行相加 == 合计」。
 *
 * 只累加 invoiceCountsTowardTotal 为真的行——判据与后端三处实现共用同一
 * 条规则（见上面函数的注释）。未核验的行**仍然出现在列表里**，只是不进
 * 合计，这是需求 3 明确要的行为。
 */
export function sumByCurrency(
  items: Array<{
    amount: number | string
    currency?: string | null
    status: string
    filePath?: string | null
    fileName?: string | null
  }>,
): CurrencyAmount[] {
  const cents = new Map<string, number>()
  const counts = new Map<string, number>()
  const order: string[] = []
  for (const it of items) {
    if (!invoiceCountsTowardTotal(it)) continue
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

// ---------------------------------------------------------------------------
// 服务端合计字段的「不许丢」链路
// ---------------------------------------------------------------------------
//
// 服务端列表响应给的是**三件套**（server_email_invoice.go:55-57）：
//
//	"amount":   page.Amount,     // 仅单一币种有意义；多币种时为 0
//	"currency": page.Currency,   // 同上；多币种时为 ""
//	"amounts":  page.Amounts,    // 按币种分组的合计，统计全量、不受分页截断
//
// 下游的优先级判定要求三个字段**都在**。中间任何一层只转发
// `{total, filed, amount}` 都会出两种错账：
//
//   - 单一外币（100 USD）：currency 丢失 → normalizeCurrency(undefined) 兜底成
//     CNY → 合计渲染成「¥100.00」。这正是 §7dk 在服务端修掉的那一类。
//   - 多币种：amounts 丢失且 amount=0 → 退回 sumByCurrency(当前页)
//     → 合计从「全量」缩成「这一页」，翻页时数字还会跳。
//
// 所以这里把「转发」和「判定优先级」都收进纯函数，让「不许丢字段」变成
// **能测的行为**，而不是一句口头约定（§7dn 同款思路）。

/** 服务端列表响应的合计字段。 */
export interface InvoiceTotals {
  total: number
  filed: number
  amount: number
  currency?: string
  amounts?: CurrencyAmount[]
}

/**
 * 把服务端列表响应的合计字段**原样**转发下去。
 *
 * 只做默认值兜底，**不做任何裁剪** —— 少转发一个字段就是错账，所以这里刻意
 * 不给 `amount` 加「多币种就归零」之类的加工：那该由服务端负责。
 */
export function invoiceTotalsFrom(res: {
  total?: number
  filed?: number
  amount?: number
  currency?: string
  amounts?: CurrencyAmount[]
}): InvoiceTotals {
  return {
    total: res.total ?? 0,
    filed: res.filed ?? 0,
    amount: res.amount ?? 0,
    currency: res.currency,
    amounts: res.amounts,
  }
}

/**
 * 按优先级决定合计区展示哪一组数。
 *
 * 1. `amounts` —— 服务端按币种分组的全量合计，**以它为准**；
 * 2. 否则 `amount` + `currency` —— 仅单一币种时服务端才给非零 amount；
 * 3. 都拿不到才用当前页的发票自行分组（本地兜底，分页窗口内）。
 *
 * 顺序不能换：第 2 步的 `currency` 一旦丢了就会兜底成 CNY，所以第 1 步必须
 * 优先于第 2 步，而第 1 步的数据又依赖转发层没把 `amounts` 吃掉。
 *
 * 第 3 步的 `list` 必须是带 status 的完整发票行，且凭证字段至少有一个非空
 * （服务端给 `filePath`、设备本地镜像给 `fileName`，见 invoiceCountsTowardTotal
 * 的注释）：兜底重算要按与服务端相同的判据过滤，只给 `{amount, currency}` 会让
 * 类型系统放行一个必然算错的调用（那样算出来的是全表求和，正是 61,500 的来源）。
 */
export function resolveSummaryGroups(
  totals: Pick<InvoiceTotals, 'amount' | 'currency' | 'amounts'> | undefined,
  list: Array<{
    amount: number | string
    currency?: string | null
    status: string
    filePath?: string | null
    fileName?: string | null
  }>,
): CurrencyAmount[] {
  if (totals?.amounts?.length) {
    return totals.amounts.map((a) => ({
      currency: normalizeCurrency(a.currency),
      amount: round2(a.amount),
    }))
  }
  if (totals?.amount) {
    return [{ currency: normalizeCurrency(totals.currency), amount: round2(totals.amount) }]
  }
  return sumByCurrency(list)
}
