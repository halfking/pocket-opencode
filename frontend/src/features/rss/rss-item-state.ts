import type { RSSItem } from '../../api/rss'
import { http } from '../../api/http'
import { recordEpochGuard } from '../../native/portal-sync/record-epoch.ts'
import { validateRecordItem } from '../../native/portal-sync/record-contract.ts'
import { rssSyncAdapter } from './rss-sync-adapter'
import { captureRssPortalOperation, rssPortalStore, type RssPortalOperation } from './rss-sync-store'
import { captureNativeRssOperation } from './rss-native-ingest'

const receipts=`CREATE TABLE IF NOT EXISTS local_rss_state_receipts(owner_user_id TEXT NOT NULL,owner_workspace_id TEXT NOT NULL,op_id TEXT NOT NULL,PRIMARY KEY(owner_user_id,owner_workspace_id,op_id))`

/** Save the user's status and its immutable outbox in the same SQLite commit.
 * Feed bodies, titles and author timestamps never enter a status projection. */
export async function saveRssItemStatuses(items: readonly RSSItem[],status:RSSItem['status'],operation:RssPortalOperation=captureRssPortalOperation()):Promise<RSSItem[]> {
  const snapshots=items.map(item=>({...item}))
  operation.assertCurrent()
  if(!snapshots.length||snapshots.length>100||snapshots.some(item=>item.portalManaged!==true)||new Set(snapshots.map(item=>item.id)).size!==snapshots.length)throw new Error('New status operations require explicitly shared RSS items')
  const store=rssPortalStore(operation.db),entries=[]
  for(const item of snapshots){
    const extId=`pocketd-item:${item.id}`,existing=await store.byExternalId(extId,'item-state')
    operation.assertCurrent()
    entries.push({snapshot:{id:existing?.source_id??`state:${extId}`,extId,kind:'item-state',title:'',payload:{item_ext_id:extId,status},modifiedAt:operation.modifiedAt,epoch:operation.epoch},created:!existing})
  }
  await store.commitMany([],entries)
  operation.assertCurrent()
  if(typeof window!=='undefined'){window.dispatchEvent(new Event('openpocket:portal-rss-applied'));window.dispatchEvent(new Event('openpocket:rss-native-changed'))}
  return snapshots.map(item=>({...item,status}))
}

export async function overlayRssItemStates<T extends RSSItem>(items:readonly T[],operation=captureRssPortalOperation()):Promise<T[]> {
  operation.assertCurrent()
  const store=rssPortalStore(operation.db)
  const result=[]
  for(const item of items){
    if(item.portalManaged!==true){result.push(item);continue}
    const extId=`pocketd-item:${item.id}`,row=await store.byExternalId(extId,'item-state'),state=row?await store.decode(row.item_json):null
    operation.assertCurrent()
    const status=state&&!state.deleted&&state.payload.item_ext_id===extId?state.payload.status:null
    result.push(typeof status==='string'&&['unread','read','starred','archived'].includes(status)?{...item,status:status as RSSItem['status']}:item)
  }
  return result
}

export async function rssItemStatusOverrides(operation:RssPortalOperation):Promise<Map<string,RSSItem['status']>> {
  operation.assertCurrent();const store=rssPortalStore(operation.db);await store.init()
  const rows=await operation.db.query<{ext_id:string;item_json:string}>(`SELECT ext_id,item_json FROM local_portal_records WHERE owner_user_id=? AND owner_workspace_id=? AND module='rss' AND kind='item-state' AND ext_id LIKE 'pocketd-item:%'`,store.scope)
  const result=new Map<string,RSSItem['status']>()
  for(const row of rows){const item=await store.decode(row.item_json);if(!item.deleted&&item.payload.item_ext_id===row.ext_id&&typeof item.payload.status==='string'&&['unread','read','starred','archived'].includes(item.payload.status))result.set(row.ext_id.slice('pocketd-item:'.length),item.payload.status as RSSItem['status'])}
  operation.assertCurrent();return result
}

