/**
 * notes-store.ts — 🦞 本地笔记数据访问（范本 store）
 *
 * 展示龙虾架构下 feature store 的标准模式：
 *   - 写入只进本地加密库（SQLCipher）
 *   - 全文搜索用 FTS5（本地）
 *   - 语义搜索用 vectorIndex（本地 JS 余弦）
 *   - 嵌入计算时才发文本片段给 pocketd（云端只见片段）
 *   - 混合检索用 RRF 融合 FTS + 向量结果
 *
 * 其他 feature store（emails/passwords/meetings）照此模式。
 */
import { localDB } from '../../native/local-db'
import { vectorIndex, type VectorMatch } from '../../native/vector'
import { http } from '../../api/http'

export interface LocalNote {
  id: string
  workspaceId: string | null
  title: string | null
  content: string
  contentType: string
  domain: string | null
  category: string | null
  tags: string[] | null
  audioPath: string | null
  audioDurationMs: number
  createdByVoice: boolean
  createdAt: number
  updatedAt: number
}

export interface SearchResult {
  note: LocalNote
  score: number
  source: 'fts' | 'vector' | 'hybrid'
}

const EMBED_DIM = 1536

/** 新增笔记。content 写本地库；文本片段发 pocketd 算嵌入，向量存本地。 */
export async function createNote(input: {
  title?: string
  content: string
  domain?: string
  tags?: string[]
  audioPath?: string
  audioDurationMs?: number
  contentType?: string
}): Promise<LocalNote> {
  const now = Date.now()
  const note: LocalNote = {
    id: `note-${now}-${Math.random().toString(36).slice(2, 8)}`,
    workspaceId: null,
    title: input.title ?? null,
    content: input.content,
    contentType: input.contentType ?? 'voice',
    domain: input.domain ?? null,
    category: null,
    tags: input.tags ?? null,
    audioPath: input.audioPath ?? null,
    audioDurationMs: input.audioDurationMs ?? 0,
    createdByVoice: true,
    createdAt: now,
    updatedAt: now,
  }

  await localDB.run(
    `INSERT INTO local_notes
       (id, workspace_id, title, content, content_type, domain, category, tags,
        audio_path, audio_duration_ms, created_by_voice, created_at, updated_at)
     VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
    [
      note.id, note.workspaceId, note.title, note.content, note.contentType,
      note.domain, note.category, note.tags ? JSON.stringify(note.tags) : null,
      note.audioPath, note.audioDurationMs, note.createdByVoice ? 1 : 0,
      note.createdAt, note.updatedAt,
    ],
  )

  // 异步算嵌入并存向量（不阻塞笔记写入返回）
  // 只发 content 给 pocketd，云端不见 audio_path / tags 等元数据
  embedAndStore(note.id, note.content).catch((e) => {
    console.warn('[lobster] 嵌入失败，笔记已存但暂无向量:', e)
  })

  // 异步触发服务端 kxmemory 分类（不影响主流程，失败静默）
  // 走单条端点：服务端 handleNotes 已绑定 classifyNoteAsync（server_assistant.go:113）
  http(`/api/notes/${note.id}/classify`, { method: 'POST' }).catch(() => {
    // kxmemory 未配置或离线时静默失败，本地笔记仍可用
  })

  return note
}

/** 更新笔记。内容变更会重新触发嵌入计算。 */
export async function updateNote(id: string, patch: Partial<Pick<LocalNote, 'title' | 'content' | 'domain' | 'tags'>>): Promise<void> {
  const sets: string[] = []
  const vals: unknown[] = []
  if (patch.title !== undefined) { sets.push('title = ?'); vals.push(patch.title) }
  if (patch.content !== undefined) { sets.push('content = ?'); vals.push(patch.content) }
  if (patch.domain !== undefined) { sets.push('domain = ?'); vals.push(patch.domain) }
  if (patch.tags !== undefined) { sets.push('tags = ?'); vals.push(patch.tags ? JSON.stringify(patch.tags) : null) }
  if (sets.length === 0) return

  sets.push('updated_at = ?')
  vals.push(Date.now())
  vals.push(id)

  await localDB.run(`UPDATE local_notes SET ${sets.join(', ')} WHERE id = ?`, vals)

  if (patch.content !== undefined) {
    embedAndStore(id, patch.content).catch(() => {})
  }
}

/** 软删除。 */
export async function deleteNote(id: string): Promise<void> {
  await localDB.run('UPDATE local_notes SET deleted_at = ? WHERE id = ?', [Date.now(), id])
  await vectorIndex.remove(id)
}

/** 按 ID 获取（含已软删除的需传 includeDeleted）。 */
export async function getNote(id: string, includeDeleted = false): Promise<LocalNote | null> {
  const sql = includeDeleted
    ? 'SELECT * FROM local_notes WHERE id = ?'
    : 'SELECT * FROM local_notes WHERE id = ? AND deleted_at IS NULL'
  return rowToNote(await localDB.queryOne<NoteRow>(sql, [id]))
}

/** 列表（最新优先）。 */
export async function listNotes(opts: { domain?: string; limit?: number } = {}): Promise<LocalNote[]> {
  let sql = 'SELECT * FROM local_notes WHERE deleted_at IS NULL'
  const vals: unknown[] = []
  if (opts.domain) { sql += ' AND domain = ?'; vals.push(opts.domain) }
  sql += ' ORDER BY updated_at DESC LIMIT ?'
  vals.push(opts.limit ?? 100)
  const rows = await localDB.query<NoteRow>(sql, vals)
  return rows.map(rowToNote).filter((n): n is LocalNote => n !== null)
}

/** FTS5 全文搜索。 */
export async function searchFullText(query: string, limit = 20): Promise<SearchResult[]> {
  // BM25 排序，FTS5 内置 bm25() 越小越相关，取负转成越大越相关
  const rows = await localDB.query<NoteRow & { score: number }>(
    `SELECT n.*, -bm25(local_notes_fts) AS score
     FROM local_notes_fts
     JOIN local_notes n ON n.rowid = local_notes_fts.rowid
     WHERE local_notes_fts MATCH ? AND n.deleted_at IS NULL
     ORDER BY score DESC LIMIT ?`,
    [sanitizeFtsQuery(query), limit],
  )
  return rows.map((r) => ({ note: rowToNote(r)!, score: r.score, source: 'fts' as const }))
}

/** 语义搜索（本地向量余弦）。queryText 先发 pocketd 算嵌入。 */
export async function searchSemantic(queryText: string, topK = 10): Promise<SearchResult[]> {
  const qVec = await embedViaPocketd(queryText)
  if (!qVec) return []
  const matches: VectorMatch[] = vectorIndex.search(qVec, topK)
  if (matches.length === 0) return []

  // 批量拉笔记详情
  const ids = matches.map((m) => m.noteId)
  const placeholders = ids.map(() => '?').join(',')
  const rows = await localDB.query<NoteRow>(
    `SELECT * FROM local_notes WHERE id IN (${placeholders}) AND deleted_at IS NULL`,
    ids,
  )
  const noteMap = new Map(rows.map((r) => [r.id, rowToNote(r)]))

  return matches
    .map((m): SearchResult | null => {
      const note = noteMap.get(m.noteId)
      return note ? { note, score: m.score, source: 'vector' } : null
    })
    .filter((r): r is SearchResult => r !== null)
}

/**
 * 混合搜索（RRF 融合 FTS + 向量）。
 * Reciprocal Rank Fusion：各路结果按排名取 1/(60+rank) 求和，无界融合。
 * 这是 2025-2026 移动端 RAG 的推荐做法。
 */
export async function searchHybrid(queryText: string, topK = 10): Promise<SearchResult[]> {
  const [ftsResults, vecResults] = await Promise.all([
    searchFullText(queryText, topK * 2),
    searchSemantic(queryText, topK * 2),
  ])

  const rrf = new Map<string, { score: number; note: LocalNote }>()
  const RRF_K = 60 // 标准常量
  const addScore = (results: SearchResult[], source: 'fts' | 'vector') => {
    results.forEach((r, rank) => {
      const existing = rrf.get(r.note.id)
      const contribution = 1 / (RRF_K + rank + 1)
      if (existing) {
        existing.score += contribution
      } else {
        rrf.set(r.note.id, { score: contribution, note: r.note })
      }
    })
  }
  addScore(ftsResults, 'fts')
  addScore(vecResults, 'vector')

  return Array.from(rrf.entries())
    .map(([_, v]) => ({ note: v.note, score: v.score, source: 'hybrid' as const }))
    .sort((a, b) => b.score - a.score)
    .slice(0, topK)
}

// ---- WS 事件接入 ----

/**
 * 注册"服务器推送 note 变化"的回调。
 * NoteListView 在 onMounted 注册，在 onUnmounted 反注册，
 * 收到事件后更新自己的 ref<LocalNote[]> 列表。
 */
const noteServerHandlers = new Set<(note: LocalNote) => void>()

/** 注册一个 note 服务器事件处理器，返回反注册函数（便于 onUnmounted 调用）。 */
export function registerNoteServerHandler(cb: (note: LocalNote) => void): () => void {
  noteServerHandlers.add(cb)
  return () => { noteServerHandlers.delete(cb) }
}

/**
 * 处理 note.created 服务器推送：
 *   - 若本地已有该笔记 → 只更新服务器后处理字段（category/domain/tags/updated_at）
 *     本地用户私有字段（content/audio/createdByVoice 等）不会被覆盖为 null
 *   - 若本地没有（罕见：另一设备创建后同步过来）→ 写入本地以便后续搜索
 *   - 然后通知所有已注册的 handler 更新视图层的内存列表
 *
 * 幂等：重复调用同一 note 的事件，结果一致（最后一份 server 字段生效）。
 */
export async function handleServerEvent(note: LocalNote): Promise<void> {
  if (!note || !note.id) return

  const existing = await getNote(note.id, true)
  const merged: LocalNote = existing
    ? {
        ...existing,
        title: note.title ?? existing.title,
        content: note.content || existing.content,
        contentType: note.contentType || existing.contentType,
        domain: note.domain ?? existing.domain,
        category: note.category ?? existing.category,
        tags: note.tags ?? existing.tags,
        updatedAt: note.updatedAt || existing.updatedAt,
      }
    : { ...note }

  if (existing) {
    await localDB.run(
      `UPDATE local_notes
         SET title = ?, content = ?, content_type = ?, domain = ?,
             category = ?, tags = ?, updated_at = ?
       WHERE id = ?`,
      [
        merged.title, merged.content, merged.contentType, merged.domain,
        merged.category, merged.tags ? JSON.stringify(merged.tags) : null,
        merged.updatedAt, merged.id,
      ],
    )
  } else {
    await localDB.run(
      `INSERT INTO local_notes
         (id, workspace_id, title, content, content_type, domain, category, tags,
          audio_path, audio_duration_ms, created_by_voice, created_at, updated_at)
       VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
      [
        merged.id, merged.workspaceId, merged.title, merged.content, merged.contentType,
        merged.domain, merged.category, merged.tags ? JSON.stringify(merged.tags) : null,
        merged.audioPath, merged.audioDurationMs, merged.createdByVoice ? 1 : 0,
        merged.createdAt, merged.updatedAt,
      ],
    )
  }

  // 通知所有视图层处理器更新内存列表
  noteServerHandlers.forEach((cb) => {
    try { cb(merged) }
    catch (e) { console.warn('[notes-store] server handler threw:', e) }
  })
}

