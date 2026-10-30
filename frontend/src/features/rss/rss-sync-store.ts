import { assertAuthSession, captureAuthSession, useAuthStore } from '../../stores/auth'
import { localDB } from '../../native/local-db'
import { newOperationId, type SyncDb } from '../../native/portal-sync/contract.ts'
import { captureModuleEpoch } from '../../native/portal-sync/epochs.ts'
import { portalRecordStore } from '../../native/portal-sync/record-storage.ts'
import type { RecordItem } from '../../native/portal-sync/record-contract.ts'
import { rssSyncAdapter } from './rss-sync-adapter'

export interface RssPortalEntry { id: string; owner: {userId: string; workspaceId: string}; item: RecordItem }
export interface RssPortalInput { kind: 'source' | 'item'; title: string; payload: Record<string, unknown> }

export function rssPortalStore(db: SyncDb) { return portalRecordStore(db, rssSyncAdapter) }

/** Capture before confirmation, lazy loading, encryption or other awaits. */
export function captureRssPortalOperation() {
  const auth = useAuthStore(), session = captureAuthSession(auth)
  const db = localDB.bindOperation(localDB.captureOperation({kind: 'local-edit'}))
  if (!db.owner) throw new Error('Verified RSS owner required')
  const owner = Object.freeze({...db.owner}), epoch = captureModuleEpoch(owner, 'rss')
  return {db, owner, epoch, modifiedAt: Date.now(), assertCurrent() {assertAuthSession(auth, session);db.assertCurrent();assertAuthSession(auth, session)}}
}
export type RssPortalOperation = ReturnType<typeof captureRssPortalOperation>

function currentEntry(entry: RssPortalEntry, operation: RssPortalOperation) {
  operation.assertCurrent()
  if (entry.owner.userId !== operation.owner.userId || entry.owner.workspaceId !== operation.owner.workspaceId) throw new DOMException('RSS entry belongs to a former owner', 'AbortError')
}

export async function listRssPortalRecords(options: {kind?: 'source' | 'item'; query?: string; limit?: number; offset?: number} = {}, operation = captureRssPortalOperation()): Promise<RssPortalEntry[]> {
  operation.assertCurrent()
  const store = rssPortalStore(operation.db)
  await store.init()
  // Owner/module selection happens before sorting, search and pagination.
  const rows = await operation.db.query<{source_id: string;item_json: string}>(`SELECT source_id,item_json FROM local_portal_records WHERE owner_user_id=? AND owner_workspace_id=? AND module='rss' ORDER BY version_key DESC,op_id DESC`, store.scope)
  const decoded = await Promise.all(rows.map(async row => ({id: row.source_id, owner: {...operation.owner}, item: await store.decode(row.item_json)})))
  operation.assertCurrent()
  const query = (options.query ?? '').toLocaleLowerCase()
  return decoded.filter(row => !row.item.deleted && ['source','item'].includes(row.item.kind) && (!options.kind || row.item.kind === options.kind) && (!query || `${row.item.title}\n${JSON.stringify(row.item.payload)}`.toLocaleLowerCase().includes(query)))
    .slice(Math.max(0,options.offset ?? 0),Math.max(0,options.offset ?? 0)+Math.max(1,Math.min(options.limit ?? 100,500)))
}

export async function createRssPortalRecord(input: RssPortalInput, operation = captureRssPortalOperation()): Promise<RssPortalEntry> {
  const kind = input.kind, title = input.title, payload = rssSyncAdapter.payload({...input.payload}, kind), id = newOperationId()
  operation.assertCurrent()
  const store = rssPortalStore(operation.db)
  await store.commit([], {id,kind,extId:`rss-${kind}:${id}`,title,payload,modifiedAt:operation.modifiedAt,epoch:operation.epoch}, true)
  operation.assertCurrent()
  const row = await store.find(id)
  if (!row) throw new Error('RSS original was not saved')
  return {id,owner:{...operation.owner},item:await store.decode(row.item_json)}
}

export async function saveRssPortalRecord(entry: RssPortalEntry, input: {title: string;payload: Record<string,unknown>}, operation = captureRssPortalOperation()): Promise<void> {
  currentEntry(entry,operation)
  const id = entry.id, kind = entry.item.kind, title = input.title, payload = rssSyncAdapter.payload({...input.payload},kind)
  const store = rssPortalStore(operation.db)
  if (!await store.find(id)) throw new Error('RSS original is no longer available')
  operation.assertCurrent()
  await store.commit([], {id,kind,title,payload,modifiedAt:operation.modifiedAt,epoch:operation.epoch}, false)
  operation.assertCurrent()
}

export async function deleteRssPortalRecord(entry: RssPortalEntry, operation = captureRssPortalOperation()): Promise<void> {
  currentEntry(entry,operation)
  const id=entry.id,kind=entry.item.kind,title=entry.item.title
  const store = rssPortalStore(operation.db)
  if (!await store.find(id)) throw new Error('RSS original is no longer available')
  operation.assertCurrent()
  await store.commit([], {id,kind,title,payload:{},deleted:true,modifiedAt:operation.modifiedAt,epoch:operation.epoch}, false)
  operation.assertCurrent()
}

/** Explicitly create/update a portable copy; this is not a background feed scan. */
export async function copyRssItemToPortal(input: {id:string;title:string;sourceId:string;summary?:string;url:string}, operation = captureRssPortalOperation()): Promise<void> {
  const id=`pocketd-item:${input.id}`, title=input.title
  const payload=rssSyncAdapter.payload({source:input.sourceId,summary:input.summary??'',url:input.url},'item')
  operation.assertCurrent()
  const store=rssPortalStore(operation.db),existing=await store.find(id)
  operation.assertCurrent()
  await store.commit([], {id,kind:'item',extId:`rss-item:${id}`,title,payload,modifiedAt:operation.modifiedAt,epoch:operation.epoch},!existing)
  operation.assertCurrent()
}
