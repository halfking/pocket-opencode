/**
 * finance.ts — 记账 API（自动记账 + 手动记账）。
 *
 * 数据在服务端 PostgreSQL（finance_transactions）；来源 source:
 *   manual 手动 | voice 语音解析 | auto 笔记自动记账。
 */
import { http } from './http'

export type FinanceTxType = 'income' | 'expense'
export type FinanceTxSource = 'manual' | 'voice' | 'auto'

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
}

export const financeApi = {
  list(): Promise<{ transactions: FinanceTransaction[]; total: number }> {
    return http('/api/finance')
  },
  stats(month?: string, category?: string): Promise<FinanceStats> {
    const qs = new URLSearchParams()
    if (month) qs.set('month', month)
    if (category) qs.set('category', category)
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