// ---- 内部辅助 ----

interface NoteRow {
  id: string; workspace_id: string | null; title: string | null; content: string;
  content_type: string; domain: string | null; category: string | null; tags: string | null;
  audio_path: string | null; audio_duration_ms: number; created_by_voice: number;
  created_at: number; updated_at: number; deleted_at: number | null;
}

function rowToNote(r: NoteRow | null): LocalNote | null {
  if (!r) return null
  return {
    id: r.id, workspaceId: r.workspace_id, title: r.title, content: r.content,
    contentType: r.content_type, domain: r.domain, category: r.category,
    tags: r.tags ? JSON.parse(r.tags) : null, audioPath: r.audio_path,
    audioDurationMs: r.audio_duration_ms, createdByVoice: r.created_by_voice === 1,
    createdAt: r.created_at, updatedAt: r.updated_at,
  }
}

/** FTS5 查询转义：对每个词加引号避免被当操作符。 */
function sanitizeFtsQuery(q: string): string {
  return q.split(/\s+/).filter(Boolean).map((w) => `"${w.replace(/"/g, '""')}"`).join(' ')
}

/**
 * 发文本片段给 pocketd /api/embed，返回嵌入向量。
 * 云端只见这一段文本，不见笔记元数据。pocketd 无状态转发给嵌入 API。
 */
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

/** 算嵌入并存入本地向量索引。 */
async function embedAndStore(noteId: string, content: string): Promise<void> {
  const res = await http<{ embedding: number[]; model: string }>('/api/embed', {
    method: 'POST',
    body: JSON.stringify({ text: content }),
  })
  await vectorIndex.add(noteId, Float32Array.from(res.embedding), res.model)
}
