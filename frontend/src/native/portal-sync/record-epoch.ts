import type { Statement, SyncDb } from './contract.ts'
import { rememberModuleEpoch, validEpoch, type RecordModule } from './epochs.ts'

const scope = (db: SyncDb) => {
  if (!db.owner) throw new Error('Verified module owner required')
  return [db.owner.userId, db.owner.workspaceId]
}
export async function currentRecordEpoch(db: SyncDb, module: RecordModule): Promise<number | null> {
  if (!db.owner) return null
  const row = await db.queryOne<{ epoch: number }>(
    'SELECT epoch FROM local_portal_sync_epochs WHERE owner_user_id=? AND owner_workspace_id=? AND module=?',
    [...scope(db), module],
  )
  return validEpoch(row?.epoch) ? row.epoch : null
}
export function recordEpochGuard(db: SyncDb, module: RecordModule, epoch: number) {
  return {
    clause: `(EXISTS(SELECT 1 FROM local_portal_sync_epochs WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND epoch=?)
      AND NOT EXISTS(SELECT 1 FROM local_portal_resets WHERE owner_user_id=? AND owner_workspace_id=? AND completed=0 AND instr(modules_json,?)>0))`,
    values: [...scope(db), module, epoch, ...scope(db), JSON.stringify(module)],
  }
}
export function recordEpochStatements(db: SyncDb, module: RecordModule, epoch: number): Statement[] {
  if (!validEpoch(epoch)) throw new Error('Invalid module reset epoch')
  const owner = scope(db)
  return [
    {
      statement: `DELETE FROM local_portal_cursors WHERE owner_user_id=? AND owner_workspace_id=? AND module=?
        AND COALESCE((SELECT epoch FROM local_portal_sync_epochs WHERE owner_user_id=? AND owner_workspace_id=? AND module=?),-1)<?`,
      values: [...owner, module, ...owner, module, epoch],
    },
    {
      statement: `INSERT INTO local_portal_sync_epochs(owner_user_id,owner_workspace_id,module,epoch) VALUES(?,?,?,?)
        ON CONFLICT(owner_user_id,owner_workspace_id,module) DO UPDATE SET epoch=MAX(local_portal_sync_epochs.epoch,excluded.epoch)`,
      values: [...owner, module, epoch],
    },
    {
      statement: `UPDATE local_portal_record_outbox SET last_error=CASE WHEN reset_epoch IS NULL THEN 'epoch_unknown' ELSE 'reset_epoch_mismatch' END
        WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND (reset_epoch IS NULL OR reset_epoch<>(SELECT epoch FROM local_portal_sync_epochs WHERE owner_user_id=? AND owner_workspace_id=? AND module=?))`,
      values: [...owner, module, ...owner, module],
    },
  ]
}
export async function observeRecordEpoch(db: SyncDb, module: RecordModule, epoch: number): Promise<boolean> {
  if (!validEpoch(epoch)) throw new Error('Invalid module reset epoch')
  const old = await currentRecordEpoch(db, module)
  if (old !== null && old > epoch) throw new Error('Stale module reset response; local original retained')
  if (old === epoch) {
    rememberModuleEpoch(db.owner, module, epoch)
    return false
  }
  await db.runInTransaction(recordEpochStatements(db, module, epoch))
  if ((await currentRecordEpoch(db, module)) !== epoch) throw new Error('Module epoch changed during synchronization')
  rememberModuleEpoch(db.owner, module, epoch)
  return true
}
