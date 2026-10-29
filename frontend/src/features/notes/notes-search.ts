import { localDB } from '../../native/local-db'
import { portalNoteReadScope } from '../../native/portal-sync/storage'
import { vectorIndex, type VectorMatch } from '../../native/vector'
import { http } from '../../api/http'
import { assetStore } from '../../native/asset-store'
import { assetToNote, rowToNote } from './notes-row'
import type { LocalNote, NoteRow, SearchResult } from './notes-types'
import { buildFtsQuery, mergeSearchHits, needsCjkLike } from './note-fts-query'
import { ensureNotesSearchIndex } from './notes-fts-ready'
import { likePattern } from '../../native/fts-query.ts'
import type { SyncModule } from '../../api/syncPolicyContract.ts'
import { readCachedModuleMode } from '../../stores/syncPolicyStore.ts'

export function sanitizeFtsQuery(q: string): string {
  return buildFtsQuery(q)
}

// §887：原来这里有一个私有副本，与 `native/fts-query.ts` 的
// `likePattern` 逐字相同 ⇒ 删掉改 import。行为等价，无可观察变化。

async function searchLike(query: string, limit: number, workspaceId: string): Promise<SearchResult[]> {
  const scope = await portalNoteReadScope('note','local_notes')
  const rows = await scope.db.query<NoteRow>(
    `SELECT * FROM local_notes
     WHERE workspace_id = ? AND deleted_at IS NULL AND (status IS NULL OR status = 'saved')
       AND ${scope.clause} AND (title LIKE ? OR content LIKE ? OR IFNULL(search_text, '') LIKE ?)
     ORDER BY updated_at DESC LIMIT ?`,
    [workspaceId, ...scope.values, likePattern(query), likePattern(query), likePattern(query), limit],
  )
  const notes = (await Promise.all(rows.map(rowToNote))).filter((n): n is LocalNote => n !== null)
  return notes.map((note, i) => ({ note, score: 1 / (i + 1), source: 'fts' as const }))
}

export async function searchFullText(query: string, limit = 20, workspaceId = 'default'): Promise<SearchResult[]> {
  await ensureNotesSearchIndex()
  const scope = await portalNoteReadScope('note','n')
  const ftsQuery = buildFtsQuery(query)
  let localResults: SearchResult[] = []
  try {
    const rows = await scope.db.query<NoteRow & { score: number }>(
      `SELECT n.*, -bm25(local_notes_fts) AS score
       FROM local_notes_fts
       JOIN local_notes n ON n.rowid = local_notes_fts.rowid
       WHERE local_notes_fts MATCH ? AND n.workspace_id = ? AND n.deleted_at IS NULL
         AND (n.status IS NULL OR n.status = 'saved') AND ${scope.clause}
       ORDER BY score DESC LIMIT ?`,
      [ftsQuery, workspaceId, ...scope.values, limit],
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

async function embedViaPocketd(text: string, syncModule: SyncModule, assertCurrent?: () => void): Promise<Float32Array | null> {
  try {
    const res = await http<{ embedding: number[]; model: string }>('/api/embed', {
      syncModule,
      assertCurrent,
      method: 'POST',
      body: JSON.stringify({ text }),
    })
    return Float32Array.from(res.embedding)
  } catch {
    return null
  }
}

export async function searchSemantic(queryText: string, topK = 10, workspaceId = 'default', syncModule: SyncModule = 'notes'): Promise<SearchResult[]> {
  if (readCachedModuleMode(syncModule) === 'local') return searchFullText(queryText, topK, workspaceId)
  const scope = await portalNoteReadScope('note', 'local_notes')
  const assets = await assetStore.search({ workspaceId, kind: 'note', source: 'enex_import', fts: sanitizeFtsQuery(queryText), limit: topK })
  const assetResults = assets.map((asset, index) => ({ note: assetToNote(asset), score: 1 / (index + 1), source: 'vector' as const }))
  if (assetResults.length >= topK) return assetResults.slice(0, topK)

  const eligibleRows = await scope.db.query<{ id: string }>(`SELECT id FROM local_notes WHERE workspace_id=? AND deleted_at IS NULL AND (status IS NULL OR status='saved') AND ${scope.clause}`, [workspaceId, ...scope.values])
  const qVec = await embedViaPocketd(queryText, syncModule, scope.db.assertCurrent)
  if (!qVec) return readCachedModuleMode(syncModule) === 'local'
    ? searchFullText(queryText, topK, workspaceId)
    : assetResults
  // ⚠⚠ §895/D65：必须用 **searchAsync**，不能用同步的 `search()`。
  //   `search()` 是全量暴力点积，单次耗时线性于索引条数
  //   （实测 n=20000 → 24.89ms，到 50000 上限外推约 62ms），
  //   而本函数在 **UI 线程**上被调用 ——
  //   最硬的一条调用点是 `NoteDetailView.loadRelated`：**每次打开笔记详情**都会跑一次。
  //   `searchAsync` 结果与之逐字相同，只是每 512 条让出一次事件循环。
  const matches: VectorMatch[] = await vectorIndex.searchAsync(qVec, topK, undefined, new Set(eligibleRows.map(row => row.id)))
  if (matches.length === 0) return assetResults

  const ids = matches.map((m) => m.noteId)
  const placeholders = ids.map(() => '?').join(',')
  const rows = await scope.db.query<NoteRow>(
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

export async function searchHybrid(queryText: string, topK = 10, workspaceId = 'default', syncModule: SyncModule = 'notes'): Promise<SearchResult[]> {
  if (readCachedModuleMode(syncModule) === 'local') return searchFullText(queryText, topK, workspaceId)
  const [ftsResults, vecResults] = await Promise.all([
    searchFullText(queryText, topK * 2, workspaceId),
    searchSemantic(queryText, topK * 2, workspaceId, syncModule),
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
