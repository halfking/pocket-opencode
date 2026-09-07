import { localDB } from '../../native/local-db'
import { vectorIndex } from '../../native/vector'
import { http } from '../../api/http'
import { encryptString } from '../../native/crypto'
import { useCryptoConfig } from '../../stores/crypto-config'
import { assetStore } from '../../native/asset-store'
import { decideNoteStorage } from './note-storage-policy'
import { deleteNoteFiles, persistNotePayload, readNoteBody } from './note-files'
import { assetToNote, rowToNote, mergeImportedNotes } from './notes-row'
import type { CreateNoteInput, LocalNote, NoteMediaInput, NoteRow } from './notes-types'

function newNoteId(): string {
  return `note-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
}

function collectMedia(input: CreateNoteInput): NoteMediaInput[] {
  const media = [...(input.media ?? [])]
  if (input.audioBlob) {
    media.unshift({
      kind: 'audio',
      blob: input.audioBlob,
      mime: input.audioBlob.type || 'audio/webm',
      durationMs: input.audioDurationMs ?? 0,
    })
  }
  return media
}

export async function createNote(input: CreateNoteInput): Promise<LocalNote> {
  const now = Date.now()
  const workspaceId = input.workspaceId ?? 'default'
  const media = collectMedia(input)
  const hasMedia = media.length > 0 || Boolean(input.audioPath)
  const decided = decideNoteStorage({
    title: input.title,
    body: input.content,
    tags: input.tags,
    hasMedia,
  })
  const id = newNoteId()
  let bodyPath: string | null = null
  let mediaJson: string | null = null
  let audioPath = input.audioPath ?? null

  if (decided.persistBodyFile || media.length > 0) {
    const written = await persistNotePayload({
      noteId: id,
      createdAt: now,
      body: input.content,
      persistBodyFile: decided.persistBodyFile,
      media,
    })
    bodyPath = written.bodyPath
    mediaJson = written.files.length ? JSON.stringify(written.files) : null
    if (written.audioPath) audioPath = written.audioPath
  }

  const note: LocalNote = {
    id,
    workspaceId,
    title: input.title ?? null,
    content: decided.displayContent,
    contentType: input.contentType ?? (hasMedia ? 'voice' : 'text'),
    domain: input.domain ?? null,
    category: null,
    tags: input.tags ?? null,
    audioPath,
    audioDurationMs: input.audioDurationMs ?? 0,
    createdByVoice: input.createdByVoice ?? hasMedia,
    createdAt: now,
    updatedAt: now,
    storage: 'local_notes',
    status: input.status ?? 'saved',
    storageTier: decided.tier,
    summary: decided.summary,
    searchText: decided.searchText,
    bodyPath,
    mediaJson,
  }

  const shouldEncrypt = useCryptoConfig().shouldEncryptField()
  const storedContent = shouldEncrypt ? await encryptString(note.content) : note.content
  await localDB.run(
    `INSERT INTO local_notes
       (id, workspace_id, title, content, content_type, domain, category, tags,
        audio_path, audio_duration_ms, created_by_voice, encrypted_content, created_at, updated_at,
        status, storage_tier, summary, search_text, body_path, media_json)
     VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
    [
      note.id, note.workspaceId, note.title, storedContent, note.contentType,
      note.domain, note.category, note.tags ? JSON.stringify(note.tags) : null,
      note.audioPath, note.audioDurationMs, note.createdByVoice ? 1 : 0,
      shouldEncrypt ? 1 : 0, note.createdAt, note.updatedAt,
      note.status, note.storageTier, note.summary, note.searchText, note.bodyPath, note.mediaJson,
    ],
  )

  embedAndStore(note.id, decided.searchText || note.content).catch((e) => {
    console.warn('[lobster] 嵌入失败，笔记已存但暂无向量:', e)
  })
  http(`/api/notes/${note.id}/classify`, { method: 'POST' }).catch(() => {})
  return note
}

