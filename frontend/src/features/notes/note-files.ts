import { Capacitor } from '@capacitor/core'
import { Filesystem, Directory } from '@capacitor/filesystem'
import { localDB } from '../../native/local-db'
import { blobToBase64 } from '../../utils/base64'
import { noteDir, noteFileRelPath, type NoteFileKind } from './note-paths'
import type { NoteFileRecord, NoteMediaInput } from './notes-types'

export interface PersistNotePayloadInput {
  noteId: string
  createdAt: number
  body: string
  persistBodyFile: boolean
  media: NoteMediaInput[]
}

export interface PersistNotePayloadResult {
  bodyPath: string | null
  files: NoteFileRecord[]
  audioPath: string | null
}

function extFromMime(mime: string, fallback: string): string {
  const map: Record<string, string> = {
    'audio/webm': 'webm',
    'audio/mp4': 'm4a',
    'audio/mpeg': 'mp3',
    'image/jpeg': 'jpg',
    'image/png': 'png',
    'image/webp': 'webp',
    'video/mp4': 'mp4',
    'video/webm': 'webm',
  }
  return map[mime.split(';', 1)[0]] || fallback
}

async function writeRelPath(relPath: string, dataBase64: string): Promise<string> {
  const parent = relPath.slice(0, relPath.lastIndexOf('/'))
  if (Capacitor.isNativePlatform()) {
    await Filesystem.mkdir({ path: parent, directory: Directory.Data, recursive: true }).catch(() => {})
    await Filesystem.writeFile({ path: relPath, data: dataBase64, directory: Directory.Data })
    const uri = await Filesystem.getUri({ path: relPath, directory: Directory.Data })
    return uri.uri
  }
  return `sqlite:${relPath}`
}

async function insertFileRow(row: NoteFileRecord, dataBase64: string, nativeUri: string | null): Promise<void> {
  await localDB.run(
    `INSERT OR REPLACE INTO local_note_files
       (id, note_id, kind, rel_path, mime, size_bytes, duration_ms, data_base64, created_at)
     VALUES (?,?,?,?,?,?,?,?,?)`,
    [
      row.id, row.noteId, row.kind, nativeUri || row.relPath, row.mime,
      row.sizeBytes, row.durationMs, nativeUri ? '' : dataBase64, Date.now(),
    ],
  )
}

export async function persistNotePayload(input: PersistNotePayloadInput): Promise<PersistNotePayloadResult> {
  const dir = noteDir(input.noteId, input.createdAt)
  const files: NoteFileRecord[] = []
  let bodyPath: string | null = null
  let audioPath: string | null = null
  const counters: Record<string, number> = { audio: 0, image: 0, video: 0, file: 0 }

  if (input.persistBodyFile && input.body) {
    const rel = noteFileRelPath(dir, 'body')
    const dataBase64 = btoa(unescape(encodeURIComponent(input.body)))
    await writeRelPath(rel, dataBase64)
    bodyPath = Capacitor.isNativePlatform() ? rel : `sqlite:${rel}`
    const row: NoteFileRecord = {
      id: `${input.noteId}-body`,
      noteId: input.noteId,
      kind: 'body',
      relPath: rel,
      mime: 'text/markdown',
      sizeBytes: input.body.length,
      durationMs: 0,
    }
    files.push(row)
    await insertFileRow(row, dataBase64, Capacitor.isNativePlatform() ? rel : null)
  }

  for (const media of input.media) {
    counters[media.kind] = (counters[media.kind] || 0) + 1
    const mime = media.mime || media.blob.type || 'application/octet-stream'
    const ext = media.ext || extFromMime(mime, 'bin')
    const rel = noteFileRelPath(dir, media.kind as NoteFileKind, counters[media.kind], ext)
    const dataBase64 = await blobToBase64(media.blob)
    const uri = await writeRelPath(rel, dataBase64)
    const row: NoteFileRecord = {
      id: `${input.noteId}-${media.kind}-${counters[media.kind]}`,
      noteId: input.noteId,
      kind: media.kind,
      relPath: rel,
      mime,
      sizeBytes: media.blob.size,
      durationMs: media.durationMs ?? 0,
    }
    files.push(row)
    await insertFileRow(row, dataBase64, Capacitor.isNativePlatform() ? rel : null)
    if (media.kind === 'audio' && !audioPath) audioPath = uri
  }

  return { bodyPath, files, audioPath }
}

export async function readNoteBody(bodyPath: string | null): Promise<string | null> {
  if (!bodyPath) return null
  if (bodyPath.startsWith('sqlite:')) {
    const rel = bodyPath.slice('sqlite:'.length)
    const row = await localDB.queryOne<{ data_base64: string }>(
      'SELECT data_base64 FROM local_note_files WHERE rel_path = ? OR rel_path = ?',
      [rel, bodyPath],
    )
    if (!row?.data_base64) return null
    return decodeURIComponent(escape(atob(row.data_base64)))
  }
  if (!Capacitor.isNativePlatform()) return null
  try {
    const res = await Filesystem.readFile({ path: bodyPath, directory: Directory.Data })
    const data = typeof res.data === 'string' ? res.data : ''
    return decodeURIComponent(escape(atob(data)))
  } catch {
    const row = await localDB.queryOne<{ data_base64: string }>(
      'SELECT data_base64 FROM local_note_files WHERE rel_path = ?',
      [bodyPath],
    )
    if (!row?.data_base64) return null
    return decodeURIComponent(escape(atob(row.data_base64)))
  }
}

export async function deleteNoteFiles(noteId: string, createdAt: number): Promise<void> {
  const dir = noteDir(noteId, createdAt)
  if (Capacitor.isNativePlatform()) {
    await Filesystem.rmdir({ path: dir, directory: Directory.Data, recursive: true }).catch(() => {})
  }
  await localDB.run('DELETE FROM local_note_files WHERE note_id = ?', [noteId])
}
