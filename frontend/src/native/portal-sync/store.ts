import { validateItem, versionKey, newOperationId, localVersionOperationId, type NotePayload, type PortalAck, type PortalItem, type Statement, type SyncDb } from './contract.ts'
import { epochTables, currentNotesEpoch, epochGuard, observeNotesEpoch } from './epoch-state.ts'
import { validEpoch } from './epochs.ts'
import { recordTables } from './record-tables.ts'
import { acknowledgeClock, resumeClockCorrections } from './clock-retry.ts'

export interface NoteSnapshot {
  source: 'note' | 'asset'
  id: string
  title: string
  payload: NotePayload
  modifiedAt: number
  deleted?: boolean
  syncError?: string
  epoch?: number | null
  /** Earlier business read used to prepare this source edit; do not adopt a later winner. */
  expectedVersion?: ProjectionRow | null
}
export interface ProjectionRow {
  ext_id: string
  source: 'note' | 'asset'
  source_id: string
  item_json: string
  version_ms: number
  version_key: string
  op_id: string
  reset_epoch: number | null
}
const tables = [
  `CREATE TABLE IF NOT EXISTS local_portal_note_write_guards (
    allowed INTEGER NOT NULL CONSTRAINT portal_notes_write_conflict CHECK(allowed=1))`,
  `CREATE TABLE IF NOT EXISTS local_portal_notes (
    owner_user_id TEXT NOT NULL, owner_workspace_id TEXT NOT NULL,
    ext_id TEXT NOT NULL, source TEXT NOT NULL, source_id TEXT NOT NULL,
    item_json TEXT NOT NULL, version_ms INTEGER NOT NULL, version_key TEXT NOT NULL, op_id TEXT NOT NULL, reset_epoch INTEGER,
    PRIMARY KEY(owner_user_id,owner_workspace_id,ext_id),
    UNIQUE(owner_user_id,owner_workspace_id,source,source_id))`,
  `CREATE TABLE IF NOT EXISTS local_portal_outbox (
    owner_user_id TEXT NOT NULL, owner_workspace_id TEXT NOT NULL,
    op_id TEXT NOT NULL, ext_id TEXT NOT NULL, item_json TEXT NOT NULL, last_error TEXT NOT NULL DEFAULT '',reset_epoch INTEGER,
    PRIMARY KEY(owner_user_id,owner_workspace_id,op_id))`,
  `CREATE TABLE IF NOT EXISTS local_portal_cursors (
    owner_user_id TEXT NOT NULL, owner_workspace_id TEXT NOT NULL,
    module TEXT NOT NULL, cursor TEXT NOT NULL,
    PRIMARY KEY(owner_user_id,owner_workspace_id,module))`,
  ...epochTables,
  ...recordTables,
]
const schemaReady = new WeakMap<object, Promise<void>>()

