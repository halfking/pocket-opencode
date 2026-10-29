import { assertAuthSession, captureAuthSession, useAuthStore } from '../../stores/auth'
import { http } from '../../api/http'
import { localDB } from '../../native/local-db'
import { compareVersions, versionKey, type Statement, type SyncDb } from '../../native/portal-sync/contract.ts'
import { validEpoch } from '../../native/portal-sync/epochs.ts'
import { recordEpochGuard } from '../../native/portal-sync/record-epoch.ts'
import { validateRecordItem, type RecordItem } from '../../native/portal-sync/record-contract.ts'
import { rssPortalStore } from './rss-sync-store'
import { rssSyncAdapter } from './rss-sync-adapter'

interface NativeChange {
  sequence: number; kind: 'source'|'item'; ext_id: string; title: string; payload: Record<string,unknown>
  client_updated_at: string|null; op_id: string; epoch: number|null; deleted: boolean
  intent: 'insert'|'update'|'delete'|'feed'; fields: string[]; sync_error: string
}
interface NativePage {user_id:string;workspace_id:string;changes:NativeChange[];next_cursor:number}
const tables = [
  `CREATE TABLE IF NOT EXISTS local_rss_native_cursor(owner_user_id TEXT NOT NULL,owner_workspace_id TEXT NOT NULL,cursor INTEGER NOT NULL,PRIMARY KEY(owner_user_id,owner_workspace_id))`,
  `CREATE TABLE IF NOT EXISTS local_rss_native_events(owner_user_id TEXT NOT NULL,owner_workspace_id TEXT NOT NULL,sequence INTEGER NOT NULL,item_json TEXT NOT NULL,reason TEXT NOT NULL,PRIMARY KEY(owner_user_id,owner_workspace_id,sequence))`,
]

export function captureNativeRssOperation() {
  const auth=useAuthStore(),session=captureAuthSession(auth)
  const db=localDB.bindOperation(localDB.captureOperation({kind:'cloud-sync',module:'rss'}))
  if(!db.owner)throw new Error('Verified RSS owner required')
  return {db,assertCurrent(){assertAuthSession(auth,session);db.assertCurrent();assertAuthSession(auth,session)}}
}

async function init(db:SyncDb){await rssPortalStore(db).init();for(const statement of tables)await db.run(statement)}
export async function nativeRssCursor(db:SyncDb):Promise<number>{
  await init(db)
  const row=await db.queryOne<{cursor:number}>(`SELECT cursor FROM local_rss_native_cursor WHERE owner_user_id=? AND owner_workspace_id=?`,[db.owner!.userId,db.owner!.workspaceId])
  return row?.cursor??0
}

function parsePage(raw:unknown,db:SyncDb,cursor:number):NativePage{
  const page=raw as NativePage
  if(!page||page.user_id!==db.owner?.userId||page.workspace_id!==db.owner?.workspaceId||!Array.isArray(page.changes)||page.changes.length>100||!Number.isSafeInteger(page.next_cursor)||page.next_cursor<cursor)throw new Error('Invalid RSS source change scope')
  let last=cursor
  for(const change of page.changes){
    if(!change||!Number.isSafeInteger(change.sequence)||change.sequence<=last||!['source','item'].includes(change.kind)||typeof change.ext_id!=='string'||!change.ext_id.startsWith(`pocketd-${change.kind}:`)||typeof change.title!=='string'||typeof change.op_id!=='string'||!/^pocketd-rss:[0-9a-f-]{36}$/.test(change.op_id)||typeof change.deleted!=='boolean'||!['insert','update','delete','feed'].includes(change.intent)||!Array.isArray(change.fields)||change.fields.some(field=>!['title','url','category','source','summary'].includes(field))||typeof change.sync_error!=='string'||!change.payload||typeof change.payload!=='object'||Array.isArray(change.payload)||(change.epoch!==null&&!validEpoch(change.epoch))||(change.client_updated_at!==null&&typeof change.client_updated_at!=='string'))throw new Error('Invalid RSS source change')
    if(change.client_updated_at!==null)versionKey(change.client_updated_at)
    last=change.sequence
  }
  if(page.next_cursor!==last)throw new Error('RSS source cursor did not match its changes')
  return page
}

/** Apply one durable source event and its checkpoint atomically. Never reassign
 * a new epoch or manufacture a timestamp for delayed or undated feed data. */
