import { emailDateToMs } from './cleanup-filter.ts'

export interface InboxSearch {
  q?: string
  from?: string
  subject?: string
  sinceMs?: number
  untilMs?: number
}

export interface InboxSearchMail {
  fromAddress?: string | null
  fromName?: string | null
  subject?: string | null
  snippet?: string | null
  aiSummary?: string | null
  date: number
}

export function hasInboxSearch(s: InboxSearch): boolean {
  return !!(
    s.q?.trim()
    || s.from?.trim()
    || s.subject?.trim()
    || (s.sinceMs && s.sinceMs > 0)
    || (s.untilMs && s.untilMs > 0)
  )
}

export function matchInboxSearch(e: InboxSearchMail, s: InboxSearch): boolean {
  if (!hasInboxSearch(s)) return true
  const fromHay = `${e.fromAddress ?? ''} ${e.fromName ?? ''}`.toLowerCase()
  const subject = (e.subject ?? '').toLowerCase()
  const bodyHay = `${e.snippet ?? ''} ${e.aiSummary ?? ''}`.toLowerCase()
  if (s.q?.trim()) {
    const q = s.q.trim().toLowerCase()
    if (!fromHay.includes(q) && !subject.includes(q) && !bodyHay.includes(q)) return false
  }
  if (s.from?.trim() && !fromHay.includes(s.from.trim().toLowerCase())) return false
  if (s.subject?.trim() && !subject.includes(s.subject.trim().toLowerCase())) return false
  const ms = emailDateToMs(e.date)
  if (s.sinceMs && s.sinceMs > 0 && ms < s.sinceMs) return false
  if (s.untilMs && s.untilMs > 0 && ms > s.untilMs) return false
  return true
}

/** 导航栏中间的缩略条件；空表示未搜索。 */
export function formatInboxSearchLabel(s: InboxSearch, max = 18): string {
  if (!hasInboxSearch(s)) return ''
  const parts: string[] = []
  if (s.q?.trim()) parts.push(s.q.trim())
  if (s.from?.trim()) parts.push(s.from.trim())
  if (s.subject?.trim()) parts.push(s.subject.trim())
  if ((s.sinceMs && s.sinceMs > 0) || (s.untilMs && s.untilMs > 0)) {
    const a = s.sinceMs && s.sinceMs > 0 ? new Date(s.sinceMs).toISOString().slice(5, 10) : ''
    const b = s.untilMs && s.untilMs > 0 ? new Date(s.untilMs).toISOString().slice(5, 10) : ''
    parts.push([a, b].filter(Boolean).join('~') || '日期')
  }
  const joined = parts.join(' · ')
  return joined.length > max ? `${joined.slice(0, max - 1)}…` : joined
}