export async function updateNote(
  id: string,
  patch: Partial<Pick<LocalNote, 'title' | 'content' | 'domain' | 'tags' | 'status'>> & {
    media?: NoteMediaInput[]
    audioBlob?: Blob
    audioDurationMs?: number
  },
  workspaceId = 'default',
): Promise<void> {
  const existing = await getNote(id, false, workspaceId)
  if (!existing) throw new Error('笔记不存在或不属于当前 workspace')
  if (existing.storage === 'asset') {
    const asset = await assetStore.getForWorkspace(id, workspaceId)
    if (!asset) throw new Error('笔记不存在或不属于当前 workspace')
    let meta: Record<string, unknown> = {}
    try { meta = JSON.parse(asset.metaJson || '{}') } catch { /* empty */ }
    await assetStore.upsert({
      id,
      workspaceId,
      kind: asset.kind,
      title: patch.title !== undefined ? patch.title || '' : asset.title,
      bodyText: patch.content !== undefined ? patch.content : asset.bodyText,
      metaJson: JSON.stringify({ ...meta, ...(patch.tags !== undefined ? { tags: patch.tags } : {}) }),
      source: asset.source,
      syncMode: asset.syncMode,
    })
    return
  }

  const nextContent = patch.content !== undefined ? patch.content : await loadFullContent(existing)
  const nextTitle = patch.title !== undefined ? patch.title : existing.title
  const nextTags = patch.tags !== undefined ? patch.tags : existing.tags
  const media = patch.media ?? (patch.audioBlob ? [{
    kind: 'audio' as const,
    blob: patch.audioBlob,
    mime: patch.audioBlob.type || 'audio/webm',
    durationMs: patch.audioDurationMs ?? existing.audioDurationMs,
  }] : [])
  const hasMedia = media.length > 0 || Boolean(existing.audioPath)
  const decided = decideNoteStorage({
    title: nextTitle,
    body: nextContent,
    tags: nextTags,
    hasMedia,
  })

  let bodyPath = existing.bodyPath ?? null
  let mediaJson = existing.mediaJson ?? null
  let audioPath = existing.audioPath
  if (patch.content !== undefined || media.length > 0) {
    if (existing.bodyPath || existing.mediaJson) {
      await deleteNoteFiles(id, existing.createdAt)
    }
    if (decided.persistBodyFile || media.length > 0) {
      const written = await persistNotePayload({
        noteId: id,
        createdAt: existing.createdAt,
        body: nextContent,
        persistBodyFile: decided.persistBodyFile,
        media,
      })
      bodyPath = written.bodyPath
      mediaJson = written.files.length ? JSON.stringify(written.files) : null
      if (written.audioPath) audioPath = written.audioPath
    } else {
      bodyPath = null
      if (!existing.audioPath) mediaJson = null
    }
  }

  const shouldEncrypt = useCryptoConfig().shouldEncryptField()
  const storedContent = shouldEncrypt ? await encryptString(decided.displayContent) : decided.displayContent
  await localDB.run(
    `UPDATE local_notes SET
       title = ?, content = ?, encrypted_content = ?, domain = ?, tags = ?,
       status = ?, storage_tier = ?, summary = ?, search_text = ?, body_path = ?,
       media_json = ?, audio_path = ?, audio_duration_ms = ?, updated_at = ?
     WHERE id = ? AND workspace_id = ?`,
    [
      nextTitle, storedContent, shouldEncrypt ? 1 : 0,
      patch.domain !== undefined ? patch.domain : existing.domain,
      nextTags ? JSON.stringify(nextTags) : null,
      patch.status ?? existing.status ?? 'saved',
      decided.tier, decided.summary, decided.searchText, bodyPath, mediaJson,
      audioPath, patch.audioDurationMs ?? existing.audioDurationMs, Date.now(),
      id, workspaceId,
    ],
  )
  if (patch.content !== undefined) {
    embedAndStore(id, decided.searchText).catch(() => {})
  }
}

export async function deleteNote(id: string, workspaceId = 'default'): Promise<void> {
  const existing = await getNote(id, true, workspaceId)
  if (existing?.storage === 'asset') {
    await assetStore.softDeleteForWorkspace(id, workspaceId)
    return
  }
  if (existing && (existing.bodyPath || existing.mediaJson)) {
    await deleteNoteFiles(id, existing.createdAt)
  }
  await localDB.run('UPDATE local_notes SET deleted_at = ? WHERE id = ? AND workspace_id = ?', [Date.now(), id, workspaceId])
  await vectorIndex.remove(id)
}

export async function getNote(id: string, includeDeleted = false, workspaceId = 'default'): Promise<LocalNote | null> {
  const sql = includeDeleted
    ? 'SELECT * FROM local_notes WHERE id = ? AND workspace_id = ?'
    : 'SELECT * FROM local_notes WHERE id = ? AND workspace_id = ? AND deleted_at IS NULL'
  const local = await rowToNote(await localDB.queryOne<NoteRow>(sql, [id, workspaceId]))
  if (local) return local
  if (includeDeleted) return null
  const asset = await assetStore.getForWorkspace(id, workspaceId)
  return asset?.kind === 'note' && asset.source === 'enex_import' ? assetToNote(asset) : null
}

export async function loadFullContent(note: LocalNote): Promise<string> {
  if (note.storageTier !== 'file' || !note.bodyPath) return note.content
  const body = await readNoteBody(note.bodyPath)
  return body ?? note.content
}

export async function listNotes(opts: {
  domain?: string
  limit?: number
  offset?: number
  workspaceId?: string
  includeDrafts?: boolean
} = {}): Promise<LocalNote[]> {
  const limit = opts.limit ?? 30
  const offset = opts.offset ?? 0
  let sql = 'SELECT * FROM local_notes WHERE workspace_id = ? AND deleted_at IS NULL'
  const vals: unknown[] = [opts.workspaceId ?? 'default']
  if (!opts.includeDrafts) sql += " AND (status IS NULL OR status = 'saved')"
  if (opts.domain) { sql += ' AND domain = ?'; vals.push(opts.domain) }
  sql += ' ORDER BY updated_at DESC LIMIT ? OFFSET ?'
  vals.push(limit, offset)
  const rows = await localDB.query<NoteRow>(sql, vals)
  const localNotes = (await Promise.all(rows.map(rowToNote))).filter((n): n is LocalNote => n !== null)
  if (offset > 0) return localNotes
  return mergeImportedNotes(localNotes, opts.workspaceId ?? 'default', limit)
}

export async function listDraftNotes(workspaceId = 'default'): Promise<LocalNote[]> {
  const rows = await localDB.query<NoteRow>(
    `SELECT * FROM local_notes
     WHERE workspace_id = ? AND deleted_at IS NULL AND status = 'draft'
     ORDER BY updated_at DESC LIMIT 5`,
    [workspaceId],
  )
  return (await Promise.all(rows.map(rowToNote))).filter((n): n is LocalNote => n !== null)
}

async function embedAndStore(noteId: string, content: string): Promise<void> {
  const res = await http<{ embedding: number[]; model: string }>('/api/embed', {
    method: 'POST',
    body: JSON.stringify({ text: content }),
  })
  await vectorIndex.add(noteId, Float32Array.from(res.embedding), res.model)
}