/** Reconcile the winning projection only. Receipts survive process restarts;
 * an interrupted request is retried with exactly the same user time/op/epoch. */
export async function reconcileRssItemStates(operation=captureNativeRssOperation()):Promise<number> {
  operation.assertCurrent();const {db}=operation,store=rssPortalStore(db)
  await store.init();await db.run(receipts)
  const epoch=await store.currentEpoch();if(epoch===null)return 0
  await store.requireEpoch(epoch)
  const rows=await db.query<{item_json:string}>(`SELECT r.item_json FROM local_portal_records r WHERE r.owner_user_id=? AND r.owner_workspace_id=? AND r.module='rss' AND r.kind='item-state' AND r.ext_id LIKE 'pocketd-item:%' AND r.reset_epoch=? AND NOT EXISTS(SELECT 1 FROM local_rss_state_receipts a WHERE a.owner_user_id=r.owner_user_id AND a.owner_workspace_id=r.owner_workspace_id AND a.op_id=r.op_id) ORDER BY r.rowid`,[...store.scope,epoch])
  const pending=[]
  for(const row of rows){
    const item=await store.decode(row.item_json)
    validateRecordItem(rssSyncAdapter,item)
    // Independent manual/portal copies are read models, never native targets.
    if(!item.ext_id.startsWith('pocketd-item:')||(!item.deleted&&item.payload.item_ext_id!==item.ext_id))continue
    pending.push({ext_id:item.ext_id,title:item.title,status:item.deleted?'':item.payload.status,client_updated_at:item.client_updated_at,op_id:item.op_id,epoch:item.epoch,deleted:item.deleted})
  }
  let count=0
  for(let offset=0;offset<pending.length;offset+=100)count+=await sendBatch(pending.slice(offset,offset+100))
  return count

  async function sendBatch(pending:{ext_id:string;title:string;status:unknown;client_updated_at:string;op_id:string;epoch:number|null;deleted:boolean}[]):Promise<number>{
  if(!pending.length)return 0
  if(epoch===null)throw new Error('RSS state reset epoch changed')
  operation.assertCurrent();await store.requireEpoch(epoch)
  const response=await http<{user_id:string;workspace_id:string;acks:{ext_id:string;op_id:string;epoch:number;outcome:string}[]}>('/api/rss/portable/item-states',{method:'POST',body:JSON.stringify({items:pending}),assertCurrent:operation.assertCurrent})
  operation.assertCurrent();await store.requireEpoch(epoch)
  if(response.user_id!==db.owner?.userId||response.workspace_id!==db.owner?.workspaceId||!Array.isArray(response.acks)||response.acks.length!==pending.length)throw new Error('Invalid RSS status acknowledgement scope')
  const seen=new Set<string>(),statements=[]
  for(const ack of response.acks){
    const sent=pending.find(item=>item.op_id===ack.op_id)
    if(!sent||seen.has(ack.op_id)||ack.ext_id!==sent.ext_id||ack.epoch!==sent.epoch||!['applied','duplicate','kept_existing','unavailable'].includes(ack.outcome))throw new Error('Invalid RSS status acknowledgement')
    seen.add(ack.op_id)
    // Missing/deleted/unmanaged targets remain portable-only. Retry later in
    // case that exact already-managed native article has not arrived yet.
    if(ack.outcome==='unavailable')continue
    const guard=recordEpochGuard(db,'rss',epoch)
    statements.push({statement:`INSERT INTO local_rss_state_receipts(owner_user_id,owner_workspace_id,op_id) SELECT ?,?,? WHERE ${guard.clause} ON CONFLICT DO NOTHING`,values:[...store.scope,ack.op_id,...guard.values]})
  }
  if(statements.length)await db.runInTransaction(statements)
  return statements.length
  }
}