async function applyChange(db:SyncDb,change:NativeChange,cursor:number):Promise<void>{
  const store=rssPortalStore(db),owner=store.scope
  const existing=await store.byExternalId(change.ext_id,change.kind)
  const currentEpoch=await store.currentEpoch()
  if(!validEpoch(currentEpoch))throw new Error('RSS native snapshot awaits a verified reset epoch')
  let payload=change.payload,title=change.title,reason=change.sync_error
  if(change.epoch!==currentEpoch)reason||=change.epoch===null?'epoch_unknown':'reset_epoch_mismatch'
  const prior=existing?await store.decode(existing.item_json):null
  if(change.intent==='update'&&prior&&!prior.deleted&&!change.deleted){
    payload={...prior.payload}
    for(const field of change.fields)if(field!=='title')payload[field]=change.payload[field]
    if(!change.fields.includes('title'))title=prior.title
  }
  const item:RecordItem={module:'rss',kind:change.kind,ext_id:change.ext_id,title,payload:change.deleted?{}:payload,client_updated_at:change.client_updated_at??'',op_id:change.op_id,deleted:change.deleted,epoch:change.epoch}
  if(!reason&&!item.deleted&&typeof item.payload.url==='string'&&item.payload.url){
    // A background importer cannot tell a query token from ordinary metadata.
    // Keep the exact original locally rather than strip it or export a secret.
    try{const url=new URL(item.payload.url);if(url.username||url.password||url.search||url.hash)reason='native_url_review_required'}catch{reason='native_url_review_required'}
  }
  if(!reason){try{validateRecordItem(rssSyncAdapter,item)}catch(error){reason=error instanceof Error?error.message:String(error)}}
  // Portal metadata edits are independent user intentions. A later fetch cannot
  // overwrite one merely because its source updated a timestamp.
  if(!reason&&change.intent==='feed'&&prior&&!/^pocketd-rss:[0-9a-f-]{36}$/.test(prior.op_id))reason='portal_edit_preserved'
  if(!reason&&prior&&prior.op_id!==item.op_id&&compareVersions(item,prior)<=0)reason='portable_version_conflict'
  const json=await store.encode(item),statements:Statement[]=[]
  const snapshotGuard=recordEpochGuard(db,'rss',currentEpoch)
  statements.push({statement:`INSERT INTO local_portal_record_write_guards(allowed) SELECT 0 WHERE NOT (${snapshotGuard.clause})`,values:snapshotGuard.values})
  const observed=existing?{clause:'EXISTS(SELECT 1 FROM local_portal_records WHERE owner_user_id=? AND owner_workspace_id=? AND module=\'rss\' AND kind=? AND ext_id=? AND op_id=? AND version_key=? AND reset_epoch IS ?)',values:[...owner,item.kind,item.ext_id,existing.op_id,existing.version_key,existing.reset_epoch]}:{clause:'NOT EXISTS(SELECT 1 FROM local_portal_records WHERE owner_user_id=? AND owner_workspace_id=? AND module=\'rss\' AND kind=? AND ext_id=?)',values:[...owner,item.kind,item.ext_id]}
  statements.push({statement:`INSERT INTO local_portal_record_write_guards(allowed) SELECT 0 WHERE NOT (${observed.clause})`,values:observed.values})
  if(!reason&&prior?.op_id!==item.op_id){
    const key=versionKey(item.client_updated_at),admitted=recordEpochGuard(db,'rss',item.epoch!)
    statements.push({statement:`INSERT INTO local_portal_records(owner_user_id,owner_workspace_id,module,kind,ext_id,source_id,item_json,version_key,op_id,reset_epoch) VALUES(?,?,'rss',?,?,?,?,?,?,?) ON CONFLICT(owner_user_id,owner_workspace_id,module,kind,ext_id) DO UPDATE SET item_json=excluded.item_json,version_key=excluded.version_key,op_id=excluded.op_id,reset_epoch=excluded.reset_epoch`,values:[...owner,item.kind,item.ext_id,existing?.source_id??`native:${item.ext_id}`,json,key,item.op_id,item.epoch]})
    statements.push({statement:`INSERT INTO local_portal_record_outbox(owner_user_id,owner_workspace_id,module,kind,op_id,ext_id,item_json,reset_epoch,last_error) VALUES(?,?,'rss',?,?,?,?,?,CASE WHEN ${admitted.clause} THEN '' ELSE 'reset_epoch_mismatch' END) ON CONFLICT(owner_user_id,owner_workspace_id,module,op_id) DO NOTHING`,values:[...owner,item.kind,item.op_id,item.ext_id,json,item.epoch,...admitted.values]})
  }
  statements.push({statement:`INSERT INTO local_rss_native_events(owner_user_id,owner_workspace_id,sequence,item_json,reason) VALUES(?,?,?,?,?) ON CONFLICT(owner_user_id,owner_workspace_id,sequence) DO NOTHING`,values:[...owner,change.sequence,json,reason]})
  statements.push({statement:`INSERT INTO local_portal_record_write_guards(allowed) SELECT 0 WHERE COALESCE((SELECT cursor FROM local_rss_native_cursor WHERE owner_user_id=? AND owner_workspace_id=?),0)<>?`,values:[...owner,cursor]})
  statements.push({statement:`INSERT INTO local_rss_native_cursor(owner_user_id,owner_workspace_id,cursor) VALUES(?,?,?) ON CONFLICT(owner_user_id,owner_workspace_id) DO UPDATE SET cursor=excluded.cursor`,values:[...owner,change.sequence]})
  await db.runInTransaction(statements)
}

export async function applyNativeRssPage(db:SyncDb,raw:unknown,cursor:number):Promise<number>{
  await init(db);const page=parsePage(raw,db,cursor)
  for(const change of page.changes){db.assertCurrent();await applyChange(db,change,cursor);cursor=change.sequence}
  if(page.changes.length&&typeof window!=='undefined'){window.dispatchEvent(new Event('openpocket:portal-rss-applied'));window.dispatchEvent(new Event('openpocket:portal-rss-outbox-changed'))}
  return cursor
}

export async function ingestNativeRssChanges(operation=captureNativeRssOperation()):Promise<number>{
  operation.assertCurrent();const {db}=operation
  let cursor=await nativeRssCursor(db),count=0
  for(let n=0;n<10;n++){
    operation.assertCurrent()
    const page=await http<NativePage>(`/api/rss/portable/changes?limit=50&cursor=${cursor}`,{syncModule:'rss',assertCurrent:operation.assertCurrent})
    operation.assertCurrent();cursor=await applyNativeRssPage(db,page,cursor);count+=page.changes.length
    if(page.changes.length<50)break
  }
  return count
}

export async function nativeRssBlockedCount(db:SyncDb):Promise<number>{
  await init(db)
  const row=await db.queryOne<{count:number}>(`SELECT COUNT(*) AS count FROM local_rss_native_events WHERE owner_user_id=? AND owner_workspace_id=? AND reason<>''`,[db.owner!.userId,db.owner!.workspaceId])
  return row?.count??0
}
