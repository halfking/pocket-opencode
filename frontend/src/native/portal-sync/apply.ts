import { remoteNotePayload, validateItem, versionKey, newOperationId, type ChangePage, type Statement } from './contract.ts'
import { PortalNotesStore } from './store.ts'
import { epochGuard } from './epoch-state.ts'

type EncodeBody = (content: string) => Promise<{ content: string; encrypted: number }>
const plaintext: EncodeBody = async content => ({ content, encrypted: 0 })

/** Apply the sync projection only. Audio, attachment references and files are retained. */
export async function applyPage(store: PortalNotesStore, page: ChangePage, encodeBody: EncodeBody = plaintext): Promise<void> {
  if (!store.db.owner) throw new Error('Verified notes owner is required')
  await store.init()
  const epoch = page.epochs.notes
  await store.observeEpoch(epoch)
  const admitted = epochGuard(store.db, epoch)
  const statements: Statement[] = []
  for (const change of page.changes) {
    const item = { ...change, epoch }
    validateItem(item, true)
    const existing = await store.byExternalId(item.ext_id)
    const payload = item.deleted ? null : remoteNotePayload(item.payload)
    const source = existing?.source ?? (payload?.format === 'html' || (item.deleted && item.ext_id.startsWith('asset:')) ? 'asset' : 'note')
    const id = existing?.source_id ?? `portal-${newOperationId()}`
    const modified = Date.parse(item.client_updated_at)
    const key = versionKey(item.client_updated_at)
    statements.push({ statement: `INSERT INTO local_portal_notes
      (owner_user_id,owner_workspace_id,ext_id,source,source_id,item_json,version_ms,version_key,op_id,reset_epoch)
      SELECT ?,?,?,?,?,?,?,?,?,? WHERE ${admitted.clause} ON CONFLICT(owner_user_id,owner_workspace_id,ext_id)
      DO UPDATE SET item_json=excluded.item_json,version_ms=excluded.version_ms,version_key=excluded.version_key,op_id=excluded.op_id,reset_epoch=excluded.reset_epoch
      WHERE excluded.reset_epoch > COALESCE(local_portal_notes.reset_epoch,0)
         OR (excluded.reset_epoch = COALESCE(local_portal_notes.reset_epoch,0) AND (excluded.version_key > local_portal_notes.version_key
         OR (excluded.version_key = local_portal_notes.version_key AND excluded.op_id > local_portal_notes.op_id)))`,
      values: [...store.scope, item.ext_id, source, id, await store.encode(item), modified, key, item.op_id, epoch, ...admitted.values] })
    const guard = `EXISTS (SELECT 1 FROM local_portal_notes WHERE owner_user_id=? AND owner_workspace_id=? AND ext_id=? AND op_id=? AND version_key=? AND reset_epoch=?)
      AND ${admitted.clause} AND NOT EXISTS(SELECT 1 FROM local_portal_outbox WHERE owner_user_id=? AND owner_workspace_id=? AND ext_id=? AND (reset_epoch IS NULL OR reset_epoch<>?))`
    const guardValues = [...store.scope, item.ext_id, item.op_id, key, epoch, ...admitted.values, ...store.scope, item.ext_id, epoch]
    if (item.deleted) {
      statements.push({ statement: `UPDATE ${source === 'note' ? 'local_notes' : 'local_assets'} SET deleted_at=?,updated_at=? WHERE id=? AND workspace_id=? AND ${guard}`,
        values: [modified, modified, id, store.db.owner.workspaceId, ...guardValues] })
      continue
    }
    const encoded = await encodeBody(payload!.content)
    store.db.assertCurrent()
    const created = payload!.created_at || modified
    if (source === 'note') {
      statements.push({ statement: `INSERT INTO local_notes
        (id,workspace_id,title,content,encrypted_content,content_type,domain,tags,created_at,updated_at,status,summary,search_text,storage_tier,created_by_voice)
        SELECT ?,?,?,?,?,?,?,?,?,?,?,?,?,'inline',0 WHERE ${guard}
        ON CONFLICT(id) DO UPDATE SET title=excluded.title,content=excluded.content,encrypted_content=excluded.encrypted_content,
          content_type=excluded.content_type,domain=excluded.domain,tags=excluded.tags,updated_at=excluded.updated_at,
          status=excluded.status,summary=excluded.summary,search_text=excluded.search_text,storage_tier='inline',body_path=NULL,deleted_at=NULL
        WHERE local_notes.workspace_id=excluded.workspace_id`,
        values: [id, store.db.owner.workspaceId, item.title, encoded.content, encoded.encrypted, payload!.content_type || 'text',
          payload!.domain, JSON.stringify(payload!.tags), created, modified, payload!.status, payload!.summary,
          encoded.encrypted ? '' : `${item.title}\n${payload!.content}`, ...guardValues] })
    } else {
      statements.push({ statement: `INSERT INTO local_assets
        (id,workspace_id,kind,title,body_text,body_encrypted,meta_json,source,sync_mode,created_at,updated_at,dirty)
        SELECT ?,?,'note',?,?,?,?, 'portal_sync','e2ee_local_first',?,?,0 WHERE ${guard}
        ON CONFLICT(id) DO UPDATE SET title=excluded.title,body_text=excluded.body_text,body_encrypted=excluded.body_encrypted,
          meta_json=json_patch(COALESCE(local_assets.meta_json,'{}'),excluded.meta_json),updated_at=excluded.updated_at,deleted_at=NULL
        WHERE local_assets.workspace_id=excluded.workspace_id`,
        values: [id, store.db.owner.workspaceId, item.title, encoded.content, encoded.encrypted,
          JSON.stringify({ tags: payload!.tags, links: payload!.links, dailyDate: payload!.daily_date, summary: payload!.summary }), created, modified, ...guardValues] })
    }
  }
  // A failed materialization rolls back both the versions and the cursor.
  statements.push(store.cursorStatement(page.next_cursor, epoch))
  await store.db.runInTransaction(statements)
  await store.requireEpoch(epoch)
}
