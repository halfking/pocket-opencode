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
      // ⚠ 2026-10-29（module-privacy-wire 门禁对齐）：「capture 时模块就已
      //   是 local」 ⇒ 这里没有 cloud lease 可用，正确语义是**策略拒绝**
      //   （ModulePolicyLocalError 409，与 assertCloudModule 同一形态），
      //   不是「操作被中止」（AbortError）。AbortError 只用于
      //   「capture 时有权限、之后被撤销」的竞态——那种场景 cloudDb
      //   存在但 assertCurrent/assertCloudModule 会抛。
      if (!cloudDb) assertCloudModule(module)
      cloudDb?.assertCurrent()
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
