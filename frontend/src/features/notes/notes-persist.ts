import { localDB } from '../../native/local-db'
import { vectorIndex } from '../../native/vector'
import { http } from '../../api/http'
import { encryptString } from '../../native/crypto'
import { useCryptoConfig } from '../../stores/crypto-config'
import { assetStore } from '../../native/asset-store'
import { decideNoteStorage } from './note-storage-policy'
import { deleteNoteBodyFile, deleteNoteFiles, persistNotePayload, readNoteBody } from './note-files'
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

  // 写入顺序是外键决定的，不是风格问题：schema.ts 里
  // `local_note_files.note_id REFERENCES local_notes(id)`，而
  // persistNotePayload() 会先往 local_note_files 插子行。父行不存在就插子行，
  // SQLite 报 787 FOREIGN KEY constraint failed（2026-10-05 真机复现：
  // 语音笔记一条都存不进去，createNote 抛错被 Vue 吞进 console.error）。
  //
  // 所以先插父行（body_path/media_json 留空），附件写完再 UPDATE 回填。
  // 附件写失败时父行仍在，loadFullContent() 会退回 note.content，
  // 用户至少拿得到文字——比整条丢失、且报错被吞要好。
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
    note.bodyPath = bodyPath
    note.mediaJson = mediaJson
    note.audioPath = audioPath
    await localDB.run(
      `UPDATE local_notes
         SET body_path = ?, media_json = ?, audio_path = ?, audio_duration_ms = ?
       WHERE id = ?`,
      [bodyPath, mediaJson, audioPath, note.audioDurationMs, id],
    )
  }

  embedAndStore(note.id, decided.searchText || note.content).catch((e) => {
    console.warn('[lobster] 嵌入失败，笔记已存但暂无向量:', e)
  })
  http(`/api/notes/${note.id}/classify`, { method: 'POST' }).catch(() => {})
  return note
}

export async function updateNote(
  id: string,
  patch: Partial<Pick<LocalNote, 'title' | 'content' | 'domain' | 'tags' | 'status' | 'summary'>> & {
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
      metaJson: JSON.stringify({
        ...meta,
        ...(patch.tags !== undefined ? { tags: patch.tags } : {}),
        ...(patch.summary !== undefined ? { summary: patch.summary } : {}),
      }),
      source: asset.source,
      syncMode: asset.syncMode,
    })
    return
  }

  const nextContent = patch.content !== undefined ? patch.content : await loadFullContent(existing)
  const nextTitle = patch.title !== undefined ? patch.title : existing.title
  const nextTags = patch.tags !== undefined ? patch.tags : existing.tags
  const nextSummary = patch.summary !== undefined ? patch.summary : undefined
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
  // 旧 mediaJson 里的非 body 条目要留到最后合并回去：persistNotePayload 的 files
  // 里只有本次写的正文与新媒体，旧的媒体条目会被这次覆盖抹掉。
  //
  // ★ 2026-10-06 真机复现（换媒体路径，本分支此前从未在真库上跑过）：
  //   原来 `const replacingMedia = media.length > 0`，命中时先 deleteNoteFiles
  //   （对整目录 rmdir -r）再 persistNotePayload。一旦后者抛错，
  //   结尾的 UPDATE local_notes 永不执行 ⇒ **文件与子行都没了、库行却保持旧值**，
  //   audio_path 变成指向不存在文件的悬空指针，local_note_files 里的 audio 行消失，
  //   而用户只在页面上看到「保存失败，请稍后重试」。录音不可恢复地没了，无回滚。
  //
  //   与 FK 写入顺序是同一条原则：**破坏性操作必须排在成功写之后**。
  //
  // ★ 语义同时改了（2026-10-06，产品决策）：**加附件是「追加」不是「替换」**。
  //   原来 media.length>0 意味着整条录音被替换掉 —— 用户给语音笔记补一张图片，
  //   录音就没了。新规则：
  //     · 跨类型共存：加 video/image 不动已有 audio（文件名本来就按类型分目录，
  //       audio/01.webm 与 video/01.mp4 不冲突）。
  //     · 同类型替换：新的 audio 会顶掉旧 audio（一条笔记只该有一条音轨）；
  //       连续加两个 video 则后者顶掉前者（persistNotePayload 的计数器从 1 起，
  //       会写到同一个 video/01.* 并 REPLACE 同一行 id）。
  const carriedMedia = (() => {
    if (!existing.mediaJson) return []
    try {
      const old = JSON.parse(existing.mediaJson)
      return Array.isArray(old) ? old.filter((f) => f && f.kind !== 'body') : []
    } catch {
      return []
    }
  })()

  if (patch.content !== undefined || media.length > 0) {
    // 正文还要不要单独落文件。正文变短到可以内联时为 false。
    const bodyFileNeeded = Boolean(decided.persistBodyFile && nextContent)
    if (bodyFileNeeded || media.length > 0) {
      const written = await persistNotePayload({
        noteId: id,
        createdAt: existing.createdAt,
        body: nextContent,
        persistBodyFile: bodyFileNeeded,
        media,
      })
      bodyPath = written.bodyPath
      // 同类型的以本次写的为准，跨类型的从旧 mediaJson 合回来。
      const writtenKinds = new Set(written.files.map((f) => f.kind))
      const kept = [...written.files, ...carriedMedia.filter((f) => !writtenKinds.has(f.kind))]
      mediaJson = kept.length ? JSON.stringify(kept) : null
      // 只有本次真的提交了 audio 才换引用；否则原样保留。
      // （原来这里是 `replacingMedia ? written.audioPath : existing.audioPath`，
      //   在「只改正文」之外的加附件路径上会把 audio_path 置空。）
      audioPath = written.audioPath ?? existing.audioPath
    } else {
      bodyPath = null
      // 正文不再落文件 ≠ 媒体也没了：audio_path 必须留着。
      audioPath = existing.audioPath
      const oldNonBody = carriedMedia
      mediaJson = oldNonBody.length ? JSON.stringify(oldNonBody) : null
    }
    // ★ 清理排在**写成功之后**：只有确认正文文件不再需要时才删旧 body 文件。
    //   删除永远不能排在写之前 —— 写失败就没有回滚了。
    if (!bodyFileNeeded && existing.bodyPath) {
      await deleteNoteBodyFile(id, existing.createdAt)
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
      decided.tier, nextSummary ?? decided.summary, decided.searchText, bodyPath, mediaJson,
      audioPath, audioPath ? (patch.audioDurationMs ?? existing.audioDurationMs) : (patch.audioDurationMs ?? 0),
      Date.now(),
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
