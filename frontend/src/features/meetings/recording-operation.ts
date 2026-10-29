import { localDB, type OperationLease } from '../../native/local-db'
import { assertAuthSession, captureAuthSession, useAuthStore } from '../../stores/auth'
import { assertCloudModule } from '../../api/data-policy'
import { readCachedModuleMode } from '../../stores/syncPolicyStore'
import { effectScope, watch } from 'vue'

export type RecordingModule = 'notes' | 'meetings'
export type RecordingDb = ReturnType<typeof localDB.bindOperation>

export interface RecordingOperation {
  readonly workspaceId: string
  readonly localLease: OperationLease
  readonly localDb: RecordingDb
  readonly cloudLease: OperationLease | null
  readonly cloudDb: RecordingDb | null
  assertLocal(): void
  assertCloud(): void
}

/** Capture once before the first recording/file/decoder await. No historical ownership is inferred. */
export function captureRecordingOperation(module: RecordingModule): RecordingOperation {
  const auth = useAuthStore()
  const session = captureAuthSession(auth)
  const workspaceId = auth.workspaceId
  const localLease = localDB.captureOperation({ kind: 'local-edit' })
  const localDb = localDB.bindOperation(localLease)
  let cloudLease: OperationLease | null = null
  if (readCachedModuleMode(module) === 'cloud') {
    cloudLease = localDB.captureOperation({ kind: 'cloud-sync', module })
  }
  const cloudDb = cloudLease ? localDB.bindOperation(cloudLease) : null
  const assertLocal = () => {
    assertAuthSession(auth, session)
    localDb.assertCurrent()
  }
  return Object.freeze({
    workspaceId, localLease, localDb, cloudLease, cloudDb, assertLocal,
    assertCloud() {
      assertLocal()
      if (!cloudDb) throw new DOMException('Recording has no initiating cloud permission', 'AbortError')
      cloudDb.assertCurrent()
      assertCloudModule(module)
    },
  })
}

/** A local edit remains allowed when cloud policy is revoked. */
export function captureRecordingDb(): RecordingDb {
  return localDB.bindOperation(localDB.captureOperation({ kind: 'local-edit' }))
}

/** Lifecycle cancellation supplements checks at every awaited I/O boundary. */
export function watchRecordingOperation(operation: RecordingOperation, revoked: (ownerChanged: boolean) => void): () => void {
  const auth = useAuthStore()
  let ownerRevoked = false, cloudRevoked = false
  const check = () => {
    try { operation.assertLocal() } catch {
      if (!ownerRevoked) { ownerRevoked = true; revoked(true) }
      return
    }
    try { operation.assertCloud() } catch {
      if (!cloudRevoked) { cloudRevoked = true; revoked(false) }
    }
  }
  const scope = effectScope(true)
  scope.run(() => watch(() => [auth.activationStatus, auth.activationVersion], check, { flush: 'sync' }))
  if (typeof window !== 'undefined') window.addEventListener('openpocket:sync-policy-changed', check)
  return () => {
    scope.stop()
    if (typeof window !== 'undefined') window.removeEventListener('openpocket:sync-policy-changed', check)
  }
}
