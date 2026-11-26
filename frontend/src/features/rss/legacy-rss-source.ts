import { http } from '../../api/http'
import { newOperationId, versionKey } from '../../native/portal-sync/contract.ts'
import type { RecordItem } from '../../native/portal-sync/record-contract.ts'
import { rssSyncAdapter } from './rss-sync-adapter'
import { rssPortalStore, type RssPortalOperation } from './rss-sync-store'

interface SourceSnapshot {
  id: string
  user_id: string
  workspace_id: string
  title: string
  url: string
  category: string
  updated_at: string
}

export async function prepareLegacyRssSource(id: string, operation: RssPortalOperation) {
  operation.assertCurrent()
  const store = rssPortalStore(operation.db)
  const externalId = `pocketd-source:${id}`
  const sourceId = `native:source:${externalId}`
  if (await store.byExternalId(externalId, 'source')) return null

  async function readSnapshot() {
    operation.assertCurrent()
    const snapshot = await http<SourceSnapshot>(`/api/rss/sources/${encodeURIComponent(id)}/migration-snapshot`, {
      method: 'GET', syncModule: 'rss', cache: 'no-store', assertCurrent: operation.assertCurrent,
    })
    operation.assertCurrent()
    if (snapshot?.id !== id || snapshot.user_id !== operation.owner.userId
      || snapshot.workspace_id !== operation.owner.workspaceId || typeof snapshot.title !== 'string'
      || typeof snapshot.updated_at !== 'string' || typeof snapshot.url !== 'string'
      || /[?#]/.test(snapshot.url) || typeof snapshot.category !== 'string') {
      throw new Error('Invalid historical RSS source snapshot')
    }
    const payload = rssSyncAdapter.payload({ url: snapshot.url, category: snapshot.category }, 'source')
    const bytes = new TextEncoder()
    if (bytes.encode(externalId).length > 512 || bytes.encode(snapshot.title).length > 4096
      || bytes.encode(JSON.stringify(payload)).length > 256 * 1024) {
      throw new Error('Historical RSS source exceeds the portable metadata limits')
    }
    return { title: snapshot.title, payload, timestamp: snapshot.updated_at, version: versionKey(snapshot.updated_at) }
  }

  const original = await readSnapshot()
  return {
    title: original.title || String(original.payload.url),
    workspaceId: operation.owner.workspaceId,
    async save() {
      const latest = await readSnapshot()
      if (JSON.stringify(latest) !== JSON.stringify(original)) throw new Error('Original RSS source changed during confirmation')
      const item: RecordItem = {
        module: 'rss', kind: 'source', ext_id: externalId, title: original.title,
        payload: original.payload, client_updated_at: original.timestamp, op_id: newOperationId(), deleted: false, epoch: null,
      }
      const json = await store.encode(item)
      operation.assertCurrent()
      await operation.db.runInTransaction([
        { statement: `INSERT INTO local_portal_records
           (owner_user_id,owner_workspace_id,module,kind,ext_id,source_id,item_json,version_key,op_id,reset_epoch)
           VALUES(?,?,'rss','source',?,?,?,?,?,NULL) ON CONFLICT DO NOTHING`,
          values: [...store.scope, externalId, sourceId, json, original.version, item.op_id] },
      ])
      const saved = await store.byExternalId(externalId, 'source')
      operation.assertCurrent()
      return saved?.op_id === item.op_id
    },
  }
}
