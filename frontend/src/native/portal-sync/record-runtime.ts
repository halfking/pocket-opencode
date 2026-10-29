import { reactive, watch } from 'vue'
import { localDB } from '../local-db'
import { lobsterReady } from '../lobster-init'
import { useAuthStore } from '../../stores/auth'
import { readCachedModuleMode, recordSyncReportEntry } from '../../stores/syncPolicyStore'
import { portalRecordStore } from './record-storage.ts'
import { portalTransport } from './transport.ts'
import { syncRecordsOnce } from './record-engine.ts'
import { markModuleResetPending, rememberModuleEpoch, type RecordModule } from './epochs.ts'
import type { RecordAdapter } from './record-contract.ts'
import type { SyncDb } from './contract.ts'

export function createRecordRuntime(adapter: RecordAdapter) {
  const module = adapter.module
  const status = reactive({
    failed: false,
    running: false,
    blocked: 0,
    resetPending: false,
    resetBlocked: false,
    epochUnknown: false,
  })
  let started = false,
    flight: Promise<number> | null = null,
    resumePolicy = false,
    version = 0
  const refresh = async (db: SyncDb) => {
    const stamp = version,
      owner = [db.owner!.userId, db.owner!.workspaceId]
    const row = await db.queryOne<{ count: number; unknown_epoch: number; reset_epoch: number }>(
      `SELECT COUNT(*) AS count,COALESCE(SUM(last_error='epoch_unknown'),0) AS unknown_epoch,
      COALESCE(SUM(last_error IN ('reset_epoch_mismatch','reset_pending')),0) AS reset_epoch FROM local_portal_record_outbox WHERE owner_user_id=? AND owner_workspace_id=? AND module=? AND last_error<>''`,
      [...owner, module],
    )
    const reset = await db.queryOne(
      `SELECT reset_id FROM local_portal_resets WHERE owner_user_id=? AND owner_workspace_id=? AND completed=0 AND instr(modules_json,?)>0`,
      [...owner, JSON.stringify(module)],
    )
    db.assertCurrent()
    if (stamp !== version) return
    Object.assign(status, {
      blocked: row?.count ?? 0,
      epochUnknown: Boolean(row?.unknown_epoch),
      resetBlocked: Boolean(row?.reset_epoch),
      resetPending: Boolean(reset),
    })
    markModuleResetPending(db.owner, module, Boolean(reset))
  }
  const syncNow = (): Promise<number> => {
    if (flight) return flight
    const auth = useAuthStore()
    if (!lobsterReady.value || auth.activationStatus !== 'ready' || !auth.userId || !auth.workspaceId)
      return Promise.resolve(0)
    const cloud = readCachedModuleMode(module) === 'cloud'
    let db: ReturnType<typeof localDB.bindOperation>
    try {
      db = localDB.bindOperation(
        localDB.captureOperation(cloud ? { kind: 'cloud-sync', module } : { kind: 'local-edit' }),
      )
    } catch (error) {
      if (!(error instanceof Error) || error.name !== 'AbortError')
        console.warn('[portal-sync] Module sync admission unavailable:', module, error)
      return Promise.resolve(0)
    }
    const stamp = version
    status.running = true
    flight = (async () => {
      try {
        const store = portalRecordStore(db, adapter)
        await store.init()
        if (!cloud) {
          await refresh(db)
          const epoch = await store.currentEpoch()
          db.assertCurrent()
          if (epoch !== null) rememberModuleEpoch(db.owner, module, epoch)
          if (stamp === version) status.failed = false
          return 0
        }
        const context = await portalTransport(db, { module })
        if (resumePolicy) {
          await store.resumeSkipped()
          resumePolicy = false
        }
        let received = 0
        const count = await syncRecordsOnce(
          store,
          {
            changes: (cursor) =>
              context.request(
                `changes?workspace_id=${encodeURIComponent(db.owner!.workspaceId)}&module=${module}&limit=50&cursor=${encodeURIComponent(cursor)}`,
              ),
            push: (items) =>
              context.request(`push?workspace_id=${encodeURIComponent(db.owner!.workspaceId)}`, {
                method: 'POST',
                body: JSON.stringify({ items }),
              }),
          },
          context.assertCurrent,
          (n) => {
            context.assertCurrent()
            received += n
            window.dispatchEvent(new Event(`openpocket:portal-${module}-applied`))
          },
        )
        await refresh(db)
        context.assertCurrent()
        if (stamp === version) status.failed = false
        if (count || received) recordSyncReportEntry({ module, status: 'synced', count: count || received })
        return count
      } catch (error) {
        try {
          db.assertCurrent()
        } catch {
          return 0
        }
        if (error instanceof Error && error.name === 'AbortError') return 0
        if (stamp === version) status.failed = true
        console.warn('[portal-sync] Local module original retained:', module, error)
        return 0
      } finally {
        if (stamp === version) status.running = false
        flight = null
      }
    })()
    return flight
  }
  const start = () => {
    if (started) return
    started = true
    const auth = useAuthStore(),
      kick = () => {
        void syncNow()
      }
    let mode = readCachedModuleMode(module)
    watch(
      () => [auth.activationVersion, auth.userId, auth.workspaceId, auth.activationStatus],
      () => {
        version++
        Object.assign(status, {
          failed: false,
          running: false,
          blocked: 0,
          resetPending: false,
          resetBlocked: false,
          epochUnknown: false,
        })
        kick()
      },
      { flush: 'sync' },
    )
    watch(() => lobsterReady.value, kick, { immediate: true })
    window.addEventListener('online', kick)
    window.addEventListener(`openpocket:portal-${module}-outbox-changed`, kick)
    window.addEventListener('openpocket:sync-policy-changed', () => {
      const next = readCachedModuleMode(module)
      if (mode === 'local' && next === 'cloud') resumePolicy = true
      mode = next
      kick()
    })
    window.setInterval(kick, 30000)
  }
  return { status, start, syncNow }
}
export type RecordRuntime = ReturnType<typeof createRecordRuntime>
export const recordRuntimes = new Map<RecordModule, RecordRuntime>()
