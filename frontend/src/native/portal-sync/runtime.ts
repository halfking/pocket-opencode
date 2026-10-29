import { watch } from 'vue'
import { localDB } from '../local-db'
import { lobsterReady } from '../lobster-init'
import { useAuthStore } from '../../stores/auth'
import { readCachedModuleMode, recordSyncReportEntry } from '../../stores/syncPolicyStore'
import { portalNotesStore, encodePortalBody } from './storage.ts'
import { portalTransport } from './transport.ts'
import { syncNotesOnce } from './engine.ts'
import { portalNotesSyncStatus, refreshNotesSyncStatus, invalidateNotesStatusReads } from './status.ts'
import { rememberNotesEpoch, markNotesResetPending } from './epochs.ts'
export { portalNotesSyncStatus } from './status.ts'

let started = false
let flight: Promise<void> | null = null
let resumePolicy = false

/** Startup, online, policy and timer triggers share one owner-bound flight. */
export function syncPortalNotesNow(): Promise<void> {
  if (flight) return flight
  const auth = useAuthStore()
  if (!lobsterReady.value || auth.activationStatus !== 'ready' || !auth.userId || !auth.workspaceId) return Promise.resolve()
  const cloud = readCachedModuleMode('notes') === 'cloud'
  let db: ReturnType<typeof localDB.bindOperation>
  try { db = localDB.bindOperation(localDB.captureOperation(cloud ? { kind: 'cloud-sync', module: 'notes' } : { kind: 'local-edit' })) } catch (error) {
    if (!(error instanceof Error) || error.name !== 'AbortError') console.warn('[portal-sync] 笔记同步尚未获准：', error)
    return Promise.resolve()
  }
  portalNotesSyncStatus.running = true
  flight = (async () => {
    try {
      const store = portalNotesStore(db)
      await store.init()
      if (!cloud) {
        await refreshNotesSyncStatus(db, store.scope)
        const epoch = await store.currentEpoch()
        db.assertCurrent()
        markNotesResetPending(db.owner, portalNotesSyncStatus.resetPending)
        if (epoch !== null) rememberNotesEpoch(db.owner, epoch)
        portalNotesSyncStatus.failed = false
        return
      }
      const context = await portalTransport(db)
      if (resumePolicy) { await store.resumeSkipped(); resumePolicy = false }
      let received = 0
      const count = await syncNotesOnce(store, context.transport, context.assertCurrent, encodePortalBody, count => {
        context.assertCurrent()
        received += count
        if (typeof window !== 'undefined') window.dispatchEvent(new Event('openpocket:portal-notes-applied'))
      })
      context.assertCurrent()
      await refreshNotesSyncStatus(db, store.scope)
      context.assertCurrent()
      portalNotesSyncStatus.failed = false
      if (count || received) recordSyncReportEntry({ module: 'notes', status: 'synced', count: count || received })
    } catch (error) {
      try { db.assertCurrent() } catch { return } // A former account cannot publish status for the new one.
      if (error instanceof Error && error.name === 'AbortError') return
      portalNotesSyncStatus.failed = true
      console.warn('[portal-sync] 笔记仍保留在本机，同步稍后重试：', error)
    } finally {
      portalNotesSyncStatus.running = false
      flight = null
    }
  })()
  return flight
}

export function startPortalNotesSync(): void {
  if (started) return
  started = true
  const auth = useAuthStore()
  const kick = () => { void syncPortalNotesNow() }
  let previousMode = readCachedModuleMode('notes')
  watch(() => [auth.activationVersion, auth.userId, auth.workspaceId, auth.activationStatus], () => {
    invalidateNotesStatusReads()
    portalNotesSyncStatus.failed = false
    portalNotesSyncStatus.blocked = 0
    portalNotesSyncStatus.resetPending = false
    portalNotesSyncStatus.resetBlocked = false
    portalNotesSyncStatus.epochUnknown = false
    kick()
  }, { flush: 'sync' })
  watch(() => lobsterReady.value, kick, { immediate: true })
  window.addEventListener('online', kick)
  window.addEventListener('openpocket:portal-notes-outbox-changed', kick)
  window.addEventListener('openpocket:sync-policy-changed', () => {
    const mode = readCachedModuleMode('notes')
    if (previousMode === 'local' && mode === 'cloud') resumePolicy = true
    previousMode = mode
    kick()
  })
  window.setInterval(kick, 30000)
}
