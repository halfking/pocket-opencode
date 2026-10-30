/**
 * rss API — 订阅源 / item / 过滤 / 分享
 *
 * 全部走 http() 统一鉴权 + 错误归一化。
 */
import { http, type HttpOptions } from './http'
import { captureRssPortalOperation } from '../features/rss/rss-sync-store'
import { overlayRssItemStates,saveRssItemStatuses } from '../features/rss/rss-item-state'
import { listRssItemsWithState } from '../features/rss/rss-item-list'

function nativeMutation(options:HttpOptions={}) {
  const operation=captureRssPortalOperation()
  const assertCurrent=()=>{operation.assertCurrent();options.assertCurrent?.();operation.assertCurrent()}
  assertCurrent()
  return {operation,options:{...options,assertCurrent}}
}
function nativeChanged(){if(typeof window!=='undefined')window.dispatchEvent(new Event('openpocket:rss-native-changed'))}

export interface RSSSource {
  id: string
  url: string
  title: string
  description?: string
  siteUrl?: string
  language?: string
  status: string
  enabled: boolean
  error?: string
  fetchIntervalSec: number
  lastFetchedAt?: string
  nextFetchAt?: string
  unreadCount: number
}

export interface RSSItem {
  id: string
  sourceId: string
  guid: string
  title: string
  author?: string
  url: string
  summary?: string
  content?: string
  language?: string
  categories: string[]
  publishedAt?: string
  fetchedAt: string
  relevance: number
  matchReasons: string[]
  status: 'unread' | 'read' | 'starred' | 'archived'
  portalManaged?: boolean
}

export interface RSSFilter {
  id: string
  name: string
  enabled: boolean
  includeKeywords: string[]
  excludeKeywords: string[]
  languages: string[]
  since?: string
  until?: string
  minRelevance: number
}

export interface RSSSeed {
  url: string
  title: string
  siteUrl: string
  language: string
  category: string
}

export interface RSSCandidate {
  url: string
  title?: string
  contentType?: string
}

export interface RSSShareResponse {
  deepLink?: string
  copyText?: string
  hint?: string
  downloadUrl?: string
}

/** 内置推荐源目录条目（后端 rss.StarterFeed）。 */
export interface RSSStarterFeed {
  url: string
  title: string
  siteUrl: string
  language: string
  category: string
  categoryLabel: string
  note?: string
  fetchInterval?: string
}

/** 每日摘要里的一条。 */
export interface RSSDigestItem {
  id: string
  title: string
  url: string
  sourceId: string
  sourceTitle: string
  category: string
  language: string
  summary?: string
  publishedAt?: string
}

export interface RSSDigestSection {
  category: string
  label: string
  items: RSSDigestItem[]
}

/** 每天一份的「全部信息摘要」。body 是可直接分享出去的纯文本。 */
export interface RSSDigest {
  id: string
  date: string
  headline: string
  body: string
  sections: RSSDigestSection[]
  itemCount: number
  sourceCount: number
  generatedAt: string
}

export interface RSSDigestListItem {
  date: string
  headline: string
  itemCount: number
  generatedAt: string
}

