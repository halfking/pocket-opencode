import { isPortalSyncModule, type PortalSyncModule } from '../../api/syncPolicyContract.ts'
import { newOperationId, type Statement } from './contract.ts'
import { markNotesResetPending, rememberNotesEpoch, validEpoch } from './epochs.ts'
import { currentNotesEpoch, notesEpochStatements } from './epoch-state.ts'
import { PortalNotesStore } from './store.ts'

export interface ResetIntent { reset_id: string; modules: PortalSyncModule[] }
export interface ResetResult {
  reset_id: string
  purged: Partial<Record<PortalSyncModule, number>>
  epochs: Partial<Record<PortalSyncModule, number>>
}

export function parseResetResult(raw: unknown, intent: ResetIntent): ResetResult {
  const body = raw as ResetResult
  if (!body || body.reset_id !== intent.reset_id || !body.purged || !body.epochs ||
    typeof body.purged !== 'object' || typeof body.epochs !== 'object' ||
    Array.isArray(body.purged) || Array.isArray(body.epochs) ||
    Object.keys(body.purged).length !== intent.modules.length ||
    Object.keys(body.epochs).length !== intent.modules.length ||
    intent.modules.some(module => !validEpoch(body.purged[module]) || !validEpoch(body.epochs[module]))) {
    throw new Error('Invalid trusted cloud reset response; local originals and reset intent retained')
  }
  return body
}

/** A failed/uncertain purge reuses its persistent reset id, including after restart. */
export class PortalResetStore {
  readonly store: PortalNotesStore
  constructor(store: PortalNotesStore) { this.store = store }
  async begin(modules: PortalSyncModule[]): Promise<ResetIntent> {
    if (!modules.length || !modules.every(isPortalSyncModule)) throw new Error('Unsupported cloud reset module')
    const sorted = [...new Set(modules)].sort() as PortalSyncModule[]
    const json = JSON.stringify(sorted), store = this.store, db = store.db
    if (!db.owner) throw new Error('Verified cloud reset owner required')
    await store.init()
    const blocksNotes = sorted.includes('notes')
    await db.runInTransaction([
      { statement: `INSERT OR IGNORE INTO local_portal_resets(owner_user_id,owner_workspace_id,reset_id,modules_json,blocks_notes) VALUES(?,?,?,?,?)`, values: [...store.scope, newOperationId(), json, blocksNotes ? 1 : 0] },
      ...(blocksNotes ? [{ statement: `UPDATE local_portal_outbox SET last_error='reset_pending' WHERE owner_user_id=? AND owner_workspace_id=?`, values: store.scope }] : []),
    ])
    const pending = await db.queryOne<{ reset_id: string }>(`SELECT reset_id FROM local_portal_resets WHERE owner_user_id=? AND owner_workspace_id=? AND modules_json=? AND completed=0`, [...store.scope, json])
    if (!pending) throw new Error('Cloud reset intent was not persisted')
    if (blocksNotes) markNotesResetPending(db.owner, true)
    return { reset_id: pending.reset_id, modules: sorted }
  }
  async complete(intent: ResetIntent, raw: unknown): Promise<ResetResult> {
    const result = parseResetResult(raw, intent)
    const store = this.store, db = store.db
    const issued = await db.queryOne<{ modules_json: string }>(`SELECT modules_json FROM local_portal_resets WHERE owner_user_id=? AND owner_workspace_id=? AND reset_id=?`, [...store.scope, intent.reset_id])
    if (!issued || issued.modules_json !== JSON.stringify(intent.modules)) throw new Error('Cloud reset intent does not belong to this operation')
    const statements: Statement[] = []
    for (const module of intent.modules) {
      const epoch = result.epochs[module]!
      if (module === 'notes') statements.push(...notesEpochStatements(db, epoch))
      else statements.push({ statement: `INSERT INTO local_portal_sync_epochs(owner_user_id,owner_workspace_id,module,epoch) VALUES(?,?,?,?)
        ON CONFLICT(owner_user_id,owner_workspace_id,module) DO UPDATE SET epoch=MAX(local_portal_sync_epochs.epoch,excluded.epoch)`, values: [...store.scope, module, epoch] })
    }
    statements.push({ statement: `UPDATE local_portal_resets SET completed=1,response_json=? WHERE owner_user_id=? AND owner_workspace_id=? AND reset_id=? AND modules_json=?`,
      values: [JSON.stringify(result), ...store.scope, intent.reset_id, JSON.stringify(intent.modules)] })
    await db.runInTransaction(statements)
    if (intent.modules.includes('notes')) {
      const current = await currentNotesEpoch(db)
      const remaining = await db.queryOne(`SELECT reset_id FROM local_portal_resets WHERE owner_user_id=? AND owner_workspace_id=? AND blocks_notes=1 AND completed=0`, store.scope)
      markNotesResetPending(db.owner, Boolean(remaining))
      if (current !== null) rememberNotesEpoch(db.owner, current)
    }
    return result
  }
}
