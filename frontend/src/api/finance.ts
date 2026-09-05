/**
 * finance.ts — 记账 API（自动记账 + 手动记账）。
 *
 * 数据在服务端 PostgreSQL（finance_transactions）；来源 source:
 *   manual 手动 | voice 语音解析 | auto 笔记自动记账 | invoice 发票入账。
 */
import { http } from './http'

export type FinanceTxType = 'income' | 'expense'
export type FinanceTxSource = 'manual' | 'voice' | 'auto' | 'invoice'

export interface FinanceTransaction {
  id: string
  type: FinanceTxType
  amount: number
  category: string
  note?: string
  tags?: string[]
  project_id?: string
  source: FinanceTxSource
  created_at: string
}

export interface FinanceStats {
  month: string
  total_income: number
  total_expense: number
  balance: number
  by_category: Record<string, number>
  count: number
}

export interface FinanceParseResult {
  type: FinanceTxType
  amount: number
  category: string
  note: string
}

export interface FinanceCreateInput {
  type: FinanceTxType
  amount: number
  category: string
  note?: string
  source?: FinanceTxSource
  /**
   * 幂等键（对齐后端 note_ref 列，snake_case）：同 owner+workspace 下非空且已存在时
   * 返回既有记录而不重复入账。发票入账用 `invoice:<id>`，防归档失败重试/重复点击双记账。
   */
  note_ref?: string
}

export const financeApi = {
  list(): Promise<{ transactions: FinanceTransaction[]; total: number }> {
    return http('/api/finance')
  },
  /**
   * 月度统计。tzOffsetMinutes 为本地时区相对 UTC 的分钟偏移（-getTimezoneOffset()，
   * 东八区=480）；显式传给服务端按用户本地日历月分桶，避免跨时区统计错位。
   */
  stats(month?: string, category?: string, tzOffsetMinutes?: number): Promise<FinanceStats> {
    const qs = new URLSearchParams()
    if (month) qs.set('month', month)
    if (category) qs.set('category', category)
    if (tzOffsetMinutes !== undefined) qs.set('tz', String(tzOffsetMinutes))
    const q = qs.toString()
    return http(`/api/finance/stats${q ? `?${q}` : ''}`)
  },
  create(input: FinanceCreateInput): Promise<FinanceTransaction> {
    return http('/api/finance', { method: 'POST', body: JSON.stringify(input) })
  },
  remove(id: string): Promise<void> {
    return http(`/api/finance/${id}`, { method: 'DELETE' })
  },
  /** 自然语言解析（不落库）：「打车花了 32 元」→ expense/交通/32。 */
  parse(text: string): Promise<FinanceParseResult> {
    return http('/api/finance/parse', { method: 'POST', body: JSON.stringify({ text }) })
  },
}
