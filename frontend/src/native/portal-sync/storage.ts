import { localDB } from '../local-db'
import { encryptString, decryptString } from '../crypto'
import { useCryptoConfig } from '../../stores/crypto-config'
import { PortalNotesStore } from './store.ts'
import type { PortalItem, SyncDb } from './contract.ts'
export { portalRecordStore } from './record-storage.ts'

export function portalNotesStore(db: SyncDb): PortalNotesStore {
  return new PortalNotesStore(db, {
    async encode(item: PortalItem) {
      const json = JSON.stringify(item)
      return useCryptoConfig().shouldEncryptField() ? `e1:${await encryptString(json)}` : json
    },
    async decode(value: string) {
      return JSON.parse(value.startsWith('e1:') ? await decryptString(value.slice(3)) : value) as PortalItem
    },
  })
}

export async function encodePortalBody(content: string): Promise<{ content: string; encrypted: number }> {
  return useCryptoConfig().shouldEncryptField()
    ? { content: await encryptString(content), encrypted: 1 }
    : { content, encrypted: 0 }
}

/** Unclaimed historical rows stay on this device; known rows require the exact owner. */
export async function canReadPortalNote(source: string, id: string, db?: SyncDb): Promise<boolean> {
  db ??= localDB.bindOperation(localDB.captureOperation({ kind: 'local-edit' }))
  const store = portalNotesStore(db)
  await store.init()
  const row = await db.queryOne<{ owner_user_id: string; owner_workspace_id: string }>(
    'SELECT owner_user_id,owner_workspace_id FROM local_portal_notes WHERE source=? AND source_id=?', [source, id])
  db.assertCurrent()
  return !row || Boolean(db.owner && row.owner_user_id === db.owner.userId && row.owner_workspace_id === db.owner.workspaceId)
}

/** Apply ownership before LIMIT/OFFSET; other principals cannot consume a page. */
export async function portalNoteReadScope(source: 'note' | 'asset', alias: string, db?: SyncDb) {
  db ??= localDB.bindOperation(localDB.captureOperation({ kind: 'local-edit' }))
  await portalNotesStore(db).init()
  const owner = db.owner
  const column = `${alias ? `${alias}.` : ''}id`
  const unclaimed = `NOT EXISTS (SELECT 1 FROM local_portal_notes p WHERE p.source=? AND p.source_id=${column})`
  return {
    db,
    clause: owner ? `(${unclaimed} OR EXISTS (SELECT 1 FROM local_portal_notes p WHERE p.source=? AND p.source_id=${column} AND p.owner_user_id=? AND p.owner_workspace_id=?))` : unclaimed,
    values: owner ? [source, source, owner.userId, owner.workspaceId] : [source],
  }
}
