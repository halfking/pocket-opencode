import type { Statement, SyncDb } from './contract.ts'
import type { RecordModule } from './epochs.ts'

type Module = 'notes' | RecordModule
export const OUTBOX_LIMIT = 500
export const capacityTables = [
  `CREATE TABLE IF NOT EXISTS local_portal_outbox_capacity_guards(
    allowed INTEGER NOT NULL CONSTRAINT portal_outbox_capacity CHECK(allowed=1))`,
  `CREATE TABLE IF NOT EXISTS local_portal_outbox_retention(
    owner_user_id TEXT NOT NULL,owner_workspace_id TEXT NOT NULL,module TEXT NOT NULL,evicted_count INTEGER NOT NULL,
    PRIMARY KEY(owner_user_id,owner_workspace_id,module))`,
]

/** Append to the same source/projection/outbox transaction, after its inserts. */
export function outboxCapacityStatements(db: SyncDb, module: Module): Statement[] {
  if (!db.owner) return []
  const owner = [db.owner.userId,db.owner.workspaceId], notes = module === 'notes'
  const table = notes ? 'local_portal_outbox' : 'local_portal_record_outbox'
  const scope = `owner_user_id=? AND owner_workspace_id=?${notes?'':' AND module=?'}`
  const values = notes ? owner : [...owner,module]
  const victims = `SELECT rowid FROM ${table} WHERE ${scope}
    AND last_error<>'' AND last_error NOT IN ('policy_local','reset_pending','reset_epoch_mismatch','epoch_unknown')
    AND substr(last_error,1,6)<>'clock_'
    AND reset_epoch=(SELECT epoch FROM local_portal_sync_epochs WHERE owner_user_id=? AND owner_workspace_id=? AND module=?)
    AND NOT EXISTS(SELECT 1 FROM local_portal_resets WHERE owner_user_id=? AND owner_workspace_id=? AND completed=0 AND instr(modules_json,?)>0)
    ORDER BY rowid LIMIT MAX(0,(SELECT COUNT(*) FROM ${table} WHERE ${scope})-${OUTBOX_LIMIT})`
  const victimValues = [...values,...owner,module,...owner,JSON.stringify(module),...values]
  return [
    {statement:`INSERT INTO local_portal_outbox_retention(owner_user_id,owner_workspace_id,module,evicted_count)
      SELECT ?,?,?,COUNT(*) FROM ${table} WHERE rowid IN (${victims}) HAVING COUNT(*)>0
      ON CONFLICT(owner_user_id,owner_workspace_id,module) DO UPDATE SET evicted_count=evicted_count+excluded.evicted_count`,
      values:[...owner,module,...victimValues]},
    {statement:`DELETE FROM ${table} WHERE rowid IN (${victims})`,values:victimValues},
    {statement:`INSERT INTO local_portal_outbox_capacity_guards(allowed) SELECT 0 WHERE (SELECT COUNT(*) FROM ${table} WHERE ${scope})>?`,
      values:[...values,OUTBOX_LIMIT]},
  ]
}

export async function outboxEvictedCount(db: SyncDb, module: Module): Promise<number> {
  if (!db.owner) return 0
  const row = await db.queryOne<{evicted_count:number}>(`SELECT evicted_count FROM local_portal_outbox_retention
    WHERE owner_user_id=? AND owner_workspace_id=? AND module=?`,[db.owner.userId,db.owner.workspaceId,module])
  db.assertCurrent()
  return row?.evicted_count ?? 0
}
export async function publishOutboxRetention(db: SyncDb, module: Module): Promise<void> {
  if (typeof window === 'undefined') return
  const evictedCount = await outboxEvictedCount(db,module)
  if (evictedCount) window.dispatchEvent(new CustomEvent('openpocket:portal-outbox-retention',
    {detail:{module,owner:db.owner,evictedCount}}))
}
export function capacityFailure(cause: unknown): void {
  if (String(cause).includes('portal_outbox_capacity'))
    throw Object.assign(new Error('Sync queue is full; sync or resolve retained operations before saving again'),{cause})
}
