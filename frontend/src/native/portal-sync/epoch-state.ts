import { rememberNotesEpoch, validEpoch } from './epochs.ts'
import type { Statement, SyncDb } from './contract.ts'

export const epochTables = [
  `CREATE TABLE IF NOT EXISTS local_portal_sync_epochs (
    owner_user_id TEXT NOT NULL,owner_workspace_id TEXT NOT NULL,module TEXT NOT NULL,epoch INTEGER NOT NULL,
    PRIMARY KEY(owner_user_id,owner_workspace_id,module))`,
  `CREATE TABLE IF NOT EXISTS local_portal_resets (
    owner_user_id TEXT NOT NULL,owner_workspace_id TEXT NOT NULL,reset_id TEXT NOT NULL,
    modules_json TEXT NOT NULL,blocks_notes INTEGER NOT NULL,completed INTEGER NOT NULL DEFAULT 0,response_json TEXT,
    PRIMARY KEY(owner_user_id,owner_workspace_id,reset_id))`,
  `CREATE UNIQUE INDEX IF NOT EXISTS idx_portal_pending_reset ON local_portal_resets
    (owner_user_id,owner_workspace_id,modules_json) WHERE completed=0`,
]

function scope(db: SyncDb): string[] {
  if (!db.owner) throw new Error('Verified reset owner required')
  return [db.owner.userId, db.owner.workspaceId]
}

export async function currentNotesEpoch(db: SyncDb): Promise<number | null> {
  if (!db.owner) return null
  const row = await db.queryOne<{ epoch: number }>(`SELECT epoch FROM local_portal_sync_epochs WHERE owner_user_id=? AND owner_workspace_id=? AND module='notes'`, scope(db))
  return validEpoch(row?.epoch) ? row.epoch : null
}

export function epochGuard(db: SyncDb, epoch: number): { clause: string; values: unknown[] } {
  return {
    clause: `(EXISTS(SELECT 1 FROM local_portal_sync_epochs e WHERE e.owner_user_id=? AND e.owner_workspace_id=? AND e.module='notes' AND e.epoch=?)
      AND NOT EXISTS(SELECT 1 FROM local_portal_resets r WHERE r.owner_user_id=? AND r.owner_workspace_id=? AND r.blocks_notes=1 AND r.completed=0))`,
    values: [...scope(db), epoch, ...scope(db)],
  }
}

/** Reset metadata and block old operations; never rewrite their payload/time/id/epoch. */
export function notesEpochStatements(db: SyncDb, epoch: number): Statement[] {
  if (!validEpoch(epoch)) throw new Error('Invalid notes reset epoch')
  const owner = scope(db)
  return [
    { statement: `DELETE FROM local_portal_cursors WHERE owner_user_id=? AND owner_workspace_id=? AND module='notes'
       AND COALESCE((SELECT epoch FROM local_portal_sync_epochs WHERE owner_user_id=? AND owner_workspace_id=? AND module='notes'),-1) < ?`, values: [...owner, ...owner, epoch] },
    { statement: `INSERT INTO local_portal_sync_epochs(owner_user_id,owner_workspace_id,module,epoch) VALUES (?,?,'notes',?)
       ON CONFLICT(owner_user_id,owner_workspace_id,module) DO UPDATE SET epoch=MAX(local_portal_sync_epochs.epoch,excluded.epoch)`, values: [...owner, epoch] },
    { statement: `UPDATE local_portal_outbox SET last_error=CASE WHEN reset_epoch IS NULL THEN 'epoch_unknown' ELSE 'reset_epoch_mismatch' END
       WHERE owner_user_id=? AND owner_workspace_id=? AND (reset_epoch IS NULL OR reset_epoch<>(SELECT epoch FROM local_portal_sync_epochs WHERE owner_user_id=? AND owner_workspace_id=? AND module='notes'))`, values: [...owner, ...owner] },
  ]
}

export async function observeNotesEpoch(db: SyncDb, epoch: number): Promise<boolean> {
  if (!validEpoch(epoch)) throw new Error('Invalid notes reset epoch')
  const old = await currentNotesEpoch(db)
  if (old !== null && old > epoch) throw new Error('Stale portal reset response; local notes retained')
  if (old === epoch) { rememberNotesEpoch(db.owner, epoch); return false }
  await db.runInTransaction(notesEpochStatements(db, epoch))
  const current = await currentNotesEpoch(db)
  if (current !== epoch) throw new Error('Notes reset changed during synchronization')
  rememberNotesEpoch(db.owner, epoch)
  return old !== epoch
}
