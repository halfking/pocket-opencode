import { http } from '../../api/http'
import { newOperationId, versionKey } from '../../native/portal-sync/contract.ts'
import type { RecordItem } from '../../native/portal-sync/record-contract.ts'
import { rssSyncAdapter } from './rss-sync-adapter'
import { rssPortalStore, type RssPortalOperation } from './rss-sync-store'

interface NativeSnapshot {
  id: string
  user_id: string
  workspace_id: string
  title: string
  url: string
  category?: string
  source_id?: string
  summary?: string
  updated_at: string
}

export async function prepareLegacyRssSnapshot(kind: 'source' | 'item', id: string, operation: RssPortalOperation) {
  operation.assertCurrent()
  const store = rssPortalStore(operation.db)
  const nativeId = `pocketd-${kind}:${id}`
  // Article archives reuse the ID of the existing explicit shared-copy action.
  const externalId = kind === 'source' ? nativeId : `rss-item:${nativeId}`
  const sourceId = kind === 'source' ? `native:source:${nativeId}` : nativeId
  const existing = [externalId, nativeId]
  await store.init()
  const prior = await operation.db.queryOne(`SELECT 1 FROM local_portal_records
    WHERE owner_user_id=? AND owner_workspace_id=? AND module='rss' AND kind=? AND ext_id IN (?,?)`,
    [...store.scope, kind, ...existing])
  operation.assertCurrent()
  if (prior) return null

  async function readSnapshot() {
    operation.assertCurrent()
    const collection = kind === 'source' ? 'sources' : 'items'
    const snapshot = await http<NativeSnapshot>(`/api/rss/${collection}/${encodeURIComponent(id)}/migration-snapshot`, {
      method: 'GET', syncModule: 'rss', cache: 'no-store', assertCurrent: operation.assertCurrent,
    })
    operation.assertCurrent()
    if (snapshot?.id !== id || snapshot.user_id !== operation.owner.userId
      || snapshot.workspace_id !== operation.owner.workspaceId || typeof snapshot.title !== 'string'
      || typeof snapshot.updated_at !== 'string' || typeof snapshot.url !== 'string'
      || /[?#]/.test(snapshot.url)) {
      throw new Error('Invalid historical RSS snapshot')
    }
    if (kind === 'source' ? typeof snapshot.category !== 'string'
      : typeof snapshot.source_id !== 'string' || !snapshot.source_id.trim() || typeof snapshot.summary !== 'string') {
      throw new Error('Missing historical RSS metadata')
    }
    const payload = rssSyncAdapter.payload(kind === 'source'
      ? { url: snapshot.url, category: snapshot.category }
      : { url: snapshot.url, source: snapshot.source_id, summary: snapshot.summary }, kind)
    const bytes = new TextEncoder()
    if (bytes.encode(externalId).length > 512 || bytes.encode(snapshot.title).length > 4096
      || bytes.encode(JSON.stringify(payload)).length > 256 * 1024) {
      throw new Error('Historical RSS snapshot exceeds the portable metadata limits')
    }
    return { title: snapshot.title, payload, timestamp: snapshot.updated_at, version: versionKey(snapshot.updated_at) }
  }

  const original = await readSnapshot()
  return {
    title: original.title || String(original.payload.url),
    workspaceId: operation.owner.workspaceId,
    async save() {
      const latest = await readSnapshot()
      if (JSON.stringify(latest) !== JSON.stringify(original)) throw new Error('Original RSS snapshot changed during confirmation')
      const item: RecordItem = {
        module: 'rss', kind, ext_id: externalId, title: original.title,
        payload: original.payload, client_updated_at: original.timestamp, op_id: newOperationId(), deleted: false, epoch: null,
      }
      const json = await store.encode(item)
      operation.assertCurrent()
      await operation.db.runInTransaction([
        { statement: `INSERT INTO local_portal_records
           (owner_user_id,owner_workspace_id,module,kind,ext_id,source_id,item_json,version_key,op_id,reset_epoch)
           SELECT ?,?,'rss',?,?,?,?,?,?,NULL
           WHERE NOT EXISTS (SELECT 1 FROM local_portal_records
             WHERE owner_user_id=? AND owner_workspace_id=? AND module='rss' AND kind=? AND ext_id IN (?,?))
           ON CONFLICT DO NOTHING`,
          values: [...store.scope, kind, externalId, sourceId, json, original.version, item.op_id, ...store.scope, kind, ...existing] },
      ])
      const saved = await store.byExternalId(externalId, kind)
      operation.assertCurrent()
      return saved?.op_id === item.op_id
    },
  }
}
