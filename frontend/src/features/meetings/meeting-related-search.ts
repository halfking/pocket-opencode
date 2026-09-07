import { localDB } from '../../native/local-db'
import { searchHybrid } from '../notes/notes-store'
import type { RecommendItem } from './meetings-store'
import {
  mergeRecommendations, parseWikipediaOpenSearch, wikipediaSearchUrl,
} from './meeting-related'

export async function searchRelatedNotes(query: string, limit = 3): Promise<RecommendItem[]> {
  const q = query.trim()
  if (!q) return []
  try {
    const hits = await searchHybrid(q, limit)
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
  const like = `%${q.replace(/[%_]/g, '').slice(0, 40)}%`
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

async function fetchWikipedia(query: string): Promise<RecommendItem[]> {
  const url = wikipediaSearchUrl(query)
  if (!url) return []
  try {
    const res = await fetch(url)
    if (!res.ok) return []
    return parseWikipediaOpenSearch(await res.json())
  } catch {
    return []
  }
}

export async function searchRelatedWeb(query: string): Promise<RecommendItem[]> {
  const primary = await fetchWikipedia(query)
  if (primary.length) return primary
  const fallback = query.split(/\s+/).filter(Boolean).pop() || ''
  if (!fallback || fallback === query) return []
  return fetchWikipedia(fallback)
}

export async function searchRelatedContext(
  query: string,
  opts?: { excludeMeetingId?: string },
): Promise<RecommendItem[]> {
  const [notes, knowledge, web] = await Promise.all([
    searchRelatedNotes(query),
    searchRelatedMeetings(query, opts?.excludeMeetingId),
    searchRelatedWeb(query),
  ])
  return mergeRecommendations(notes, [...knowledge, ...web], 8)
}
