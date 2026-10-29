import { assertCloudModule } from '../../api/data-policy'
import { useAuthStore, captureAuthSession, assertAuthSession } from '../../stores/auth'
import { localDB, type OperationLease } from '../../native/local-db'
import type { ConfigOwner } from '../../native/config-sync/ownership'

export interface EmailSyncOperation {
  readonly lease: OperationLease
  readonly db: ReturnType<typeof localDB.bindOperation>
  /**
   * 本次操作的行归属（来自**同一个租约**的 owner principal）。
   *
   * ⚠ 恢复出来的会话 principal 为 null ⇒ owner 为 null ⇒ 写下的行没有归属，
   *   永不上传（见 email-sync-guard 里那句「assigns no ownership to historical rows」）。
   */
  readonly owner: ConfigOwner | null
  assertCurrent(): void
}

/** Capture once at the business entry, before any HTTP/DB/import await. */
export function captureEmailSyncOperation(): EmailSyncOperation {
  const auth = useAuthStore()
  const session = captureAuthSession(auth)
  assertCloudModule('email')
  const lease = localDB.captureOperation({ kind: 'cloud-sync', module: 'email' })
  // Restored sessions may deliberately have an unknown principal. Their opaque
  // epoch fences this operation; it assigns no ownership to historical rows.
  const db = localDB.bindOperation(lease)
  const principal = lease.owner.principal
  return { lease, db, owner: principal ? { userId: principal.userId, workspaceId: principal.workspaceId } : null, assertCurrent() {
    assertAuthSession(session.owner, session)
    assertCloudModule('email')
    localDB.assertOperationCurrent(lease)
  } }
}
