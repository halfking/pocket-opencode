import { Capacitor } from '@capacitor/core'
import { Filesystem, Directory } from '@capacitor/filesystem'
import { localDB } from '../../native/local-db'
import { blobToBase64, utf8ToBase64, base64ToUtf8 } from '../../utils/base64'
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

/** 单个附件入库上限：base64 后膨胀约 1/3，整块读进内存再写 SQLite。 */
const MAX_ATTACHMENT_BYTES = 20 * 1024 * 1024

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
    await Filesystem.mkdir({ path: parent, directory: Directory.Data, recursive: true }).catch(() => {
      // mkdir 失败不致命：紧接着的 writeFile 会自己抛出去（这里没有 catch），
      // 调用方会拿到这个错误 ⇒ 失败不会消失。
    })
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
    const dataBase64 = utf8ToBase64(input.body)
    await writeRelPath(rel, dataBase64)
    bodyPath = Capacitor.isNativePlatform() ? rel : `sqlite:${rel}`
    const row: NoteFileRecord = {
      id: `${input.noteId}-body`,
      noteId: input.noteId,
      kind: 'body',
      relPath: rel,
      mime: 'text/markdown',
      // 字节数，不是字符数。`input.body.length` 是 UTF-16 code unit 数，
      // 对中文正好差 3 倍（实测「（语音草稿）」6 字符 → 记成 6，实际 18 字节），
      // 与下面 media 行的 `media.blob.size`（真字节）**单位不一致**。
      // 该列当前没有消费方，所以这不会立刻显形；但任何按字节做的判断
      // （限额、去重、UI 展示大小）都会对正文少算。写法对齐 asset-store.ts:293。
      sizeBytes: new Blob([input.body]).size,
      durationMs: 0,
    }
    files.push(row)
    await insertFileRow(row, dataBase64, Capacitor.isNativePlatform() ? rel : null)
  }

  for (const media of input.media) {
    if (media.blob.size > MAX_ATTACHMENT_BYTES) {
      console.warn(`[note-files] 附件超过 ${MAX_ATTACHMENT_BYTES} 字节上限，跳过: ${media.kind} ${media.blob.size}`)
      continue
    }
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

/**
 * §891：正文 base64 解码。**解不出来就出声并返回 null**，绝不把「读不到」
 * 报成「正文是空的」。
 *
 * ⚠⚠ D61 的根因就在这：这三处原先是裸的
 *   `decodeURIComponent(escape(atob(x)))`，正文文件被截断时会抛
 *   URIError / DOMException，**绕过了 `loadFullContent` 的 null 降级**
 *   ⇒ 那条写在 `notes-persist.ts:79-80` 的降级契约在损坏场景下形同虚设。
 *
 * ⚠ 出声而不上抛，是因为上游 `NoteDetailView` / `NoteEditView` 的 catch
 *   会把异常显示成「加载笔记失败」—— 对用户是「笔记坏了」，
 *   而这里要说的是「这个文件的 base64 解不开、路径是 X」。
 */
function decodeBody(base64: string, bodyPath: string): string | null {
  const text = base64ToUtf8(base64)
  if (text === null) {
    console.warn(
      `[note-files] 正文 base64 解码失败（长度 ${base64.length}）：${bodyPath}`,
    )
  }
  return text
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
    return decodeBody(row.data_base64, bodyPath)
  }
  if (!Capacitor.isNativePlatform()) return null
  try {
    const res = await Filesystem.readFile({ path: bodyPath, directory: Directory.Data })
    const data = typeof res.data === 'string' ? res.data : ''
    return decodeBody(data, bodyPath)
  } catch {
    const row = await localDB.queryOne<{ data_base64: string }>(
      'SELECT data_base64 FROM local_note_files WHERE rel_path = ?',
      [bodyPath],
    )
    if (!row?.data_base64) return null
    return decodeBody(row.data_base64, bodyPath)
  }
}

export async function deleteNoteFiles(noteId: string, createdAt: number): Promise<void> {
  const dir = noteDir(noteId, createdAt)
  if (Capacitor.isNativePlatform()) {
    await Filesystem.rmdir({ path: dir, directory: Directory.Data, recursive: true }).catch((e) => {
      // 删不掉笔记目录 ⇒ 磁盘上留着一份孤儿文件（用户看不到，也不会自动清）。
      // 没有别处会提示 ⇒ 留痕。
      console.warn('[note-files] 笔记目录删除失败（磁盘上留下孤儿文件）：', e)
    })
  }
  await localDB.run('DELETE FROM local_note_files WHERE note_id = ?', [noteId])
}

/**
 * 只删正文文件，**保留** audio/ images/ videos/ files/ 下的媒体。
 *
 * 2026-10-06 真机复现：用户在笔记详情页改一个字并保存，这条笔记的录音被
 * 彻底销毁（audio_path 变 NULL、audio_duration_ms 归零、audio/01.webm 连同
 * 整个笔记目录一起没了），UI 零提示。成因是 updateNote 在「正文变了」时
 * 调的是 deleteNoteFiles —— 它对整个笔记目录做 rmdir recursive。
 *
 * 用户改的是文字，不是录音；content 与 media 在域模型里是两个独立字段，
 * 媒体文件名还带序号（audio/01.webm），本就不该因为改正文而失效。
 * 换成媒体时才该整体替换，那种情况走 deleteNoteFiles 仍然正确。
 */
export async function deleteNoteBodyFile(noteId: string, createdAt: number): Promise<void> {
  const rel = noteFileRelPath(noteDir(noteId, createdAt), 'body')
  if (Capacitor.isNativePlatform()) {
    await Filesystem.deleteFile({ path: rel, directory: Directory.Data }).catch((e) => {
      // 正文文件删不掉 ⇒ 磁盘上留着一份孤儿文件（下次编辑会重新覆盖它）。
      // 没有别处会提示 ⇒ 留痕。
      console.warn('[note-files] 笔记正文文件删除失败（磁盘上留下孤儿文件）：', e)
    })
  }
  // 只清 body 那一类子行；audio 等媒体行留着，播放与回填都依赖它们
  await localDB.run('DELETE FROM local_note_files WHERE note_id = ? AND kind = ?', [noteId, 'body'])
}
