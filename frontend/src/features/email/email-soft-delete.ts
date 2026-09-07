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

export async function purgeEmailsLocal(ids: string[], now = Date.now()): Promise<number> {
  const { localDB } = await import('../../native/local-db')
  const { clearEmailBodyLocal } = await import('./email-body-cache')
  let n = 0
  for (const id of ids) {
    if (!id) continue
    const row = await localDB.queryOne<{ subject: string | null; snippet: string | null; ai_summary: string | null }>(
      'SELECT subject, snippet, ai_summary FROM local_emails WHERE id = ?',
      [id],
    )
    if (!row) continue
    const patch = buildPurgePatch({ subject: row.subject, snippet: row.snippet, aiSummary: row.ai_summary }, now)
    await localDB.run(
      'UPDATE local_emails SET snippet = ?, ai_summary = ?, deleted_at = ?, body_purged = 1, updated_at = ? WHERE id = ?',
      [patch.snippet, patch.aiSummary, patch.deletedAt, now, id],
    )
    await clearEmailBodyLocal(id)
    n++
  }
  return n
}
