import { watch } from 'vue'
import { localDB } from '../local-db'
import { useAuthStore, captureAuthSession, assertAuthSession } from '../../stores/auth'
import { useSyncPolicyStore, readCachedModuleMode } from '../../stores/syncPolicyStore'
import type { PortalSyncModule } from '../../api/syncPolicyContract.ts'

/** Capture at the public entry before dynamic imports, binding fetches or any other await. */
export function capturePortalPurgeOperation(input: PortalSyncModule[]) {
  const modules = Object.freeze([...new Set(input)].sort())
  const auth = useAuthStore(), session = captureAuthSession(auth)
  const db = localDB.bindOperation(localDB.captureOperation({ kind: 'local-edit' }))
  const policy = useSyncPolicyStore(), revision = policy.revision, generation = policy.generation
  let revoked = false
  const stop = watch(() => [policy.effective, policy.activation], () => { revoked = true }, { flush: 'sync' })
  const assertCurrent = () => {
    db.assertCurrent()
    assertAuthSession(auth, session)
    if (!db.owner || auth.userId !== db.owner.userId || auth.workspaceId !== db.owner.workspaceId || revoked ||
      !policy.activationReady || policy.activating || policy.revision !== revision || policy.generation !== generation ||
      modules.some(module => readCachedModuleMode(module) !== 'local')) {
      throw Object.assign(new Error('Cloud reset owner or policy changed'), { name: 'AbortError' })
    }
  }
  try { assertCurrent() } catch (error) { stop(); throw error }
  return Object.freeze({ modules, db, assertCurrent, dispose: stop })
}
export type PortalPurgeOperation = ReturnType<typeof capturePortalPurgeOperation>
