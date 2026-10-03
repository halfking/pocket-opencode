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
  async listSources(): Promise<RSSSource[]> {
    const res = await http<{ sources: RSSSource[] }>('/api/rss/sources')
    return res.sources ?? []
  },

  async listSeeds(): Promise<RSSSeed[]> {
    // 老端点同时回 seeds / feeds 两个键；这里两个都读，避免任一侧改名就静默空列表。
    const res = await http<{ seeds?: RSSSeed[]; feeds?: RSSSeed[] }>('/api/rss/sources/seeds')
    return res.seeds ?? res.feeds ?? []
  },

  /** 内置推荐源目录（it / finance / news 三类，可按分类过滤）。 */
  async listStarter(category?: string): Promise<{ feeds: RSSStarterFeed[]; categories: string[] }> {
    const qs = new URLSearchParams()
    if (category) qs.set('category', category)
    const suffix = qs.toString() ? `?${qs.toString()}` : ''
    return http(`/api/rss/sources/starter${suffix}`)
  },

  /**
   * 一键把推荐源加入订阅列表（幂等）：已订阅的会被跳过。
   * 返回 { created, skipped, total, sources }。
   */
  async importStarter(input: { categories?: string[]; maxPerCategory?: number; enabled?: boolean } = {}): Promise<{
    created: number
    skipped: number
    total: number
    sources: RSSSource[]
  }> {
    return http('/api/rss/sources/import-starter', {
      method: 'POST',
      body: JSON.stringify(input),
    })
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