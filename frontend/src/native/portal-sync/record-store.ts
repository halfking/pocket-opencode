import { localVersionOperationId, newOperationId, versionKey, type PortalAck, type Statement, type SyncDb } from './contract.ts'
import { PortalNotesStore } from './store.ts'
import { currentRecordEpoch, observeRecordEpoch, recordEpochGuard } from './record-epoch.ts'
import { validateRecordItem, type RecordAdapter, type RecordItem } from './record-contract.ts'
import { validEpoch } from './epochs.ts'
import { acknowledgeClock, resumeClockCorrections } from './clock-retry.ts'

export interface RecordProjection {
  source_id: string
  item_json: string
  version_key: string
  op_id: string
  reset_epoch: number | null
}
export interface RecordSnapshot {
  id: string
  extId?: string
  kind?: string
  title: string
  payload: Record<string, unknown>
  modifiedAt: number
  epoch: number | null
  deleted?: boolean
  syncError?: string
  guard?: { clause: string; values: unknown[] }
}
interface PreparedRecord {
  identity: string
  assertion: Statement
  statements: Statement[]
}
const conflictMessage = 'Module changed during this edit; original retained, keep your changes and retry the edit'

export class PortalRecordStore {
  constructor(
    readonly db: SyncDb,
    readonly adapter: RecordAdapter,
    readonly encode: (item: RecordItem) => Promise<string> = async (item) => JSON.stringify(item),
    readonly decode: (json: string) => Promise<RecordItem> = async (json) => JSON.parse(json),
  ) {}
  get scope(): string[] {
    return this.db.owner ? [this.db.owner.userId, this.db.owner.workspaceId] : ['', '']
  }
  init(): Promise<void> {
    return new PortalNotesStore(this.db).init()
  }
  async find(id: string): Promise<RecordProjection | null> {
    await this.init()
    return this.db.queryOne(
      'SELECT * FROM local_portal_records WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND source_id=?',
      [...this.scope, this.adapter.module, id],
    )
  }
  async byExternalId(id: string, kind = this.adapter.kind): Promise<RecordProjection | null> {
    await this.init()
    return this.db.queryOne(
      'SELECT * FROM local_portal_records WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND kind=? AND ext_id=?',
      [...this.scope, this.adapter.module, kind, id],
    )
  }
  /** Never infer ownership of pre-existing rows lacking a managed projection. */
  async commit(statements: Statement[], snapshot: RecordSnapshot, created: boolean): Promise<void> {
    await this.commitMany(statements, [{ snapshot, created }])
  }
  /** A parent deletion and its child tombstones share one SQLite transaction. */
  async commitMany(statements: Statement[], entries: { snapshot: RecordSnapshot; created: boolean }[]): Promise<void> {
    await this.init()
    const prepared: PreparedRecord[] = []
    const identities = new Set<string>()
    for (const { snapshot, created } of entries) {
      const record = await this.prepare(snapshot, created)
      if (!record) continue
      if (identities.has(record.identity)) throw new Error('Duplicate module identity in one local transaction')
      identities.add(record.identity)
      prepared.push(record)
    }
    try {
      // Check all observed versions before any source write. A remote/local
      // winner arriving during encryption must roll back the entire batch.
      await this.db.runInTransaction([
        ...prepared.map((record) => record.assertion),
        ...statements,
        ...prepared.flatMap((record) => record.statements),
      ])
    } catch (cause) {
      if (String(cause).includes('portal_record_write_conflict'))
        throw Object.assign(new Error(conflictMessage), { cause })
      throw cause
    }
    if (prepared.length && typeof window !== 'undefined')
      window.dispatchEvent(new Event(`openpocket:portal-${this.adapter.module}-outbox-changed`))
  }
  private async prepare(snapshot: RecordSnapshot, created: boolean): Promise<PreparedRecord | null> {
    await this.init()
    const existing = await this.find(snapshot.id)
    if (!this.db.owner || (!created && !existing)) return null
    const timestamp = new Date(snapshot.modifiedAt).toISOString()
    const item: RecordItem = {
      module: this.adapter.module,
      kind: snapshot.kind ?? this.adapter.kind,
      ext_id: existing
        ? (await this.decode(existing.item_json)).ext_id
        : (snapshot.extId ?? `${this.adapter.module}:${snapshot.id}`),
      title: snapshot.title,
      payload: snapshot.deleted ? {} : snapshot.payload,
      client_updated_at: timestamp,
      op_id: localVersionOperationId(timestamp, existing, conflictMessage),
      deleted: snapshot.deleted === true,
      epoch: snapshot.epoch,
    }
    let error = snapshot.syncError ?? ''
    try {
      validateRecordItem(this.adapter, item)
    } catch (cause) {
      error ||= cause instanceof Error ? cause.message : String(cause)
    }
    const json = await this.encode(item),
      admitted = recordEpochGuard(this.db, this.adapter.module, item.epoch ?? -1)
    const sourceGuard = snapshot.guard ?? { clause: '1=1', values: [] }
    const projectionGuard = {
      clause: `${sourceGuard.clause} AND EXISTS(SELECT 1 FROM local_portal_records WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND kind=? AND ext_id=? AND op_id=? AND version_key=?)`,
      values: [
        ...sourceGuard.values,
        ...this.scope,
        item.module,
        item.kind,
        item.ext_id,
        item.op_id,
        versionKey(item.client_updated_at),
      ],
    }
    const identityValues = [...this.scope, item.module, item.kind, item.ext_id]
    const observed = existing
      ? {
          clause: 'EXISTS(SELECT 1 FROM local_portal_records WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND kind=? AND ext_id=? AND source_id=? AND version_key=? AND op_id=? AND reset_epoch IS ?)',
          values: [...identityValues, existing.source_id, existing.version_key, existing.op_id, existing.reset_epoch],
        }
      : {
          clause: 'NOT EXISTS(SELECT 1 FROM local_portal_records WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND kind=? AND ext_id=?)',
          values: identityValues,
        }
    return {
      identity: JSON.stringify(identityValues),
      assertion: {
        statement: `INSERT INTO local_portal_record_write_guards(allowed) SELECT 0 WHERE NOT (${observed.clause})`,
        values: observed.values,
      },
      statements: [
        ...(snapshot.guard ? [{
          statement: `INSERT INTO local_portal_record_write_guards(allowed) SELECT 0 WHERE NOT (${sourceGuard.clause})`,
          values: sourceGuard.values,
        }] : []),
        {
          statement: `INSERT INTO local_portal_records(owner_user_id,owner_workspace_id,module,kind,ext_id,source_id,item_json,version_key,op_id,reset_epoch) SELECT ?,?,?,?,?,?,?,?,?,? WHERE ${sourceGuard.clause}
          ON CONFLICT(owner_user_id,owner_workspace_id,module,kind,ext_id) DO UPDATE SET item_json=excluded.item_json,version_key=excluded.version_key,op_id=excluded.op_id,reset_epoch=excluded.reset_epoch
          WHERE excluded.version_key>local_portal_records.version_key OR (excluded.version_key=local_portal_records.version_key AND excluded.op_id>local_portal_records.op_id)`,
          values: [
            ...this.scope,
            item.module,
            item.kind,
            item.ext_id,
            snapshot.id,
            json,
            versionKey(item.client_updated_at),
            item.op_id,
            item.epoch,
            ...sourceGuard.values,
          ],
        },
        {
          statement: `DELETE FROM local_portal_record_outbox WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND kind=? AND ext_id=? AND last_error<>'' AND ${projectionGuard.clause}`,
          values: [...this.scope, item.module, item.kind, item.ext_id, ...projectionGuard.values],
        },
        {
          statement: `INSERT INTO local_portal_record_outbox(owner_user_id,owner_workspace_id,module,kind,op_id,ext_id,item_json,reset_epoch,last_error)
          SELECT ?,?,?,?,?,?,?,?,CASE WHEN ? IS NULL THEN 'epoch_unknown' WHEN ${admitted.clause} THEN ? ELSE 'reset_epoch_mismatch' END WHERE ${projectionGuard.clause}`,
          values: [
            ...this.scope,
            item.module,
            item.kind,
            item.op_id,
            item.ext_id,
            json,
            item.epoch,
            item.epoch,
            ...admitted.values,
            error,
            ...projectionGuard.values,
          ],
        },
      ],
    }
  }
  async pending(): Promise<RecordItem[]> {
    await this.init()
    if (!this.db.owner) return []
    const epoch = await currentRecordEpoch(this.db, this.adapter.module)
    await this.db.run(
      `UPDATE local_portal_record_outbox SET last_error=CASE WHEN reset_epoch IS NULL THEN 'epoch_unknown' ELSE 'reset_epoch_mismatch' END
      WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND (reset_epoch IS NULL OR reset_epoch<>?)`,
      [...this.scope, this.adapter.module, epoch ?? -1],
    )
    if (epoch === null) return []
    const admitted = recordEpochGuard(this.db, this.adapter.module, epoch)
    await resumeClockCorrections(this.db, this.adapter.module, this)
    const rows = await this.db.query<{ item_json: string }>(
      `SELECT item_json FROM local_portal_record_outbox WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND reset_epoch=? AND last_error IN ('','clock_retry') AND ${admitted.clause} ORDER BY rowid LIMIT 50`,
      [...this.scope, this.adapter.module, epoch, ...admitted.values],
    )
    return Promise.all(rows.map((row) => this.decode(row.item_json)))
  }
  async acknowledge(item: RecordItem, ack: PortalAck): Promise<boolean> {
    if (ack.module !== item.module || ack.ext_id !== item.ext_id || ack.op_id !== item.op_id) return false
    if (ack.reason === 'reset_epoch_mismatch' && validEpoch(ack.current_epoch)) {
      if (((await this.currentEpoch()) ?? -1) <= ack.current_epoch) await this.observeEpoch(ack.current_epoch)
      await this.block(item, 'reset_epoch_mismatch')
      return false
    }
    if (!validEpoch(ack.current_epoch) || ack.current_epoch !== item.epoch) {
      await this.block(item, 'invalid_epoch_ack')
      return false
    }
    if (ack.outcome === 'skipped_local') {
      await this.block(item, 'policy_local')
      return false
    }
    if (ack.clock_skew || (ack.outcome === 'rejected' && ack.reason === 'clock_skew')) {
      await acknowledgeClock(this.db, item, ack, this)
      return false
    }
    if (!['applied', 'kept_existing'].includes(ack.outcome)) {
      await this.block(item, ack.reason ?? ack.outcome)
      return false
    }
    const admitted = recordEpochGuard(this.db, this.adapter.module, item.epoch!)
    await this.db.run(
      `DELETE FROM local_portal_record_outbox WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND op_id=? AND reset_epoch=? AND ${admitted.clause}`,
      [...this.scope, item.module, item.op_id, item.epoch, ...admitted.values],
    )
    return Boolean(
      await this.db.queryOne(
        `SELECT 1 AS acknowledged WHERE ${admitted.clause} AND NOT EXISTS(SELECT 1 FROM local_portal_record_outbox WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND op_id=?)`,
        [...admitted.values, ...this.scope, item.module, item.op_id],
      ),
    )
  }
  async block(item: RecordItem, error: string): Promise<void> {
    await this.db.run(
      `UPDATE local_portal_record_outbox SET last_error=? WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND op_id=?`,
      [error, ...this.scope, item.module, item.op_id],
    )
  }
  async resumeSkipped(): Promise<void> {
    await this.init()
    const rows = await this.db.query<{ item_json: string }>(
      `SELECT item_json FROM local_portal_record_outbox WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND last_error='policy_local'`,
      [...this.scope, this.adapter.module],
    )
    for (const row of rows) {
      const old = await this.decode(row.item_json)
      if (!validEpoch(old.epoch) || old.epoch !== (await this.currentEpoch())) continue
      const item = { ...old, op_id: newOperationId() },
        json = await this.encode(item)
      const guard = recordEpochGuard(this.db, this.adapter.module, item.epoch!)
      await this.db.runInTransaction([
        {
          statement: `INSERT INTO local_portal_record_outbox(owner_user_id,owner_workspace_id,module,kind,op_id,ext_id,item_json,reset_epoch) SELECT ?,?,?,?,?,?,?,? WHERE ${guard.clause}`,
          values: [...this.scope, item.module, item.kind, item.op_id, item.ext_id, json, item.epoch, ...guard.values],
        },
        {
          statement: `DELETE FROM local_portal_record_outbox WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND op_id=? AND last_error='policy_local' AND ${guard.clause}`,
          values: [...this.scope, item.module, old.op_id, ...guard.values],
        },
        {
          statement: `UPDATE local_portal_records SET op_id=?,item_json=? WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND kind=? AND ext_id=? AND op_id=? AND ${guard.clause}`,
          values: [item.op_id, json, ...this.scope, item.module, item.kind, item.ext_id, old.op_id, ...guard.values],
        },
      ])
    }
  }
  async observeEpoch(epoch: number): Promise<boolean> {
    await this.init()
    return observeRecordEpoch(this.db, this.adapter.module, epoch)
  }
  async currentEpoch(): Promise<number | null> {
    await this.init()
    return currentRecordEpoch(this.db, this.adapter.module)
  }
  async requireEpoch(epoch: number | null): Promise<void> {
    if (!validEpoch(epoch)) throw new Error('Module operation has no verified reset epoch')
    const guard = recordEpochGuard(this.db, this.adapter.module, epoch)
    if (!(await this.db.queryOne(`SELECT 1 WHERE ${guard.clause}`, guard.values)))
      throw new Error('Module reset is pending or changed; local original retained')
  }
  async cursor(): Promise<string> {
    await this.init()
    return (
      (
        await this.db.queryOne<{ cursor: string }>(
          `SELECT cursor FROM local_portal_cursors WHERE owner_user_id=? AND owner_workspace_id=? AND module=?`,
          [...this.scope, this.adapter.module],
        )
      )?.cursor ?? ''
    )
  }
  cursorStatement(cursor: string, epoch: number): Statement {
    const guard = recordEpochGuard(this.db, this.adapter.module, epoch)
    return {
      statement: `INSERT INTO local_portal_cursors(owner_user_id,owner_workspace_id,module,cursor) SELECT ?,?,?,? WHERE ${guard.clause}
      ON CONFLICT(owner_user_id,owner_workspace_id,module) DO UPDATE SET cursor=excluded.cursor`,
      values: [...this.scope, this.adapter.module, cursor, ...guard.values],
    }
  }
}
