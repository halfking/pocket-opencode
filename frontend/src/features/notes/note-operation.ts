import { localDB, type OperationLease } from '../../native/local-db'
import { assertCloudModule } from '../../api/data-policy'
import { assertAuthSession, captureAuthSession, useAuthStore } from '../../stores/auth'
import { readCachedModuleMode } from '../../stores/syncPolicyStore'

export interface NoteWriteOptions {
  /** A caller with an earlier async entry must retain that original lease. */
  lease?: OperationLease
  /** Meeting/local ingestion can save without starting separate notes uploads. */
  backgroundCloud?: boolean
}

export type NoteDbOperation = ReturnType<typeof localDB.bindOperation>
export interface NoteCloudOperation {
  readonly db: NoteDbOperation
  assertCurrent(): void
}
export interface NoteWriteOperation {
  readonly db: NoteDbOperation
  readonly cloud: NoteCloudOperation | null
  assertCurrent(): void
}

/** Capture before encryption, files, imports or HTTP; never infer historical ownership. */
export function captureNoteWriteOperation(options: NoteWriteOptions = {}): NoteWriteOperation {
  const lease = options.lease ?? localDB.captureOperation({ kind: 'local-edit' })
  const db = localDB.bindOperation(lease)
  db.assertCurrent()
  let cloud: NoteCloudOperation | null = null
  if (options.backgroundCloud !== false && readCachedModuleMode('notes') === 'cloud') {
    const auth = useAuthStore()
    if (auth.token) {
      const session = captureAuthSession(auth)
      const cloudDb = localDB.bindOperation(localDB.captureOperation({ kind: 'cloud-sync', module: 'notes' }))
      cloud = Object.freeze({ db: cloudDb, assertCurrent() {
        db.assertCurrent()
        assertAuthSession(auth, session)
        assertCloudModule('notes')
        cloudDb.assertCurrent()
      } })
    }
  }
  return Object.freeze({ db, cloud, assertCurrent: () => db.assertCurrent() })
}
