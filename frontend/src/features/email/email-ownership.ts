import { localDB } from '../../native/local-db'
import { ownerScopeSql, ownerColumnValues, type MeetingOwner } from '../../native/meeting-ownership'

/** Capture before awaiting: row scope and the database lease must share a principal. */
export function captureEmailOwnership(database?: object) {
  const db = database && database !== localDB
    ? database as ReturnType<typeof localDB.bindOperation>
    : localDB.bindOperation(localDB.captureOperation({ kind: 'local-edit' }))
  const owner: MeetingOwner | null = db.owner ?? null
  return { db, owner, scope: ownerScopeSql(owner), ownerValues: ownerColumnValues(owner) }
}