export const rssApi = {
  async listSources(options: HttpOptions = {}): Promise<RSSSource[]> {
    const res = await http<{ sources: RSSSource[] }>('/api/rss/sources', options)
    return res.sources ?? []
  },

  async listSeeds(): Promise<RSSSeed[]> {
    // 老端点同时回 seeds / feeds 两个键；这里两个都读，避免任一侧改名就静默空列表。
    const res = await http<{ seeds?: RSSSeed[]; feeds?: RSSSeed[] }>('/api/rss/sources/seeds')
    return res.seeds ?? res.feeds ?? []
  },

  /** 内置推荐源目录（it / ai / finance / product / deals / news 六类，可按分类过滤）。 */
  async listStarter(category?: string,options:HttpOptions={}): Promise<{ feeds: RSSStarterFeed[]; categories: string[] }> {
    const qs = new URLSearchParams()
    if (category) qs.set('category', category)
    const suffix = qs.toString() ? `?${qs.toString()}` : ''
    return http(`/api/rss/sources/starter${suffix}`,options)
  },

  /**
   * 一键把推荐源加入订阅列表（幂等）：已订阅的会被跳过。
   * 返回 { created, skipped, total, sources }。
   */
  async importStarter(input: { categories?: string[]; maxPerCategory?: number; enabled?: boolean } = {}, options: HttpOptions = {}): Promise<{
    created: number
    skipped: number
    total: number
    sources: RSSSource[]
  }> {
    const captured=nativeMutation(options)
    const result=await http<{created:number;skipped:number;total:number;sources:RSSSource[]}>('/api/rss/sources/import-starter', {
      ...captured.options,
      method: 'POST',
      body: JSON.stringify({...input,portalSync:true,portalEpoch:captured.operation.epoch}),
    })
    captured.options.assertCurrent();nativeChanged();return result
  },

  /** 取某天的全部信息摘要（缺省今天；当天还没有时后端会按需生成）。 */
  async getDigest(date?: string): Promise<RSSDigest> {
    const qs = date ? `?date=${encodeURIComponent(date)}` : ''
    const res = await http<{ digest: RSSDigest }>(`/api/rss/digest${qs}`)
    return res.digest
  },

  /** 强制重新生成并落库（"生成今日摘要"按钮）。 */
  async runDigest(date?: string): Promise<RSSDigest> {
    const qs = date ? `?date=${encodeURIComponent(date)}` : ''
    const res = await http<{ digest: RSSDigest }>(`/api/rss/digest/run${qs}`, { method: 'POST' })
    return res.digest
  },

  /** 历史摘要列表（新的在前）。 */
  async listDigests(limit = 14): Promise<RSSDigestListItem[]> {
    const res = await http<{ digests: RSSDigestListItem[] }>(`/api/rss/digests?limit=${limit}`)
    return res.digests ?? []
  },

  async discover(url: string,options:HttpOptions={}): Promise<RSSCandidate[]> {
    const res = await http<{ candidates: RSSCandidate[] }>('/api/rss/sources/discover', {
      ...options,
      method: 'POST',
      body: JSON.stringify({ url }),
    })
    return res.candidates ?? []
  },

  async addSource(input: { url: string; title?: string; language?: string; enabled?: boolean; fetchInterval?: string },options:HttpOptions={}): Promise<RSSSource> {
    const captured=nativeMutation(options)
    const result=await http<RSSSource>('/api/rss/sources', {
      ...captured.options,
      method: 'POST',
      body: JSON.stringify({...input,portalSync:true,portalEpoch:captured.operation.epoch}),
    })
    captured.options.assertCurrent();nativeChanged();return result
  },

  async patchSource(id: string, patch: Partial<RSSSource> & { fetchInterval?: string }, options: HttpOptions = {}): Promise<RSSSource> {
    const captured=nativeMutation(options)
    const result=await http<RSSSource>(`/api/rss/sources/${encodeURIComponent(id)}`, {
      ...captured.options,
      method: 'PATCH',
      body: JSON.stringify({...patch,portableMutation:true,portalEpoch:captured.operation.epoch}),
    })
    captured.options.assertCurrent();nativeChanged();return result
  },

  async deleteSource(id: string, options: HttpOptions = {}): Promise<void> {
    const captured=nativeMutation(options)
    await http(`/api/rss/sources/${encodeURIComponent(id)}`, { ...captured.options, method: 'DELETE',body:JSON.stringify({portableMutation:true,portalEpoch:captured.operation.epoch}) })
    captured.options.assertCurrent();nativeChanged()
  },

  async refreshSource(id: string, options: HttpOptions = {}): Promise<{ fetched: boolean; newItems: number; duplicates: number; notModified: boolean }> {
    return http(`/api/rss/sources/${encodeURIComponent(id)}/refresh`, { ...options, method: 'POST' })
  },

  async listItems(opts: { sourceId?: string; status?: string; q?: string; limit?: number; offset?: number } = {}, options: HttpOptions = {}): Promise<RSSItem[]> {
    const captured=nativeMutation(options)
    return listRssItemsWithState(opts,captured.options,captured.operation)
  },

  async getItem(id: string, options: HttpOptions = {}): Promise<RSSItem> {
    const captured=nativeMutation(options),item=await http<RSSItem>(`/api/rss/items/${encodeURIComponent(id)}`,captured.options)
    return (await overlayRssItemStates([item],captured.operation))[0]!
  },

  async markRead(input: string|RSSItem, options: HttpOptions = {}): Promise<RSSItem> {
    const captured=nativeMutation(options),item=typeof input==='string'?await rssApi.getItem(input,captured.options):{...input}
    captured.options.assertCurrent()
    if(item.portalManaged)return (await saveRssItemStatuses([item],'read',captured.operation))[0]!
    return http<RSSItem>(`/api/rss/items/${encodeURIComponent(item.id)}/read`, { ...captured.options, method: 'POST' })
  },

  async bulkRead(input: readonly (string|RSSItem)[],options:HttpOptions={}): Promise<{ updated: number }> {
    if(!input.length||input.length>100)throw new Error('1 to 100 RSS items required')
    const captured=nativeMutation(options),snapshots=input.map(item=>typeof item==='string'?item:{...item})
    const unique=[...new Map(snapshots.map(item=>[typeof item==='string'?item:item.id,item])).values()]
    const items=await Promise.all(unique.map(item=>typeof item==='string'?rssApi.getItem(item,captured.options):item))
    captured.options.assertCurrent()
    const managed=items.filter(item=>item.portalManaged),legacy=items.filter(item=>!item.portalManaged)
    if(managed.length)await saveRssItemStatuses(managed,'read',captured.operation)
    const result=legacy.length?await http<{updated:number}>('/api/rss/items/bulk-read',{...captured.options,method:'POST',body:JSON.stringify({ids:legacy.map(item=>item.id)})}):{updated:0}
    return {updated:managed.length+result.updated}
  },

  async setStarred(input: string|RSSItem, starred: boolean, options: HttpOptions = {}): Promise<RSSItem> {
    const captured=nativeMutation(options),item=typeof input==='string'?await rssApi.getItem(input,captured.options):{...input}
    captured.options.assertCurrent()
    if(item.portalManaged)return (await saveRssItemStatuses([item],starred?'starred':'read',captured.operation))[0]!
    return http<RSSItem>(`/api/rss/items/${encodeURIComponent(item.id)}/star`, {
      ...captured.options,
      method: 'POST',
      body: JSON.stringify({ starred }),
    })
  },

  async listFilters(): Promise<RSSFilter[]> {
    const res = await http<{ filters: RSSFilter[] }>('/api/rss/filters')
    return res.filters ?? []
  },

  async replaceFilters(filters: Omit<RSSFilter, 'id'>[]): Promise<RSSFilter[]> {
    const res = await http<{ filters: RSSFilter[] }>('/api/rss/filters', {
      method: 'PUT',
      body: JSON.stringify({ filters }),
    })
    return res.filters ?? []
  },

  shareCardUrl(id: string): string {
    // 直接返回路径，前端用 <img :src="..."> 即可（同步：computed 绑定需要纯 string）
    return `/api/rss/items/${encodeURIComponent(id)}/share-card?theme=light`
  },

  async share(id: string, dest: 'wechat' | 'weibo' | 'clipboard' | 'download', caption?: string, options: HttpOptions = {}): Promise<RSSShareResponse> {
    return http<RSSShareResponse>(`/api/rss/items/${encodeURIComponent(id)}/share`, {
      ...options,
      method: 'POST',
      body: JSON.stringify({ dest, caption }),
    })
  },
}
