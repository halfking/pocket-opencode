/**
 * rss API — 订阅源 / item / 过滤 / 分享
 *
 * 全部走 http() 统一鉴权 + 错误归一化。
 */
import { http } from './http'

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

export const rssApi = {
  async listSources(): Promise<RSSSource[]> {
    const res = await http<{ sources: RSSSource[] }>('/api/rss/sources')
    return res.sources ?? []
  },

  async listSeeds(): Promise<RSSSeed[]> {
    const res = await http<{ seeds: RSSSeed[] }>('/api/rss/sources/seeds')
    return res.seeds ?? []
  },

  async discover(url: string): Promise<RSSCandidate[]> {
    const res = await http<{ candidates: RSSCandidate[] }>('/api/rss/sources/discover', {
      method: 'POST',
      body: JSON.stringify({ url }),
    })
    return res.candidates ?? []
  },

  async addSource(input: { url: string; title?: string; language?: string; enabled?: boolean; fetchInterval?: string }): Promise<RSSSource> {
    return http<RSSSource>('/api/rss/sources', {
      method: 'POST',
      body: JSON.stringify(input),
    })
  },

  async patchSource(id: string, patch: Partial<RSSSource> & { fetchInterval?: string }): Promise<RSSSource> {
    return http<RSSSource>(`/api/rss/sources/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      body: JSON.stringify(patch),
    })
  },

  async deleteSource(id: string): Promise<void> {
    await http(`/api/rss/sources/${encodeURIComponent(id)}`, { method: 'DELETE' })
  },

  async refreshSource(id: string): Promise<{ fetched: boolean; newItems: number; duplicates: number; notModified: boolean }> {
    return http(`/api/rss/sources/${encodeURIComponent(id)}/refresh`, { method: 'POST' })
  },

  async listItems(opts: { sourceId?: string; status?: string; q?: string; limit?: number; offset?: number } = {}): Promise<RSSItem[]> {
    const qs = new URLSearchParams()
    if (opts.sourceId) qs.set('sourceId', opts.sourceId)
    if (opts.status) qs.set('status', opts.status)
    if (opts.q) qs.set('q', opts.q)
    if (opts.limit) qs.set('limit', String(opts.limit))
    if (opts.offset) qs.set('offset', String(opts.offset))
    const path = qs.toString() ? `/api/rss/items?${qs.toString()}` : '/api/rss/items'
    const res = await http<{ items: RSSItem[]; count: number }>(path)
    return res.items ?? []
  },

  async getItem(id: string): Promise<RSSItem> {
    return http<RSSItem>(`/api/rss/items/${encodeURIComponent(id)}`)
  },

  async markRead(id: string): Promise<RSSItem> {
    return http<RSSItem>(`/api/rss/items/${encodeURIComponent(id)}/read`, { method: 'POST' })
  },

  async bulkRead(ids: string[]): Promise<{ updated: number }> {
    return http<{ updated: number }>('/api/rss/items/bulk-read', {
      method: 'POST',
      body: JSON.stringify({ ids }),
    })
  },

  async setStarred(id: string, starred: boolean): Promise<RSSItem> {
    return http<RSSItem>(`/api/rss/items/${encodeURIComponent(id)}/star`, {
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

  async share(id: string, dest: 'wechat' | 'weibo' | 'clipboard' | 'download', caption?: string): Promise<RSSShareResponse> {
    return http<RSSShareResponse>(`/api/rss/items/${encodeURIComponent(id)}/share`, {
      method: 'POST',
      body: JSON.stringify({ dest, caption }),
    })
  },
}