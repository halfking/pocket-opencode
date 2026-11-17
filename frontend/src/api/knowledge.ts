/**
 * Knowledge Base API — unified interface for notebooks, note links, and
 * knowledge search (§7 API 设计, 2026-11-14-knowledge-base-integration-plan.md).
 *
 * This module provides:
 *   1. Notebooks API - organize notes into hierarchical notebooks
 *   2. Note Links API - manual and AI-powered note relationships
 *   3. Search API - unified search across notes, memora, emails, meetings
 */
import { http } from './http'
import type { NoteDomain } from './notes'

// ============================================================================
// Notebooks API Types
// ============================================================================

export interface Notebook {
  id: string
  userId: string
  workspaceId?: string
  name: string
  parentId?: string
  icon?: string
  color?: string
  sortOrder: number
  createdAt: string
  updatedAt: string
  noteCount: number
}

export interface NotebookInput {
  name: string
  parentId?: string
  icon?: string
  color?: string
}

// ============================================================================
// Note Links API Types
// ============================================================================

export type LinkType = 'related' | 'references' | 'blocks' | 'is-a'
export type LinkCreatedBy = 'user' | 'ai'

export interface NoteLinkNote {
  id: string
  title?: string
  snippet?: string
  tags?: string[]
}

export interface NoteLink {
  id: string
  note: NoteLinkNote
  linkType: LinkType
  createdBy: LinkCreatedBy
  createdAt: string
}

export interface SmartLink extends NoteLink {
  confidence: number
  reason?: string
}

export interface BacklinkNote {
  id: string
  title?: string
  snippet?: string
  createdAt: string
}

export interface RelatedNotesResponse {
  manualLinks: NoteLink[]
  smartLinks: SmartLink[]
  backlinks: BacklinkNote[]
}

export interface LinkInput {
  targetNoteId: string
  linkType: LinkType
}

// ============================================================================
// Search API Types
// ============================================================================

export type SearchSource = 'all' | 'notes' | 'memora' | 'emails' | 'meetings'
export type SearchResultType = 'note' | 'memory' | 'email' | 'meeting'
export type SearchResultSource = 'local' | 'memora'

export interface SearchNotebook {
  id: string
  name: string
}

export interface SearchResult {
  id: string
  type: SearchResultType
  source: SearchResultSource
  title?: string
  snippet?: string
  highlights?: string[]
  notebook?: SearchNotebook
  tags?: string[]
  domain?: NoteDomain
  createdAt: string
  updatedAt?: string
  score: number
}

export interface SearchParams {
  q: string
  source?: SearchSource
  notebook?: string
  tags?: string
  domain?: NoteDomain
  dateFrom?: string
  dateTo?: string
  limit?: number
  offset?: number
}

export interface SearchResponse {
  results: SearchResult[]
  total: number
  limit: number
  offset: number
  searchTime: number
}

// ============================================================================
// API Client
// ============================================================================

