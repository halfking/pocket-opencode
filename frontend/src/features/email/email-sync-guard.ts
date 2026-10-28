import { assertCloudModule } from '../../api/data-policy'
import { useAuthStore, captureAuthSession, assertAuthSession } from '../../stores/auth'
import { localDB, type OperationLease } from '../../native/local-db'

export interface EmailSyncOperation {
  readonly lease: OperationLease
  readonly db: ReturnType<typeof localDB.bindOperation>
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
  return { lease, db, assertCurrent() {
    assertAuthSession(session.owner, session)
    assertCloudModule('email')
    localDB.assertOperationCurrent(lease)
  } }
}