/** No migration scans existing note/asset rows: absence of a projection means unclaimed. */
export class PortalNotesStore {
  readonly db: SyncDb
  readonly encode: (item: PortalItem) => Promise<string>
  readonly decode: (value: string) => Promise<PortalItem>
  constructor(db: SyncDb, codec?: { encode: (item: PortalItem) => Promise<string>; decode: (value: string) => Promise<PortalItem> }) {
    this.db = db
    this.encode = codec?.encode ?? (async item => JSON.stringify(item))
    this.decode = codec?.decode ?? (async value => JSON.parse(value) as PortalItem)
  }
  get scope(): string[] { return this.db.owner ? [this.db.owner.userId, this.db.owner.workspaceId] : ['', ''] }
  async init(): Promise<void> {
    this.db.assertCurrent()
    const key = this.db.schemaConnection ?? this.db
    let ready = schemaReady.get(key)
    if (!ready) {
      ready = (async () => {
        for (const sql of tables) await this.db.run(sql)
        for (const table of ['local_portal_notes', 'local_portal_outbox']) {
          const columns = await this.db.query<{ name: string }>(`PRAGMA table_info(${table})`)
          if (!columns.some(column => column.name === 'reset_epoch')) await this.db.run(`ALTER TABLE ${table} ADD COLUMN reset_epoch INTEGER`)
        }
      })()
      schemaReady.set(key, ready)
      void ready.catch(() => { if (schemaReady.get(key) === ready) schemaReady.delete(key) })
    }
    await ready
    this.db.assertCurrent()
  }
  async find(source: string, id: string): Promise<ProjectionRow | null> {
    if (!this.db.owner) return null
    await this.init()
    return this.db.queryOne<ProjectionRow>(`SELECT * FROM local_portal_notes WHERE owner_user_id=? AND owner_workspace_id=? AND source=? AND source_id=?`, [...this.scope, source, id])
  }
  async byExternalId(id: string): Promise<ProjectionRow | null> {
    await this.init()
    return this.db.queryOne<ProjectionRow>(`SELECT * FROM local_portal_notes WHERE owner_user_id=? AND owner_workspace_id=? AND ext_id=?`, [...this.scope, id])
  }
  async commit(statements: Statement[], snapshot: NoteSnapshot, created: boolean): Promise<ProjectionRow | null> {
    const epoch = validEpoch(snapshot.epoch) ? snapshot.epoch : null
    await this.init()
    const existing = 'expectedVersion' in snapshot ? snapshot.expectedVersion ?? null : await this.find(snapshot.source, snapshot.id)
    if (existing && (existing.source !== snapshot.source || existing.source_id !== snapshot.id)) throw new Error('Note version does not match its source')
    const assigned = await this.db.queryOne<{ owner_user_id: string; owner_workspace_id: string }>(
      `SELECT owner_user_id,owner_workspace_id FROM local_portal_notes WHERE source=? AND source_id=?`, [snapshot.source, snapshot.id])
    if (assigned && (!this.db.owner || assigned.owner_user_id !== this.db.owner.userId || assigned.owner_workspace_id !== this.db.owner.workspaceId)) throw new Error('Note belongs to a different account or workspace')
    // Editing an old unclaimed note does not establish its provenance.
    if (!this.db.owner || (!created && !existing)) {
      await this.db.runInTransaction([
        {statement:'INSERT INTO local_portal_note_write_guards(allowed) SELECT 0 WHERE EXISTS(SELECT 1 FROM local_portal_notes WHERE source=? AND source_id=?)',values:[snapshot.source,snapshot.id]},
        ...statements,
      ])
      return null
    }
    const timestamp = new Date(snapshot.modifiedAt).toISOString()
    const conflictMessage = 'Note changed during this edit; original retained, keep your changes and retry the edit'
    const item: PortalItem = {
      module: 'notes', kind: 'note', ext_id: existing?.ext_id ?? `${snapshot.source}:${snapshot.id}`,
      title: snapshot.title, payload: snapshot.deleted ? {} : snapshot.payload,
      client_updated_at: timestamp,
      op_id: localVersionOperationId(timestamp, existing, conflictMessage), deleted: snapshot.deleted === true, epoch,
    }
    let error = snapshot.syncError ?? ''
    try { validateItem(item) } catch (cause) { error = cause instanceof Error ? cause.message : String(cause) }
    const json = await this.encode(item)
    const admitted = epochGuard(this.db, epoch ?? -1)
    const observed = existing ? {
      clause: `EXISTS(SELECT 1 FROM local_portal_notes WHERE owner_user_id=? AND owner_workspace_id=? AND ext_id=? AND source=? AND source_id=? AND version_key=? AND op_id=? AND reset_epoch IS ?)`,
      values: [...this.scope, existing.ext_id, existing.source, existing.source_id, existing.version_key, existing.op_id, existing.reset_epoch],
    } : {
      clause: `NOT EXISTS(SELECT 1 FROM local_portal_notes WHERE source=? AND source_id=?)`,
      values: [snapshot.source, snapshot.id],
    }
    try { await this.db.runInTransaction([
      { statement: `INSERT INTO local_portal_note_write_guards(allowed) SELECT 0 WHERE NOT (${observed.clause})`, values: observed.values },
      ...statements,
      { statement: `INSERT INTO local_portal_notes
          (owner_user_id,owner_workspace_id,ext_id,source,source_id,item_json,version_ms,version_key,op_id,reset_epoch)
        VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT(owner_user_id,owner_workspace_id,ext_id)
        DO UPDATE SET item_json=excluded.item_json,version_ms=excluded.version_ms,version_key=excluded.version_key,op_id=excluded.op_id,reset_epoch=excluded.reset_epoch`,
        values: [...this.scope, item.ext_id, snapshot.source, snapshot.id, json, snapshot.modifiedAt, versionKey(item.client_updated_at), item.op_id, epoch] },
      { statement: `DELETE FROM local_portal_outbox WHERE owner_user_id=? AND owner_workspace_id=? AND ext_id=? AND last_error<>''`, values: [...this.scope, item.ext_id] },
      { statement: `INSERT INTO local_portal_outbox (owner_user_id,owner_workspace_id,op_id,ext_id,item_json,reset_epoch,last_error)
          VALUES (?,?,?,?,?,?,CASE WHEN ? IS NULL THEN 'epoch_unknown' WHEN ${admitted.clause} THEN ? ELSE 'reset_epoch_mismatch' END)`,
        values: [...this.scope, item.op_id, item.ext_id, json, epoch, epoch, ...admitted.values, error] },
    ]) } catch (cause) {
      if (String(cause).includes('portal_notes_write_conflict')) throw Object.assign(new Error(conflictMessage), { cause })
      throw cause
    }
    if (error) console.warn('[portal-sync] 本地笔记已保存，同步受阻：', error)
    if (typeof window !== 'undefined') window.dispatchEvent(new Event('openpocket:portal-notes-outbox-changed'))
    return {ext_id:item.ext_id,source:snapshot.source,source_id:snapshot.id,item_json:json,version_ms:snapshot.modifiedAt,version_key:versionKey(timestamp),op_id:item.op_id,reset_epoch:epoch}
  }
  async pending(): Promise<PortalItem[]> {
    if (!this.db.owner) return []
    await this.init()
    await this.db.run(`UPDATE local_portal_outbox SET last_error=CASE WHEN reset_epoch IS NULL THEN 'epoch_unknown' ELSE 'reset_epoch_mismatch' END
      WHERE owner_user_id=? AND owner_workspace_id=? AND (reset_epoch IS NULL OR reset_epoch<>COALESCE((SELECT epoch FROM local_portal_sync_epochs WHERE owner_user_id=? AND owner_workspace_id=? AND module='notes'),-1))`, [...this.scope, ...this.scope])
    const epoch = await currentNotesEpoch(this.db)
    if (epoch === null) return []
    const admitted = epochGuard(this.db, epoch)
    await resumeClockCorrections(this.db, 'notes', this)
    const rows = await this.db.query<{ item_json: string }>(`SELECT item_json FROM local_portal_outbox
      WHERE owner_user_id=? AND owner_workspace_id=? AND last_error IN ('','clock_retry') AND reset_epoch=? AND ${admitted.clause} ORDER BY rowid LIMIT 50`, [...this.scope, epoch, ...admitted.values])
    return Promise.all(rows.map(row => this.decode(row.item_json)))
  }
  async acknowledge(item: PortalItem, ack: PortalAck): Promise<boolean> {
    if (ack.module !== item.module || ack.ext_id !== item.ext_id || ack.op_id !== item.op_id) return false
    if (ack.reason === 'reset_epoch_mismatch' && validEpoch(ack.current_epoch)) {
      if ((await currentNotesEpoch(this.db) ?? -1) <= ack.current_epoch) await this.observeEpoch(ack.current_epoch)
      await this.block(item, 'reset_epoch_mismatch')
      return false
    }
    if (!validEpoch(ack.current_epoch) || ack.current_epoch !== item.epoch) { await this.block(item, 'invalid_epoch_ack'); return false }
    if (ack.outcome === 'skipped_local') { await this.block(item, 'policy_local'); return false }
    if (ack.clock_skew || (ack.outcome === 'rejected' && ack.reason === 'clock_skew')) {
      await acknowledgeClock(this.db, item, ack, this)
      return false
    }
    if (ack.outcome === 'rejected' || ack.outcome === 'duplicate') {
      await this.block(item, `Portal sync outcome requires attention: ${ack.reason ?? ack.outcome}`)
      return false
    }
    if (ack.outcome !== 'applied' && ack.outcome !== 'kept_existing') return false
    // Exact immutable operation ack. A simultaneous edit has a different operation id.
    const admitted = epochGuard(this.db, item.epoch!)
    await this.db.run(`DELETE FROM local_portal_outbox WHERE owner_user_id=? AND owner_workspace_id=? AND op_id=? AND reset_epoch=? AND ${admitted.clause}`, [...this.scope, item.op_id, item.epoch, ...admitted.values])
    return Boolean(await this.db.queryOne(`SELECT 1 AS acknowledged WHERE ${admitted.clause}
      AND NOT EXISTS(SELECT 1 FROM local_portal_outbox WHERE owner_user_id=? AND owner_workspace_id=? AND op_id=?)`, [...admitted.values, ...this.scope, item.op_id]))
  }
  async resumeSkipped(): Promise<void> {
    await this.init()
    const rows = await this.db.query<{ item_json: string }>(`SELECT item_json FROM local_portal_outbox WHERE owner_user_id=? AND owner_workspace_id=? AND last_error='policy_local'`, this.scope)
    for (const row of rows) {
      const old = await this.decode(row.item_json)
      if (!validEpoch(old.epoch) || old.epoch !== await currentNotesEpoch(this.db)) continue
      const item = { ...old, op_id: newOperationId() }
      await this.db.runInTransaction([
        { statement: `INSERT INTO local_portal_outbox (owner_user_id,owner_workspace_id,op_id,ext_id,item_json,reset_epoch) VALUES (?,?,?,?,?,?)`, values: [...this.scope, item.op_id, item.ext_id, await this.encode(item), item.epoch] },
        { statement: `DELETE FROM local_portal_outbox WHERE owner_user_id=? AND owner_workspace_id=? AND op_id=? AND last_error='policy_local'`, values: [...this.scope, old.op_id] },
        { statement: `UPDATE local_portal_notes SET op_id=?,item_json=? WHERE owner_user_id=? AND owner_workspace_id=? AND ext_id=? AND op_id=?`, values: [item.op_id, await this.encode(item), ...this.scope, item.ext_id, old.op_id] },
      ])
    }
  }
  async observeEpoch(epoch: number): Promise<boolean> { await this.init(); return observeNotesEpoch(this.db, epoch) }
  async currentEpoch(): Promise<number | null> { await this.init(); return currentNotesEpoch(this.db) }
  async requireEpoch(epoch: number | null): Promise<void> {
    if (!validEpoch(epoch)) throw new Error('Notes operation has no verified reset epoch')
    const admitted = epochGuard(this.db, epoch)
    if (!await this.db.queryOne(`SELECT 1 AS admitted WHERE ${admitted.clause}`, admitted.values)) throw new Error('Notes cloud reset is pending or changed; original operation retained')
  }
  async block(item: PortalItem, message: string): Promise<void> {
    await this.db.run(`UPDATE local_portal_outbox SET last_error=? WHERE owner_user_id=? AND owner_workspace_id=? AND op_id=?`, [message, ...this.scope, item.op_id])
    console.warn('[portal-sync] 同步操作保留等待处理：', message)
  }
  async cursor(): Promise<string> {
    await this.init()
    return (await this.db.queryOne<{ cursor: string }>(`SELECT cursor FROM local_portal_cursors WHERE owner_user_id=? AND owner_workspace_id=? AND module='notes'`, this.scope))?.cursor ?? ''
  }
  cursorStatement(cursor: string, epoch: number): Statement {
    const admitted = epochGuard(this.db, epoch)
    return { statement: `INSERT INTO local_portal_cursors (owner_user_id,owner_workspace_id,module,cursor) SELECT ?,?,'notes',? WHERE ${admitted.clause}
      ON CONFLICT(owner_user_id,owner_workspace_id,module) DO UPDATE SET cursor=excluded.cursor`, values: [...this.scope, cursor, ...admitted.values] }
  }
}
