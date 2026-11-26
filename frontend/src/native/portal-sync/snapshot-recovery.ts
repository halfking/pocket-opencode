import type { PortalSyncModule } from '../../api/syncPolicyContract.ts'
import type { Statement, SyncDb } from './contract.ts'
import { epochGuard } from './epoch-state.ts'
import { recordEpochGuard } from './record-epoch.ts'

export const portalChangePageSize = 50

export class PortalCursorExpired extends Error {
  constructor() {
    super('Portal change cursor expired')
  }
}

interface Recovery {
  cursor: string
  epoch: number | null
}

interface ChangePage {
  changes: { kind: string; ext_id: string }[]
  next_cursor: string
}

interface CursorStore {
  db: SyncDb
  scope: string[]
  cursor(): Promise<string>
  currentEpoch(): Promise<number | null>
  observeEpoch(epoch: number): Promise<boolean>
  cursorStatement(cursor: string, epoch: number): Statement
}

const hex = '[0-9a-f]'
const generatedSourceIds = [
  `portal-${hex.repeat(32)}`,
  `portal-${[8, 4, 4, 4, 12].map(size => hex.repeat(size)).join('-')}`,
]
const scopeWhere = 'owner_user_id=? AND owner_workspace_id=? AND module=?'

/** A recovery cursor is separate from the committed incremental checkpoint. */
export async function pullPortalChanges<Page extends ChangePage>(
  store: CursorStore,
  module: PortalSyncModule,
  changes: (cursor: string) => Promise<unknown>,
  parse: (raw: unknown) => Page,
  epochOf: (page: Page) => number,
  assertCurrent: () => void,
  apply: (page: Page, checkpoint?: Statement[]) => Promise<void>,
  onApplied?: (count: number) => void,
): Promise<boolean> {
  const db = store.db, scope = [...store.scope, module]
  async function restart(epoch: number | null): Promise<Recovery> {
    await db.runInTransaction([
      { statement: `DELETE FROM local_portal_recovery_seen WHERE ${scopeWhere}`, values: scope },
      { statement: `INSERT INTO local_portal_recovery VALUES(?,?,?,'',?)
        ON CONFLICT(owner_user_id,owner_workspace_id,module) DO UPDATE SET cursor='',epoch=excluded.epoch`,
        values: [...scope, epoch] },
    ])
    return { cursor: '', epoch }
  }
  let recovery = await db.queryOne<Recovery>(`SELECT cursor,epoch FROM local_portal_recovery WHERE ${scopeWhere}`, scope)
  for (let n = 0; n < 10; n++) {
    assertCurrent()
    if (recovery) {
      const epoch = await store.currentEpoch()
      if (recovery.epoch !== epoch) recovery = await restart(epoch)
    }
    const cursor = recovery?.cursor ?? await store.cursor()
    let raw: unknown
    try {
      raw = await changes(cursor)
    } catch (error) {
      if (!(error instanceof PortalCursorExpired) || !cursor) throw error
      assertCurrent()
      recovery = await restart(await store.currentEpoch())
      continue
    }
    assertCurrent()
    const page = parse(raw), nextEpoch = epochOf(page)
    if (await store.observeEpoch(nextEpoch)) continue
    if (page.changes.length && page.next_cursor === cursor) throw new Error('Portal change cursor did not advance')
    if (!recovery) {
      await apply(page)
      if (page.changes.length) onApplied?.(page.changes.length)
      if (!page.changes.length) break
      continue
    }

    if (!page.next_cursor) throw new Error('Portal recovery page has no checkpoint')
    const complete = page.changes.length < portalChangePageSize
    const admitted = module === 'notes' ? epochGuard(db, nextEpoch) : recordEpochGuard(db, module, nextEpoch)
    const checkpoint = page.changes.map(({ kind, ext_id }): Statement => ({
      statement: `INSERT OR IGNORE INTO local_portal_recovery_seen SELECT ?,?,?,?,? WHERE ${admitted.clause}`,
      values: [...scope, kind, ext_id, ...admitted.values],
    }))
    if (complete) {
      checkpoint.push(...retireMissingMirrors(module, scope, admitted))
      checkpoint.push(store.cursorStatement(page.next_cursor, nextEpoch))
      for (const table of ['local_portal_recovery_seen', 'local_portal_recovery']) {
        checkpoint.push({ statement: `DELETE FROM ${table} WHERE ${scopeWhere} AND ${admitted.clause}`, values: [...scope, ...admitted.values] })
      }
    } else {
      checkpoint.push({ statement: `UPDATE local_portal_recovery SET cursor=? WHERE ${scopeWhere} AND ${admitted.clause}`,
        values: [page.next_cursor, ...scope, ...admitted.values] })
    }
    await apply(page, checkpoint)
    recovery = complete ? null : { cursor: page.next_cursor, epoch: nextEpoch }
    onApplied?.(page.changes.length)
    if (complete) break
  }
  return recovery === null
}

