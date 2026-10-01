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

/** 单次归类默认的轮次上限（每轮 20 封 ⇒ 一次最多 400 封）。 */
export const DEFAULT_CLASSIFY_MAX_ROUNDS = 20

export interface ClassifyRoundState {
  /** 已完成的轮次（从 1 起）。 */
  round: number
  maxRounds: number
  /** 服务端报告的剩余未归类数。 */
  remaining: number
  cancelled: boolean
  /** 本轮返回的结果条数。 */
  rowCount: number
  /** 其中带 error 的条数。 */
  errorCount: number
}

/**
 * 本轮之后是否还要再发一次请求。
 *
 * 2026-10-02 实测的缺陷：原实现只靠 `cancelled` 与 `remaining <= 0` 退出。
 * 分类器是**逐封调 LLM** 的，失败是常态（网关没配 key、凭据错、模型名不对），
 * 而 `/api/emails/classify` 在逐封失败时仍返回 200、`remaining` 保持不变
 * ⇒ 无限重试。用户看到的只是转不完的"正在归类…"。
 *
 * 三个终止条件缺一不可：
 *   - 用户中止；
 *   - 队列清空；
 *   - **整批全失败**（分类器整体不可用，重试无意义，只烧 API 配额）；
 * 再加一道轮次上限兜底，防止「部分成功 + remaining 缓慢下降」时的长尾。
 */
export function shouldContinueClassify(s: ClassifyRoundState): boolean {
  if (s.cancelled) return false
  if (s.remaining <= 0) return false
  if (s.rowCount > 0 && s.errorCount === s.rowCount) return false
  return s.round < s.maxRounds
}

export interface ClassifyDoneHintInput {
  leftover: number
  firstError?: string
  allFailed: boolean
  hitMaxRounds: boolean
  maxRounds: number
  perRound: number
}

/**
 * 归类结束后的提示文案。
 *
 * 关键点：逐封 `result.error` 此前**从未**被渲染——提示只在 HTTP 整体抛错时
 * 才出现，而后端逐封失败返回的是 200。于是「网关没配 key」与「邮件没被归类」
 * 在界面上长得一模一样：什么都没有。
 */
export function classifyDoneHint(i: ClassifyDoneHintInput): string {
  if (i.leftover <= 0) return '归类完成'
  const suffix = `（仍有 ${i.leftover} 封未归类）`
  if (i.allFailed && i.firstError) return `归类失败：${i.firstError}${suffix}`
  if (i.firstError) return `部分归类失败：${i.firstError}${suffix}`
  if (i.hitMaxRounds) return `已暂停，仍有 ${i.leftover} 封未归类（达到单次上限 ${i.maxRounds * i.perRound} 封）`
  return `已暂停，仍有 ${i.leftover} 封未归类`
}
