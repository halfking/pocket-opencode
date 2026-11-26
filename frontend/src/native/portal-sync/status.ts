import { reactive } from 'vue'
import type { SyncDb } from './contract.ts'
import { clockRetryReady, clockRetryRequired } from './clock-retry.ts'

export const portalNotesSyncStatus = reactive({
  failed: false, blocked: 0, running: false, clockPending: 0,
  resetPending: false, resetBlocked: false, epochUnknown: false,
})
let readGeneration = 0
export function invalidateNotesStatusReads(): void { readGeneration++ }

export async function refreshNotesSyncStatus(db: SyncDb, scope: string[], cloud = true): Promise<void> {
  const generation = readGeneration
  const blocked = await db.queryOne<{ count: number; unknown_epoch: number; reset_epoch: number; clock_pending: number }>(`SELECT COUNT(*) AS count,
    COALESCE(SUM(${clockRetryRequired}),0) AS clock_pending,
    COALESCE(SUM(last_error='epoch_unknown'),0) AS unknown_epoch,COALESCE(SUM(last_error IN ('reset_epoch_mismatch','reset_pending')),0) AS reset_epoch
    FROM local_portal_outbox WHERE owner_user_id=? AND owner_workspace_id=? AND last_error NOT IN ('',?)`, [...scope, clockRetryReady])
  const pendingReset = await db.queryOne(`SELECT reset_id FROM local_portal_resets WHERE owner_user_id=? AND owner_workspace_id=? AND blocks_notes=1 AND completed=0`, scope)
  db.assertCurrent()
  if (generation !== readGeneration) return
  portalNotesSyncStatus.blocked = blocked?.count ?? 0
  portalNotesSyncStatus.clockPending = cloud ? blocked?.clock_pending ?? 0 : 0
  portalNotesSyncStatus.epochUnknown = Boolean(blocked?.unknown_epoch)
  portalNotesSyncStatus.resetBlocked = Boolean(blocked?.reset_epoch)
  portalNotesSyncStatus.resetPending = Boolean(pendingReset)
}
