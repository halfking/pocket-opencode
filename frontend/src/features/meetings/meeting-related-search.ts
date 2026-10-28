import { localDB } from '../../native/local-db'
import { likePattern } from '../../native/fts-query'
import { searchHybrid } from '../notes/notes-store'
import type { RecommendItem } from './meetings-store'
import { mergeRecommendations } from './meeting-related'
// ⚠ §926：第三方检索搬进可测核心（带**等待上界**）—— 原来这里是裸 `fetch`，
//   是整条录音链上**唯一没有超时**的对外请求（App 自己的 `http()` 有 30 s 上限）。
//   「主查 + 至多一次回落」的请求预算也钉在那个模块里。
import { searchRelatedWeb as searchWikipedia } from './wikipedia-search.ts'
import { readCachedModuleMode, recordSyncReportEntry } from '../../stores/syncPolicyStore.ts'

export async function searchRelatedNotes(query: string, limit = 3, workspaceId = 'default'): Promise<RecommendItem[]> {
  const q = query.trim()
  if (!q) return []
  try {
    const hits = await searchHybrid(q, limit, workspaceId, 'meetings')
    return hits.slice(0, limit).map((hit) => ({
      type: 'note' as const,
      id: hit.note.id,
      title: hit.note.title || '未命名笔记',
      snippet: (hit.note.content || '').slice(0, 80),
      score: hit.score,
    }))
  } catch {
    return []
  }
}

export async function searchRelatedMeetings(
  query: string,
  excludeId?: string,
  limit = 3,
): Promise<RecommendItem[]> {
  const q = query.trim()
  if (!q) return []
  // §887：原来是内联字面量，与其他两处的 likePattern 副本同源。
  // 这里额外限制 40 字符（原式就有 ⇒ 行为不变），与其他两处的区别现在变成
  // 显式参数而不是隐式差异（§886 之后的第三份副本已并入此处）。
  const like = likePattern(q, 40)
  try {
    const rows = await localDB.query<{ id: string; title: string | null; summary: string | null; topic: string | null }>(
      `SELECT id, title, summary, topic FROM local_meetings
       WHERE deleted_at IS NULL AND IFNULL(archived_at, 0) = 0
         AND id != ?
         AND (IFNULL(title,'') LIKE ? OR IFNULL(summary,'') LIKE ? OR IFNULL(topic,'') LIKE ?)
       ORDER BY started_at DESC LIMIT ?`,
      [excludeId || '', like, like, like, limit],
    )
    return rows.map((row, i) => ({
      type: 'knowledge' as const,
      id: row.id,
      title: row.title || row.topic || '未命名会议',
      snippet: (row.summary || row.topic || '').slice(0, 80),
      score: 1 / (i + 1),
    }))
  } catch {
    return []
  }
}

// ⚠ §926：维基那一路现在住在 `wikipedia-search.ts`（带等待上界 + 请求预算的钉子）。
//   请求前复查会议策略，主查询后切到 local 时也不能再发回落查询。
export async function searchRelatedWeb(query: string): Promise<RecommendItem[]> {
  return searchWikipedia(query, { canRequest: () => {
    if (readCachedModuleMode('meetings') === 'cloud') return true
    recordSyncReportEntry({ module: 'meetings', status: 'skipped' })
    return false
  } })
}

export async function searchRelatedContext(
  query: string,
  opts?: { excludeMeetingId?: string; workspaceId?: string },
): Promise<RecommendItem[]> {
  const [notes, knowledge, web] = await Promise.all([
    searchRelatedNotes(query, 3, opts?.workspaceId ?? 'default'),
    searchRelatedMeetings(query, opts?.excludeMeetingId),
    searchRelatedWeb(query),
  ])
  return mergeRecommendations(notes, [...knowledge, ...web], 8)
}
