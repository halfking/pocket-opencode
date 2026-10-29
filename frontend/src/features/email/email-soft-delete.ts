import { localDB } from '../../native/local-db'
import { buildPurgePatch } from './email-purge-patch'
import type { EmailSyncOperation } from './email-sync-guard'
import { capturePortalMailWrite, commitPortalMail, mailRecordId, portalMailStore } from './portal-mail-store'
export { buildPurgePatch, shouldSkipSyncWrite } from './email-purge-patch'
export type { PurgeSource, PurgePatch } from './email-purge-patch'

export async function purgeEmailsLocal(ids: string[], now = Date.now(), operation?: EmailSyncOperation, database?: object): Promise<number> {
  const write = capturePortalMailWrite(operation?.db ?? database)
  const { db, scope } = write
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
    const sourceStamp = operation ? row.updated_at : now
    const statement = { statement: `UPDATE local_emails SET snippet = ?, ai_summary = ?, deleted_at = ?, body_purged = 1, updated_at = ? WHERE id=? AND ${scope.clause} AND updated_at IS ?`,
      values: [patch.snippet, patch.aiSummary, patch.deletedAt, sourceStamp, id, ...scope.params, row.updated_at] }
    if (operation) {
      // The old pocketd tombstone has no original modification version. Stop
      // publishing its stale snapshot rather than fabricating the receive time.
      await portalMailStore(db).init()
      await db.runInTransaction([statement, { statement: `UPDATE local_portal_record_outbox SET last_error='source_deletion_version_unknown'
        WHERE owner_user_id=? AND owner_workspace_id=? AND module='email' AND ext_id=?`, values: [...write.ownerValues, mailRecordId('summary', id)] }])
    } else {
      await commitPortalMail(write, [statement], { kind: 'summary', id, title: row.subject ?? '', payload: {}, deleted: true, modifiedAt: now,
        guard: { clause: `EXISTS(SELECT 1 FROM local_emails WHERE id=? AND ${scope.clause} AND deleted_at=? AND updated_at=?)`, values: [id, ...scope.params, now, now] } })
    }
    assertCurrent()
    await clearEmailBodyLocal(id)
    n++
  }
  return n
}