/** Only server-created mirrors use the generated portal- source ID. Retain raw native rows and queued edits. */
function retireMissingMirrors(module: PortalSyncModule, scope: string[], guard: { clause: string; values: unknown[] }): Statement[] {
  const notes = module === 'notes'
  const projection = notes ? 'local_portal_notes' : 'local_portal_records'
  const outbox = notes ? 'local_portal_outbox' : 'local_portal_record_outbox'
  const kind = notes ? "'note'" : 'p.kind'
  const identity = `o.owner_user_id=p.owner_user_id AND o.owner_workspace_id=p.owner_workspace_id AND o.ext_id=p.ext_id
    ${notes ? '' : 'AND o.module=p.module AND o.kind=p.kind'}`
  const missing = `p.owner_user_id=? AND p.owner_workspace_id=? ${notes ? '' : 'AND p.module=?'}
    AND (p.source_id GLOB ? OR p.source_id GLOB ?)
    AND NOT EXISTS (SELECT 1 FROM local_portal_recovery_seen s WHERE s.owner_user_id=p.owner_user_id
      AND s.owner_workspace_id=p.owner_workspace_id AND s.module=? AND s.kind=${kind} AND s.ext_id=p.ext_id)
    AND ${guard.clause}`
  const values = [...(notes ? scope.slice(0, 2) : scope), ...generatedSourceIds, module, ...guard.values]
  const queued = `EXISTS(SELECT 1 FROM ${outbox} o WHERE ${identity})`
  const statements: Statement[] = [{
    statement: `UPDATE ${outbox} AS o SET last_error='snapshot_missing'
      WHERE EXISTS(SELECT 1 FROM ${projection} p WHERE ${missing} AND ${identity} AND o.reset_epoch=p.reset_epoch)`,
    values,
  }]
  if (notes) {
    for (const [table, source] of [['local_notes', 'note'], ['local_assets', 'asset']]) {
      statements.push({
        statement: `UPDATE ${table} AS native SET deleted_at=? WHERE deleted_at IS NULL AND EXISTS(
          SELECT 1 FROM ${projection} p WHERE ${missing} AND NOT ${queued} AND p.source=?
            AND p.source_id=native.id AND p.owner_workspace_id=native.workspace_id)`,
        values: [Date.now(), ...values, source],
      })
    }
  } else if (module === 'meetings') {
    statements.push({
      statement: `UPDATE local_meetings AS native SET deleted_at=? WHERE deleted_at IS NULL AND EXISTS(
        SELECT 1 FROM ${projection} p WHERE ${missing} AND NOT ${queued} AND p.source_id=native.id
          AND p.owner_user_id=native.owner_user_id AND p.owner_workspace_id=native.owner_workspace_id)`,
      values: [Date.now(), ...values],
    })
  } else {
    statements.push({ statement: `DELETE FROM ${projection} AS p WHERE ${missing} AND NOT ${queued}`, values })
  }
  return statements
}
