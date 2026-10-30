import { newOperationId, versionKey, type PortalAck, type PortalItem, type SyncDb } from './contract.ts'
import type { RecordItem } from './record-contract.ts'
import { epochGuard } from './epoch-state.ts'
import { recordEpochGuard } from './record-epoch.ts'
import { validEpoch, type RecordModule } from './epochs.ts'

type Item = PortalItem | RecordItem
type SyncModule = 'notes' | RecordModule
interface Codec<T extends Item> {
  encode(item: T): Promise<string>
  decode(json: string): Promise<T>
}
function tables(db: SyncDb, module: SyncModule) {
  if (!db.owner) throw new Error('Verified clock correction owner required')
  const notes = module === 'notes', owner = [db.owner.userId, db.owner.workspaceId]
  return {
    notes,
    outbox: notes ? 'local_portal_outbox' : 'local_portal_record_outbox',
    projection: notes ? 'local_portal_notes' : 'local_portal_records',
    scope: notes ? 'owner_user_id=? AND owner_workspace_id=?' : 'owner_user_id=? AND owner_workspace_id=? AND module=?',
    values: notes ? owner : [...owner, module],
    guard: (epoch: number) => notes ? epochGuard(db, epoch) : recordEpochGuard(db, module, epoch),
  }
}

/** Only the server's original authoritative time can correct a clock rejection. */
export async function acknowledgeClock<T extends Item>(db: SyncDb, item: T, ack: PortalAck, codec: Codec<T>): Promise<void> {
  const t = tables(db, item.module), guard = t.guard(item.epoch!)
  let state = 'clock_skew_missing_time'
  try {
    if (typeof ack.client_updated_at === 'string') {
      versionKey(ack.client_updated_at)
      const timestamp = new Date(Date.parse(ack.client_updated_at) + 1).toISOString()
      state = `clock_skew:${timestamp}`
    }
  } catch { /* Invalid acknowledgements require an explicit subsequent edit. */ }
  await db.run(`UPDATE ${t.outbox} SET last_error=CASE WHEN last_error='clock_retry' THEN 'clock_skew_failed' ELSE ? END
    WHERE ${t.scope} AND op_id=? AND reset_epoch=? AND last_error IN ('','clock_retry') AND ${guard.clause}`,
  [state, ...t.values, item.op_id, item.epoch, ...guard.values])
  await resumeClockCorrections(db, item.module, codec)
}

/** Resume after interruption; a replacement, projection update and removal are atomic. */
export async function resumeClockCorrections<T extends Item>(db: SyncDb, module: SyncModule, codec: Codec<T>): Promise<void> {
  const t = tables(db, module)
  const rows = await db.query<{ item_json: string; last_error: string }>(
    `SELECT item_json,last_error FROM ${t.outbox} WHERE ${t.scope} AND last_error LIKE 'clock_skew:%' ORDER BY rowid LIMIT 50`, t.values)
  for (const row of rows) {
    const old = await codec.decode(row.item_json)
    if (!validEpoch(old.epoch)) continue
    const guard = t.guard(old.epoch), timestamp = row.last_error.slice('clock_skew:'.length)
    const item = { ...old, client_updated_at: timestamp, op_id: newOperationId() }, json = await codec.encode(item)
    const identity = `${t.scope} AND ext_id=?${t.notes ? '' : ' AND kind=?'} AND op_id=? AND version_key=? AND reset_epoch=?`
    const identityValues = [...t.values, old.ext_id, ...(t.notes ? [] : [old.kind]), old.op_id, versionKey(old.client_updated_at), old.epoch]
    const admitted = `EXISTS(SELECT 1 FROM ${t.projection} WHERE ${identity})
      AND EXISTS(SELECT 1 FROM ${t.outbox} WHERE ${t.scope} AND op_id=? AND last_error=? AND reset_epoch=?) AND ${guard.clause}`
    const values = [...identityValues, ...t.values, old.op_id, row.last_error, old.epoch, ...guard.values]
    await db.runInTransaction([
      { statement: `INSERT INTO ${t.outbox}(owner_user_id,owner_workspace_id,${t.notes ? '' : 'module,kind,'}op_id,ext_id,item_json,reset_epoch,last_error)
          SELECT ${t.notes ? '?,?,?,?,?,?' : '?,?,?,?,?,?,?,?'},'clock_retry' WHERE ${admitted}`,
        values: [...t.values, ...(t.notes ? [] : [old.kind]), item.op_id, item.ext_id, json, item.epoch, ...values] },
      { statement: `UPDATE ${t.projection} SET item_json=?,op_id=?,version_key=?${t.notes ? ',version_ms=?' : ''}
          WHERE ${identity} AND EXISTS(SELECT 1 FROM ${t.outbox} WHERE ${t.scope} AND op_id=?) AND ${guard.clause}`,
        values: [json, item.op_id, versionKey(timestamp), ...(t.notes ? [Date.parse(timestamp)] : []), ...identityValues, ...t.values, item.op_id, ...guard.values] },
      { statement: `DELETE FROM ${t.outbox} WHERE ${t.scope} AND op_id=? AND last_error=? AND
          EXISTS(SELECT 1 FROM ${t.outbox} WHERE ${t.scope} AND op_id=?) AND ${guard.clause}`,
        values: [...t.values, old.op_id, row.last_error, ...t.values, item.op_id, ...guard.values] },
      { statement: `UPDATE ${t.outbox} SET last_error='clock_skew_superseded' WHERE ${t.scope} AND op_id=? AND last_error=?
          AND NOT EXISTS(SELECT 1 FROM ${t.projection} WHERE ${identity}) AND ${guard.clause}`,
        values: [...t.values, old.op_id, row.last_error, ...identityValues, ...guard.values] },
    ])
  }
}
