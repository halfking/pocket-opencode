/** 批量清垃圾的前端过滤条件（日期为 Unix 秒，对齐 emails.date）。 */
export interface CleanupFilter {
  accountId?: string
  subject?: string
  from?: string
  since?: number
  until?: number
}

export interface CleanupMatchInput {
  fromAddress?: string | null
  fromName?: string | null
  subject?: string | null
  date: number
  accountId?: string
}

export function hasCleanupConstraint(f: CleanupFilter): boolean {
  return !!(f.subject?.trim() || f.from?.trim() || (f.since && f.since > 0) || (f.until && f.until > 0))
}

export function matchCleanup(e: CleanupMatchInput, f: CleanupFilter): boolean {
  if (f.accountId && e.accountId && e.accountId !== f.accountId) return false
  const subject = (e.subject ?? '').toLowerCase()
  const fromHay = `${e.fromAddress ?? ''} ${e.fromName ?? ''}`.toLowerCase()
  if (f.subject?.trim() && !subject.includes(f.subject.trim().toLowerCase())) return false
  if (f.from?.trim() && !fromHay.includes(f.from.trim().toLowerCase())) return false
  const sec = emailDateToSec(e.date)
  if (f.since && f.since > 0 && sec < f.since) return false
  if (f.until && f.until > 0 && sec > f.until) return false
  return true
}

/** IMAP/PG 存 Unix 秒；本地库偶发毫秒。秒级时间戳 < 1e12。 */
export function emailDateToMs(value: number): number {
  if (!Number.isFinite(value) || value <= 0) return 0
  return value < 1e12 ? value * 1000 : value
}

export function emailDateToSec(value: number): number {
  if (!Number.isFinite(value) || value <= 0) return 0
  return value < 1e12 ? Math.floor(value) : Math.floor(value / 1000)
}

export function formatEmailRelTime(value: number, nowMs = Date.now()): string {
  const ms = emailDateToMs(value)
  if (ms <= 0) return ''
  const diff = nowMs - ms
  const hr = Math.floor(diff / 3_600_000)
  if (hr < 1) return `${Math.max(0, Math.floor(diff / 60_000))}分钟前`
  if (hr < 24) return `${hr}小时前`
  return `${Math.floor(hr / 24)}天前`
}