export const knowledgeApi = {
  // --------------------------------------------------------------------------
  // Notebooks API
  // --------------------------------------------------------------------------

  /**
   * 获取所有笔记本（树形结构）
   */
  fetchNotebooks(): Promise<Notebook[]> {
    return http<Notebook[]>('/api/notebooks')
  },

  /**
   * 创建笔记本
   */
  createNotebook(data: NotebookInput): Promise<Notebook> {
    return http<Notebook>('/api/notebooks', {
      method: 'POST',
      body: JSON.stringify(data),
    })
  },

  /**
   * 获取单个笔记本详情
   */
  getNotebook(id: string): Promise<Notebook> {
    return http<Notebook>(`/api/notebooks/${encodeURIComponent(id)}`)
  },

  /**
   * 更新笔记本
   */
  updateNotebook(id: string, data: Partial<NotebookInput>): Promise<Notebook> {
    return http<Notebook>(`/api/notebooks/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(data),
    })
  },

  /**
   * 删除笔记本
   */
  deleteNotebook(id: string): Promise<void> {
    return http<void>(`/api/notebooks/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    })
  },

  /**
   * 获取笔记本下的所有笔记
   */
  getNotebookNotes(id: string): Promise<{ notes: any[] }> {
    return http<{ notes: any[] }>(`/api/notebooks/${encodeURIComponent(id)}/notes`)
  },

  // --------------------------------------------------------------------------
  // Note Links API
  // --------------------------------------------------------------------------

  /**
   * 创建笔记关联（手动建立的双向链接）
   */
  createNoteLink(noteId: string, data: LinkInput): Promise<NoteLink> {
    return http<NoteLink>(`/api/notes/${encodeURIComponent(noteId)}/links`, {
      method: 'POST',
      body: JSON.stringify(data),
    })
  },

  /**
   * 删除笔记关联
   */
  deleteNoteLink(id: string): Promise<void> {
    return http<void>(`/api/note-links/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    })
  },

  /**
   * 获取笔记的所有关联（手动链接 + AI 推荐 + 反向链接）
   */
  getRelatedNotes(noteId: string): Promise<RelatedNotesResponse> {
    return http<RelatedNotesResponse>(`/api/notes/${encodeURIComponent(noteId)}/related`)
  },

  // --------------------------------------------------------------------------
  // Search API
  // --------------------------------------------------------------------------

  /**
   * 统一知识搜索（混合搜索：本地 + memora + emails + meetings）
   *
   * 支持过滤条件：
   * - source: 搜索来源（all/notes/memora/emails/meetings）
   * - notebook: 笔记本 ID 过滤
   * - tags: 标签过滤（逗号分隔）
   * - domain: 领域过滤（work/study/life/idea）
   * - dateFrom/dateTo: 日期范围过滤
   * - limit/offset: 分页
   */
  searchKnowledge(params: SearchParams): Promise<SearchResponse> {
    const qs = new URLSearchParams()
    
    qs.set('q', params.q)
    
    if (params.source) qs.set('source', params.source)
    if (params.notebook) qs.set('notebook', params.notebook)
    if (params.tags) qs.set('tags', params.tags)
    if (params.domain) qs.set('domain', params.domain)
    if (params.dateFrom) qs.set('dateFrom', params.dateFrom)
    if (params.dateTo) qs.set('dateTo', params.dateTo)
    if (params.limit !== undefined) qs.set('limit', String(params.limit))
    if (params.offset !== undefined) qs.set('offset', String(params.offset))

    return http<SearchResponse>(`/api/knowledge/search?${qs.toString()}`)
  },

  // --------------------------------------------------------------------------
  // Knowledge Graph API
  // --------------------------------------------------------------------------

  /**
   * 获取完整知识图谱
   *
   * 支持过滤条件：
   * - notebook: 笔记本 ID 过滤
   * - domain: 领域过滤（work/study/life/idea）
   * - limit: 最大节点数（默认 200，最大 500）
   */
  fetchGraph(params?: { notebook?: string; domain?: NoteDomain; limit?: number }): Promise<GraphData> {
    const qs = new URLSearchParams()
    
    if (params?.notebook) qs.set('notebook', params.notebook)
    if (params?.domain) qs.set('domain', params.domain)
    if (params?.limit !== undefined) qs.set('limit', String(params.limit))

    const query = qs.toString()
    return http<GraphData>(`/api/knowledge/graph${query ? '?' + query : ''}`)
  },

  /**
   * 获取笔记的局部图谱（以指定笔记为中心的 1-hop 邻居）
   */
  fetchNoteGraph(noteId: string): Promise<GraphData> {
    return http<GraphData>(`/api/knowledge/graph/note/${encodeURIComponent(noteId)}`)
  },
}

// ============================================================================
// Graph API Types
// ============================================================================

export type NodeType = 'note' | 'notebook' | 'tag'
export type EdgeType = 'link' | 'reference' | 'tag' | 'notebook'

export interface NodeData {
  domain?: NoteDomain
  tags?: string[]
  createdAt?: string
  updatedAt?: string
  snippet?: string
}

export interface GraphNode {
  id: string
  label: string
  type: NodeType
  size: number
  data?: NodeData
}

export interface GraphEdge {
  source: string
  target: string
  type: EdgeType
  weight: number
}

export interface GraphData {
  nodes: GraphNode[]
  edges: GraphEdge[]
}
