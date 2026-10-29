import { captureEmailOwnership } from './email-ownership'
import { localDB } from '../../native/local-db'
import { buildPurgePatch } from './email-purge-patch'
import type { EmailSyncOperation } from './email-sync-guard'
export { buildPurgePatch, shouldSkipSyncWrite } from './email-purge-patch'
export type { PurgeSource, PurgePatch } from './email-purge-patch'

export async function purgeEmailsLocal(ids: string[], now = Date.now(), operation?: EmailSyncOperation, database?: object): Promise<number> {
  const { db, scope } = captureEmailOwnership(operation?.db ?? database)
  const assertCurrent = () => { db.assertCurrent(); operation?.assertCurrent() }
  const { clearEmailBodyLocal } = await import('./email-body-cache')
  assertCurrent()
  let n = 0
  for (const id of ids) {
    if (!id) continue
    const row = await db.queryOne<{ subject: string | null; snippet: string | null; ai_summary: string | null; updated_at: number }>(
      `SELECT subject, snippet, ai_summary, updated_at FROM local_emails WHERE id = ? AND ${scope.clause}`,
      [id, ...scope.params],
    )
    if (!row) continue
    const patch = buildPurgePatch({ subject: row.subject, snippet: row.snippet, aiSummary: row.ai_summary }, now)
    assertCurrent()
    // deletedIds carries no source version; cloud removal must not advance it
    // to the local download clock. Local user deletion is an actual local edit.
    await db.run(
      `UPDATE local_emails SET snippet = ?, ai_summary = ?, deleted_at = ?, body_purged = 1, updated_at = ? WHERE id = ? AND ${scope.clause}`,
      [patch.snippet, patch.aiSummary, patch.deletedAt, operation ? row.updated_at : now, id, ...scope.params],
    )
    assertCurrent()
    await clearEmailBodyLocal(id)
    n++
  }
  return n
}
