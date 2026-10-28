export interface PurgeSource {
  subject?: string | null
  snippet?: string | null
  aiSummary?: string | null
}

export interface PurgePatch {
  subject: string
  snippet: string
  aiSummary: string
  deletedAt: number
  bodyPurged: true
}

export function buildPurgePatch(src: PurgeSource, now: number): PurgePatch {
  const summary = (src.aiSummary ?? '').trim() || (src.snippet ?? '').trim()
  return {
    subject: src.subject ?? '',
    snippet: '',
    aiSummary: summary,
    deletedAt: now,
    bodyPurged: true,
  }
}

export function shouldSkipSyncWrite(row: { deletedAt?: number | null; bodyPurged?: boolean }): boolean {
  return !!(row.bodyPurged || (row.deletedAt && row.deletedAt > 0))
}
