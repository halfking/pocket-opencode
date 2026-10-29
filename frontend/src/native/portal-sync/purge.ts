import { portalNotesStore } from './storage.ts'
import { portalTransport } from './transport.ts'
import { PortalResetStore } from './reset-store.ts'
import { portalNotesSyncStatus, invalidateNotesStatusReads } from './status.ts'
import type { PortalPurgeOperation } from './purge-operation.ts'

export async function purgePortalCloudCopy(operation: PortalPurgeOperation) {
  operation.assertCurrent()
  const { db, modules } = operation
  const context = await portalTransport(db, { cloud: false, admit: operation.assertCurrent })
  const resets = new PortalResetStore(portalNotesStore(db))
  context.assertCurrent()
  const intent = await resets.begin([...modules])
  context.assertCurrent()
  if (modules.includes('notes')) { invalidateNotesStatusReads(); portalNotesSyncStatus.resetPending = true }
  const response = await context.purge(intent.modules, intent.reset_id)
  context.assertCurrent()
  const result = await resets.complete(intent, response)
  context.assertCurrent()
  if (modules.includes('notes')) {
    const blocked = await db.queryOne<{ count: number }>(`SELECT COUNT(*) AS count FROM local_portal_outbox WHERE owner_user_id=? AND owner_workspace_id=? AND last_error<>''`, [db.owner!.userId, db.owner!.workspaceId])
    context.assertCurrent()
    invalidateNotesStatusReads()
    portalNotesSyncStatus.resetPending = false
    portalNotesSyncStatus.resetBlocked = true
    portalNotesSyncStatus.blocked = blocked?.count ?? 0
  }
  if (typeof window !== 'undefined') for(const module of modules) window.dispatchEvent(new Event(`openpocket:portal-${module}-outbox-changed`))
  context.assertCurrent()
  return result
}
