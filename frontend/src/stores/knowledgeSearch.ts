/**
 * knowledgeSearch.ts — Pinia store for unified knowledge base search.
 *
 * Manages unified search across multiple sources:
 * - Local notes (SQLite FTS)
 * - Memora memories (vector search)
 * - Emails
 * - Meetings
 *
 * Supports filtering by notebooks, tags, date ranges, and domains.
 *
 * Reference: docs/2026-11-14-knowledge-base-integration-plan.md §5.2 & §7
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

export type SearchSourceType = 'all' | 'notes' | 'memora' | 'emails' | 'meetings'
export type SearchResultType = 'note' | 'memory' | 'email' | 'meeting'
export type DomainType = 'work' | 'study' | 'life' | 'idea'

export interface SearchFilters {
  source?: SearchSourceType
  notebook?: string
  tags?: string[]
  domain?: DomainType
  dateFrom?: string
  dateTo?: string
}

export interface SearchResult {
  id: string
  type: SearchResultType
  source: 'local' | 'memora' | 'email' | 'meeting'
  title: string
  snippet: string
  highlights: string[]
  notebook?: {
    id: string
    name: string
  }
  tags: string[]
  domain?: DomainType
  createdAt: string
  updatedAt?: string
  score: number
}

export interface SearchResponse {
  results: SearchResult[]
  total: number
  limit: number
  offset: number
  searchTime: number
}

export const useKnowledgeSearchStore = defineStore('knowledgeSearch', () => {
  // State
  const query = ref('')
  const results = ref<SearchResult[]>([])
  const filters = ref<SearchFilters>({})
  const loading = ref(false)
  const error = ref<string | null>(null)
  const searchTime = ref(0)
  const total = ref(0)
  const limit = ref(50)
  const offset = ref(0)

  // Getters
  const filteredResults = computed(() => {
    let filtered = results.value

    // Additional client-side filtering if needed
    if (filters.value.tags && filters.value.tags.length > 0) {
      filtered = filtered.filter((result) =>
        filters.value.tags!.some((tag) => result.tags.includes(tag))
      )
    }

    return filtered
  })

  const resultsBySource = computed(() => {
    const grouped: Record<string, SearchResult[]> = {
      local: [],
      memora: [],
      email: [],
      meeting: [],
    }

    results.value.forEach((result) => {
      if (grouped[result.source]) {
        grouped[result.source].push(result)
      }
    })

    return grouped
  })

  const hasActiveFilters = computed(() => {
    return !!(
      filters.value.source && filters.value.source !== 'all' ||
      filters.value.notebook ||
      filters.value.tags && filters.value.tags.length > 0 ||
      filters.value.domain ||
      filters.value.dateFrom ||
      filters.value.dateTo
    )
  })

  // Actions
  async function search(searchQuery: string, searchFilters?: SearchFilters, pagination?: { limit?: number; offset?: number }): Promise<SearchResponse> {
    if (!searchQuery.trim()) {
      results.value = []
      total.value = 0
      searchTime.value = 0
      return {
        results: [],
        total: 0,
        limit: limit.value,
        offset: offset.value,
        searchTime: 0,
      }
    }

    query.value = searchQuery
    if (searchFilters) {
      filters.value = { ...filters.value, ...searchFilters }
    }
    if (pagination?.limit !== undefined) {
      limit.value = pagination.limit
    }
    if (pagination?.offset !== undefined) {
      offset.value = pagination.offset
    }

    loading.value = true
    error.value = null

    try {
      const params = new URLSearchParams({
        q: searchQuery,
        limit: limit.value.toString(),
        offset: offset.value.toString(),
      })

      if (filters.value.source) {
        params.append('source', filters.value.source)
      }
      if (filters.value.notebook) {
        params.append('notebook', filters.value.notebook)
      }
      if (filters.value.tags && filters.value.tags.length > 0) {
        params.append('tags', filters.value.tags.join(','))
      }
      if (filters.value.domain) {
        params.append('domain', filters.value.domain)
      }
      if (filters.value.dateFrom) {
        params.append('dateFrom', filters.value.dateFrom)
      }
      if (filters.value.dateTo) {
        params.append('dateTo', filters.value.dateTo)
      }

      const response = await fetch(`/api/knowledge/search?${params.toString()}`, {
        method: 'GET',
        headers: {
          'Content-Type': 'application/json',
        },
      })

      if (!response.ok) {
        throw new Error(`Search failed: ${response.statusText}`)
      }

      const data: SearchResponse = await response.json()
      results.value = data.results
      total.value = data.total
      searchTime.value = data.searchTime

      return data
    } catch (err) {
      error.value = err instanceof Error ? err.message : 'Unknown error occurred'
      console.error('Error performing search:', err)
      results.value = []
      total.value = 0
      searchTime.value = 0
      throw err
    } finally {
      loading.value = false
    }
  }

  function applyFilters(newFilters: SearchFilters) {
    filters.value = { ...filters.value, ...newFilters }
    offset.value = 0 // Reset pagination when filters change

    // Re-run search if there's an active query
    if (query.value.trim()) {
      return search(query.value, filters.value)
    }
  }

  function clearSearch() {
    query.value = ''
    results.value = []
    filters.value = {}
    error.value = null
    searchTime.value = 0
    total.value = 0
    offset.value = 0
  }

  function clearFilters() {
    filters.value = {}
    offset.value = 0

    // Re-run search if there's an active query
    if (query.value.trim()) {
      return search(query.value)
    }
  }

  async function loadMore(): Promise<SearchResponse | null> {
    if (!query.value.trim() || loading.value) {
      return null
    }

    offset.value += limit.value
    const response = await search(query.value, filters.value, { limit: limit.value, offset: offset.value })
    
    // Append results instead of replacing
    results.value = [...results.value, ...response.results]
    
    return response
  }

  return {
    // State
    query,
    results,
    filters,
    loading,
    error,
    searchTime,
    total,
    limit,
    offset,

    // Getters
    filteredResults,
    resultsBySource,
    hasActiveFilters,

    // Actions
    search,
    applyFilters,
    clearSearch,
    clearFilters,
    loadMore,
  }
})
