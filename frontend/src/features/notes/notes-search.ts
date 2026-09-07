import { localDB } from '../../native/local-db'
import { vectorIndex, type VectorMatch } from '../../native/vector'
import { http } from '../../api/http'
import { assetStore } from '../../native/asset-store'
import { assetToNote, rowToNote } from './notes-row'
import type { LocalNote, NoteRow, SearchResult } from './notes-types'
import { buildFtsQuery, mergeSearchHits, needsCjkLike } from './note-fts-query'
import { ensureNotesSearchIndex } from './notes-fts-ready'

export function sanitizeFtsQuery(q: string): string {
  return buildFtsQuery(q)
}

function likePattern(q: string): string {
  return `%${q.replace(/[%_]/g, '')}%`
}

async function searchLike(query: string, limit: number, workspaceId: string): Promise<SearchResult[]> {
  const rows = await localDB.query<NoteRow>(
    `SELECT * FROM local_notes
     WHERE workspace_id = ? AND deleted_at IS NULL AND (status IS NULL OR status = 'saved')
       AND (title LIKE ? OR content LIKE ? OR IFNULL(search_text, '') LIKE ?)
     ORDER BY updated_at DESC LIMIT ?`,
    [workspaceId, likePattern(query), likePattern(query), likePattern(query), limit],
  )
  const notes = (await Promise.all(rows.map(rowToNote))).filter((n): n is LocalNote => n !== null)
  return notes.map((note, i) => ({ note, score: 1 / (i + 1), source: 'fts' as const }))
}

export async function searchFullText(query: string, limit = 20, workspaceId = 'default'): Promise<SearchResult[]> {
  await ensureNotesSearchIndex()
  const ftsQuery = buildFtsQuery(query)
  let localResults: SearchResult[] = []
  try {
    const rows = await localDB.query<NoteRow & { score: number }>(
      `SELECT n.*, -bm25(local_notes_fts) AS score
       FROM local_notes_fts
       JOIN local_notes n ON n.rowid = local_notes_fts.rowid
       WHERE local_notes_fts MATCH ? AND n.workspace_id = ? AND n.deleted_at IS NULL
         AND (n.status IS NULL OR n.status = 'saved')
       ORDER BY score DESC LIMIT ?`,
      [ftsQuery, workspaceId, limit],
    )
    const mapped: SearchResult[] = []
    for (const r of rows) {
      const note = await rowToNote(r)
      if (note) mapped.push({ note, score: r.score, source: 'fts' })
    }
    localResults = mapped
  } catch {
    localResults = await searchLike(query, limit, workspaceId)
  }
  // 原生 FTS5 可用；unicode61 把连续汉字当成一词。中文再并一层 LIKE，Web 无 FTS 时上面已走 LIKE。
  if (needsCjkLike(query) || localResults.length === 0) {
    const likeHits = await searchLike(query, limit, workspaceId)
    localResults = mergeSearchHits(localResults, likeHits, limit)
  }
  const assets = await assetStore.search({ workspaceId, kind: 'note', source: 'enex_import', fts: ftsQuery, limit })
  const assetResults = assets.map((asset, index) => ({ note: assetToNote(asset), score: -index - 1, source: 'fts' as const }))
  return [...localResults, ...assetResults].sort((a, b) => b.score - a.score).slice(0, limit)
}

async function embedViaPocketd(text: string): Promise<Float32Array | null> {
  try {
    const res = await http<{ embedding: number[]; model: string }>('/api/embed', {
      method: 'POST',
      body: JSON.stringify({ text }),
    })
    return Float32Array.from(res.embedding)
  } catch {
    return null
  }
}

export async function searchSemantic(queryText: string, topK = 10, workspaceId = 'default'): Promise<SearchResult[]> {
  const assets = await assetStore.search({ workspaceId, kind: 'note', source: 'enex_import', fts: sanitizeFtsQuery(queryText), limit: topK })
  const assetResults = assets.map((asset, index) => ({ note: assetToNote(asset), score: 1 / (index + 1), source: 'vector' as const }))
  if (assetResults.length >= topK) return assetResults.slice(0, topK)

  const qVec = await embedViaPocketd(queryText)
  if (!qVec) return assetResults
  const matches: VectorMatch[] = vectorIndex.search(qVec, topK)
  if (matches.length === 0) return assetResults

  const ids = matches.map((m) => m.noteId)
  const placeholders = ids.map(() => '?').join(',')
  const rows = await localDB.query<NoteRow>(
    `SELECT * FROM local_notes WHERE id IN (${placeholders}) AND workspace_id = ? AND deleted_at IS NULL
       AND (status IS NULL OR status = 'saved')`,
    [...ids, workspaceId],
  )
  const notesArray = await Promise.all(rows.map(rowToNote))
  const noteMap = new Map(notesArray.filter((n): n is LocalNote => n !== null).map((n) => [n.id, n]))
  return [
    ...matches
      .map((m): SearchResult | null => {
        const note = noteMap.get(m.noteId)
        return note ? { note, score: m.score, source: 'vector' } : null
      })
      .filter((r): r is SearchResult => r !== null),
    ...assetResults,
  ].sort((a, b) => b.score - a.score).slice(0, topK)
}

export async function searchHybrid(queryText: string, topK = 10, workspaceId = 'default'): Promise<SearchResult[]> {
  const [ftsResults, vecResults] = await Promise.all([
    searchFullText(queryText, topK * 2, workspaceId),
    searchSemantic(queryText, topK * 2, workspaceId),
  ])
  const rrf = new Map<string, { score: number; note: LocalNote }>()
  const addScore = (results: SearchResult[]) => {
    results.forEach((r, rank) => {
      const existing = rrf.get(r.note.id)
      const contribution = 1 / (60 + rank + 1)
      if (existing) existing.score += contribution
      else rrf.set(r.note.id, { score: contribution, note: r.note })
    })
  }
  addScore(ftsResults)
  addScore(vecResults)
  return Array.from(rrf.values())
    .map((v) => ({ note: v.note, score: v.score, source: 'hybrid' as const }))
    .sort((a, b) => b.score - a.score)
    .slice(0, topK)
}
