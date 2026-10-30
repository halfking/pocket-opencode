import type {RSSItem} from '../../api/rss'
import {http,type HttpOptions} from '../../api/http'
import type {RssPortalOperation} from './rss-sync-store'
import {overlayRssItemStates,rssItemStatusOverrides} from './rss-item-state'
import {versionKey} from '../../native/portal-sync/contract.ts'

export interface RssItemListOptions {sourceId?:string;status?:string;q?:string;limit?:number;offset?:number}

/** Filter and paginate after the owner-scoped local status overlay. A status
 * edited offline must neither remain in unread nor disappear from starred.
 * Supplemental IDs are resolved by pocketd before display, so a local status
 * projection alone can never resurrect a missing native article. */
export async function listRssItemsWithState(opts:RssItemListOptions,options:HttpOptions,operation:RssPortalOperation):Promise<RSSItem[]> {
  const limit=Math.max(1,Math.min(opts.limit??50,200)),offset=Math.max(0,opts.offset??0)
  const overrides=opts.status?await rssItemStatusOverrides(operation):new Map<string,RSSItem['status']>()
  const read=async(status:string|undefined,take:number,skip:number,ids?:string[])=>{
    operation.assertCurrent();const qs=new URLSearchParams()
    if(opts.sourceId)qs.set('sourceId',opts.sourceId)
    if(opts.q)qs.set('q',opts.q)
    if(status)qs.set('status',status)
    qs.set('limit',String(take));qs.set('offset',String(skip))
    for(const id of ids??[])qs.append('id',id)
    const response=await http<{items:RSSItem[]}>(`/api/rss/items?${qs}`,options)
    operation.assertCurrent();return response.items??[]
  }
  if(!overrides.size)return overlayRssItemStates(await read(opts.status,limit,offset),operation)
  // At most one extra native row is needed per local override that removes a
  // row from the server's status filter. This keeps pagination exact without
  // scanning the full feed or uploading local-only statuses to the server.
  const needed=offset+limit+[...overrides.values()].filter(status=>status!==opts.status).length
  const rows:RSSItem[]=[]
  while(rows.length<needed){
    const take=Math.min(200,needed-rows.length),page=await read(opts.status,take,rows.length)
    rows.push(...page);if(page.length<take)break
  }
  const present=new Set(rows.map(item=>item.id))
  const extra=[...overrides].filter(([id,status])=>status===opts.status&&!present.has(id)).map(([id])=>id)
  for(let start=0;start<extra.length;start+=100)rows.push(...await read(undefined,100,0,extra.slice(start,start+100)))
  const merged=await overlayRssItemStates([...new Map(rows.map(item=>[item.id,item])).values()],operation)
  const date=(value:string|undefined)=>value?versionKey(value):''
  return merged.filter(item=>item.status===opts.status).sort((a,b)=>{
    const publishedA=date(a.publishedAt),publishedB=date(b.publishedAt)
    if(publishedA!==publishedB)return publishedB>publishedA?1:-1
    const fetchedA=date(a.fetchedAt),fetchedB=date(b.fetchedAt)
    return fetchedA!==fetchedB?(fetchedB>fetchedA?1:-1):a.id<b.id?-1:a.id>b.id?1:0
  }).slice(offset,offset+limit)
}
