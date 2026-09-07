import { normalizeEmailCategory } from './email-categories.ts'

export interface ClassifyCandidate {
  id: string
  category?: string | null
  date: number
}

export interface ClassifyResult {
  emailId: string
  category?: string | null
  importance?: string | null
  summary?: string | null
}

export function isUncategorized(category: string | null | undefined): boolean {
  return !category
}

export function nextClassifyBatch(list: ClassifyCandidate[], limit = 20): string[] {
  return list
    .filter((e) => isUncategorized(e.category))
    .sort((a, b) => b.date - a.date)
    .slice(0, Math.max(0, limit))
    .map((e) => e.id)
}

export function applyClassifyResult<T extends { id: string; category: string | null; importance: string | null; aiSummary: string | null }>(
  row: T,
  result: ClassifyResult,
): T {
  if (row.id !== result.emailId) return row
  return {
    ...row,
    category: normalizeEmailCategory(result.category) || row.category,
    importance: result.importance ?? row.importance,
    aiSummary: result.summary ?? row.aiSummary,
  }
}

export function classifyProgressLabel(done: number, total: number): string {
  return `正在归类 ${done}/${total}`
}
