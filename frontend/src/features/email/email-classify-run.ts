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

/** 连续多少轮「一封都没归类成功」就判定链路跑不通并停下。 */
export const MAX_NO_PROGRESS_PASSES = 2

export interface ClassifyRunStep {
  /** 本轮成功归类的邮件数 */
  classified: number
  /** 本轮结束后仍未归类的邮件数 */
  remaining: number
  /** 用户是否点了取消 */
  cancel: boolean
  /** 此前连续零进展的轮数 */
  noProgressPasses: number
}

export type ClassifyRunVerdict =
  | { kind: 'done' }
  | { kind: 'cancelled' }
  | { kind: 'stalled'; noProgressPasses: number }
  | { kind: 'continue'; noProgressPasses: number }

/**
 * 归类循环的终止判据。
 *
 * 原实现的终止条件只有 `remaining <= 0`，也就是**默认分类一定会成功**。
 * 没配 LLM provider 时（`POCKET_LLM_API_KEY` / kxmemory 都没有），服务端
 * 每轮 `classified` 恒为 0、`remaining` 恒等于总数：
 *
 *   · UI 永远停在「正在归类 1/120」（Math.max(1, 0) 把 0 显示成 1）；
 *   · 循环以网络速度无限重发，服务端每轮被打一遍并刷 20 行
 *     `[email/classify] …: llmbff: no provider configured`；
 *   · 只有用户手动点「取消」才会停。
 *
 * 2026-10-02 在模拟器上实测到这个空转：刷新一次收件箱，服务端日志每分钟
 * 多出上百行相同错误，界面进度条纹丝不动。
 *
 * 这里补一条**零进展即停**：连着 MAX_NO_PROGRESS_PASSES 轮一封都没归类成功，
 * 说明分类链路根本跑不通，再刷一百轮也不会有结果。留两轮而不是一轮，是为了
 * 容忍单次网络抖动。
 *
 * 停下后由调用方如实告诉用户「归类未生效、还有 N 封未归类」，
 * 而不是让一个永远转不动的进度条挂在界面上。
 */
export function classifyRunVerdict(step: ClassifyRunStep): ClassifyRunVerdict {
  if (step.cancel) return { kind: 'cancelled' }
  if (step.remaining <= 0) return { kind: 'done' }
  const noProgressPasses = step.classified > 0 ? 0 : step.noProgressPasses + 1
  if (noProgressPasses >= MAX_NO_PROGRESS_PASSES) return { kind: 'stalled', noProgressPasses }
  return { kind: 'continue', noProgressPasses }
}
