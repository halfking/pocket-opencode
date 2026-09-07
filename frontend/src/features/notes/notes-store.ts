/**
 * notes-store — 本地笔记门面。CRUD 在 notes-persist，检索在 notes-search。
 */
import { localDB } from '../../native/local-db'
import { encryptString } from '../../native/crypto'
import { useCryptoConfig } from '../../stores/crypto-config'
import { getNote, listNotes as listNotesRaw } from './notes-persist'
import { ensureNotesSearchIndex } from './notes-fts-ready'
import type { LocalNote } from './notes-types'

export type { LocalNote, SearchResult, CreateNoteInput, NoteMediaInput } from './notes-types'
export { createNote, updateNote, deleteNote, getNote, listDraftNotes, loadFullContent } from './notes-persist'
export { searchFullText, searchSemantic, searchHybrid } from './notes-search'
export { ensureNotesSearchIndex }

export async function listNotes(
  ...args: Parameters<typeof listNotesRaw>
): ReturnType<typeof listNotesRaw> {
  await ensureNotesSearchIndex()
  return listNotesRaw(...args)
}

const noteServerHandlers = new Set<(note: LocalNote) => void>()

export function registerNoteServerHandler(cb: (note: LocalNote) => void): () => void {
  noteServerHandlers.add(cb)
  return () => { noteServerHandlers.delete(cb) }
}

export async function handleServerEvent(note: LocalNote): Promise<void> {
  if (!note || !note.id) return
  const workspaceId = note.workspaceId ?? 'default'
  const existing = await getNote(note.id, true, workspaceId)
  const merged: LocalNote = existing
    ? {
        ...existing,
        workspaceId,
        title: note.title ?? existing.title,
        content: note.content || existing.content,
        contentType: note.contentType || existing.contentType,
        domain: note.domain ?? existing.domain,
        category: note.category ?? existing.category,
        tags: note.tags ?? existing.tags,
        updatedAt: note.updatedAt || existing.updatedAt,
      }
    : { ...note, workspaceId }

  const shouldEncrypt = useCryptoConfig().shouldEncryptField()
  const storedContent = shouldEncrypt ? await encryptString(merged.content) : merged.content
  if (existing) {
    await localDB.run(
      `UPDATE local_notes
         SET title = ?, content = ?, encrypted_content = ?, content_type = ?, domain = ?,
             category = ?, tags = ?, updated_at = ?
       WHERE id = ? AND workspace_id = ?`,
      [
        merged.title, storedContent, shouldEncrypt ? 1 : 0, merged.contentType, merged.domain,
        merged.category, merged.tags ? JSON.stringify(merged.tags) : null,
        merged.updatedAt, merged.id, workspaceId,
      ],
    )
  } else {
    await localDB.run(
      `INSERT INTO local_notes
         (id, workspace_id, title, content, content_type, domain, category, tags,
          audio_path, audio_duration_ms, created_by_voice, encrypted_content, created_at, updated_at,
          status, storage_tier, search_text)
       VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
      [
        merged.id, workspaceId, merged.title, storedContent, merged.contentType,
        merged.domain, merged.category, merged.tags ? JSON.stringify(merged.tags) : null,
        merged.audioPath, merged.audioDurationMs, merged.createdByVoice ? 1 : 0,
        shouldEncrypt ? 1 : 0, merged.createdAt, merged.updatedAt,
        merged.status ?? 'saved', merged.storageTier ?? 'inline', merged.searchText ?? merged.content,
      ],
    )
  }

  noteServerHandlers.forEach((cb) => {
    try { cb(merged) }
    catch (e) { console.warn('[notes-store] server handler threw:', e) }
  })
}
