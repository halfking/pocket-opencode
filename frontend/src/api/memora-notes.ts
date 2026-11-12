/**
 * Memora notes API — memora 记忆库「笔记本」面（/api/v2/notebook/*）经
 * pocketd 的薄转发（2026-11-13，A5 openpocket↔memora notebook 集成）。
 *
 * 与 api/notes.ts（openpocket 自有笔记的 CRUD）是**两条面**：本模块只做
 * 两件事——
 *   1. sync：把 openpocket 笔记批量推送到 memora notebook（幂等：同内容
 *      重推 skipped、内容变化就地更新，见 server_memora.go 的登记差异）；
 *   2. search/detail：云端记忆库检索 + 单条详情（列表行不含正文，
 *      展开正文走 detail）。
 *
 * 未配置 memora（POCKET_MEMORA_BASE_URL 为空）时后端一律 503 +
 * 可读 error 字段，调用方据此提示而不是装作「搜到了 0 条」。
 */
import { http } from './http'

/** memora notebook 笔记的列表行（蛇形字段直传 memora NoteListItem）。 */
export interface MemoraNoteItem {
  id: string
  kb_id?: string
  title: string
  lifecycle?: string
  current_version?: number
  tags?: string[]
  created_at?: string
  updated_at?: string
  /** 后端固定标注 "memora"，前端据此区分来源。 */
  source?: 'memora'
}

/** memora notebook 笔记详情（GET /api/memora/notes/{id}，含正文）。 */
export interface MemoraNoteDetail extends MemoraNoteItem {
  revision?: number
  html?: string
  plain_text?: string
  change_reason?: string
  etag?: string
  removed?: boolean
}

export type MemoraSyncNoteStatus = 'created' | 'updated' | 'skipped' | 'failed'

/** 单条笔记的同步结论（逐条返回，失败不整批报错）。 */
export interface MemoraSyncNoteResult {
  noteId: string
  title?: string
  status: MemoraSyncNoteStatus
  memoraId?: string
  error?: string
}

export interface MemoraSyncResult {
  total: number
  created: number
  updated: number
  skipped: number
  failed: number
  results: MemoraSyncNoteResult[]
}

/** GET /api/memora/status 的形状（已配置检查用）。 */
export interface MemoraStatus {
  configured: boolean
  reachable?: boolean
  error?: string
  stats?: Record<string, unknown>
}

export interface MemoraSearchParams {
  q?: string
  tag?: string
  lifecycle?: string
  kb_id?: string
  page?: number
  /** 服务端上限 200，超出被裁剪。 */
  limit?: number
}

export interface MemoraSyncPayload {
  /** 精确推送若干条（与 all 二选一）。 */
  noteIds?: string[]
  /** 推送当前 workspace 的全部手记（domain 可再筛）。 */
  all?: boolean
  domain?: string
  /** all=true 时的条数上限（服务端上限 100）。 */
  limit?: number
}

export const memoraNotesApi = {
  /** 连通性/已配置探针（设置页与同步入口的已配置检查）。 */
  status(): Promise<MemoraStatus> {
    return http('/api/memora/status')
  },

  /** 云端记忆库检索（转发 memora GET /api/v2/notebook/notes）。 */
  search(params: MemoraSearchParams): Promise<{ items: MemoraNoteItem[]; total: number }> {
    const qs = new URLSearchParams()
    if (params.q) qs.set('q', params.q)
    if (params.tag) qs.set('tag', params.tag)
    if (params.lifecycle) qs.set('lifecycle', params.lifecycle)
    if (params.kb_id) qs.set('kb_id', params.kb_id)
    if (params.page && params.page > 0) qs.set('page', String(params.page))
    if (params.limit && params.limit > 0) qs.set('limit', String(params.limit))
    const enc = qs.toString()
    return http(`/api/memora/notes/search${enc ? `?${enc}` : ''}`)
  },

  /** 单条详情（正文 plain_text 供展开展示）。 */
  detail(id: string): Promise<MemoraNoteDetail> {
    return http(`/api/memora/notes/${encodeURIComponent(id)}`)
  },

  /**
   * 批量推送到 memora notebook。幂等：服务端按
   * sync:openpocket:note:<id> 定位旧推送 + 内容哈希比对，同内容 skipped、
   * 变化 updated、新笔记 created；单条失败只记在 results 里。
   */
  sync(payload: MemoraSyncPayload): Promise<MemoraSyncResult> {
    return http('/api/memora/notes/sync', {
      method: 'POST',
      body: JSON.stringify(payload),
    })
  },
}
