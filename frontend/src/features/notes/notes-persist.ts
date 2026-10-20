import { localDB } from '../../native/local-db'
import { vectorIndex } from '../../native/vector'
import { http } from '../../api/http'
import { encryptString } from '../../native/crypto'
import { useCryptoConfig } from '../../stores/crypto-config'
import { assetStore } from '../../native/asset-store'
import { decideNoteStorage, splitForEmbedding } from './note-storage-policy'
import { sha1Hex } from '../../utils/text-hash'
import { needsFullRebuild } from './embed-reuse'
import { deleteNoteBodyFile, deleteNoteFiles, persistNotePayload, readNoteBody } from './note-files'
import { RECORD_MEDIA_CLEANUP_FAIL_TAG } from '../../utils/action-failure-notice'
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
  //
  // ⚠⚠ §891/D61：上面这条降级**只在 body_path 还没回填时成立**
  //   （读函数首行 `if (!note.bodyPath) return note.content` 直接短路）。
  //   一旦 body_path 已回填、正文文件又读不到，现在**抛错而不是退回 content** ——
  //   因为此时 content 是 `decideNoteStorage` 写进去的 ≤280 字**摘要**，
  //   退回它等于让摘要冒充全文，而本函数下方的 updateNote 会把它写回去覆盖全文。
  //   ⇒ 「附件写失败」是安全降级，「正文丢失」不是，两者不可混为一谈。
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

  // ⚠⚠ §896/D66：喂给 embedding 的是 `embedText`（纯内容）**不是** `searchText`
  //   ——后者带着给 FTS 用的合成 bigram 尾巴（短笔记里能占一半以上）。
  //   这两个失败都是 fire-and-forget（正文已落 SQLite，不该让主链路等云端），
  //   所以**两条路径的失败必须同样出声**：此前一条 warn、一条完全吞掉，
  //   于是「保存一条长会议笔记」既没有向量、也没有任何痕迹。
  embedAndStore(note.id, decided.embedText || note.content).catch((e) => {
    console.warn('[lobster] 嵌入失败，笔记已存但暂无向量:', e)
  })
  // 记下这次镜像的 promise，让依赖后端行存在的调用方（录音后的 AI 总结）
  // 能 await 它。createNote 自己不等 —— 笔记正文已落 SQLite，主链路不该等云端。
  noteMirrorReady.set(note.id, mirrorNoteToBackend(note).catch((e) => {
    console.warn('[notes] 镜像到后端失败，AI 总结/分类暂不可用:', e)
  }))
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
    // ⚠ §896/D66：此前是 `.catch(() => {})` —— 整条编辑保存路径的嵌入失败**零痕迹**，
    //   而它是长笔记**唯一**的重试机会（创建时已经失败过一次）。改为与创建路径对称。
    embedAndStore(id, decided.embedText).catch((e) => {
      console.warn('[notes] 正文已更新但向量写入失败，语义检索暂时搜不到这条:', e)
    })
  }
}

/**
 * 删除一条笔记（软删 + 清理附属文件）。
 *
 * ⚠⚠ §838/D3：**软删必须排在文件清理之前**。原顺序是
 *   「deleteNoteFiles → 标记 deleted_at → vectorIndex.remove」，
 *   ⇒ 文件删不掉时**连软删都没发生** ⇒ 用户点删除，笔记原封不动，且零提示
 *   （调用方此前也没有 try/catch）。
 *   ⚠ 本文件自己在媒体替换那处（updateNote 里 deleteNoteBodyFile 上方）
 *     写下了同一条原则：「删除永远不能排在写之前 —— 写失败就没有回滚了」。
 *     ⇒ 同一个文件里两条删除路径遵守了相反的顺序，这一条是遗漏。
 *
 * ⇒ 现在的顺序与代价：
 *   ① 先标记 deleted_at（用户意图达成，笔记从列表消失）；
 *   ② 文件与向量索引清理各自**独立兜住**：
 *      · 文件删不掉 ⇒ **孤儿文件**残留占空间（与会议侧音频清理同款，见 §838/D1），
 *        但**不再**让「删除没生效」这种更糟的结果发生；
 *      · 向量索引删不掉 ⇒ 检索可能仍命中这条已删笔记（同样只记诊断，不阻断）。
 *
 * @returns `mediaCleaned=false` 表示主体已删、但附属文件没清干净。
 */
export async function deleteNote(
  id: string,
  workspaceId = 'default',
): Promise<{ mediaCleaned: boolean }> {
  const existing = await getNote(id, true, workspaceId)
  if (existing?.storage === 'asset') {
    await assetStore.softDeleteForWorkspace(id, workspaceId)
    return { mediaCleaned: true }
  }
  // ① 软删先行：这一步失败 ⇒ 笔记仍在，调用方如实报「删除失败」（不假装）。
  await localDB.run('UPDATE local_notes SET deleted_at = ? WHERE id = ? AND workspace_id = ?', [Date.now(), id, workspaceId])
  // ② 附属清理：各自兜住，绝不因为它失败而让「删除没生效」。
  let mediaCleaned = true
  if (existing && (existing.bodyPath || existing.mediaJson)) {
    try {
      await deleteNoteFiles(id, existing.createdAt)
    } catch (e) {
      mediaCleaned = false
      console.error(RECORD_MEDIA_CLEANUP_FAIL_TAG, '笔记正文/媒体文件', id, e)
    }
  }
  try {
    await vectorIndex.remove(id)
  } catch (e) {
    console.warn('[notes-persist] 向量索引清理失败（笔记已软删，检索可能仍命中）：', id, e)
  }
  return { mediaCleaned }
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

/**
 * §891/D61：`storage_tier='file'` 的笔记**正文文件读不到**时抛这个错。
 *
 * ⚠⚠ 这是为了**挡住静默的数据销毁**，不是为了「加载不出就报错」：
 *   `note-storage-policy.ts::decideNoteStorage` 在 tier='file' 时
 *   `displayContent = summarizeForDisplay(body)`（**只取第一段、≤280 字**），
 *   而它落的就是 `local_notes.content` 那一列。
 *   ⇒ 原来 `body ?? note.content` 在正文读不到时，是**拿 280 字摘要冒充全文**。
 *   而 `updateNote`（本文件）在 `patch.content !== undefined` 时会
 *   `persistNotePayload({ body: nextContent })` 把这份摘要**写回正文文件**
 *   ⇒ 「打开笔记 → 改个标题 → 保存」就足以把全文永久换成摘要。
 *
 * ⚠ 与既有行为一致：正文文件**损坏**时原先就会抛（裸 URIError/DOMException），
 *   本节只是让「文件不存在 / sqlite 行缺失」与它对齐 —— 两者都读不到全文，
 *   差别只在损坏与缺失，**不该一个静默销毁数据、一个报错**。
 */
export class NoteBodyUnavailableError extends Error {
  readonly bodyPath: string
  constructor(bodyPath: string) {
    super(`笔记正文文件读不到：${bodyPath}（不做降级：tier='file' 的 content 只是 ≤280 字摘要，保存会把它写回正文文件覆盖全文）`)
    this.name = 'NoteBodyUnavailableError'
    this.bodyPath = bodyPath
  }
}

export async function loadFullContent(note: LocalNote): Promise<string> {
  if (note.storageTier !== 'file' || !note.bodyPath) return note.content
  const body = await readNoteBody(note.bodyPath)
  // ⚠⚠ §891/D61：原来这里是 `return body ?? note.content`。
  //   在 tier='file' 下 `note.content` 是摘要不是全文，而 updateNote 会把它写回去
  //   ⇒ **打开再保存就能销毁全文**。读不到就必须出声，不许静默降级。
  //
  //   ⚠ 上游影响（已核对，非推断）：
  //     NoteDetailView.vue / NoteEditView.vue 的 load() 都在 try 里
  //     ⇒ 用户看到 `errors.loadNotesFailed`，且视图不会 hydrate
  //     ⇒ **无法进入「编辑并保存」那条销毁路径**（数据因此保住）。
  //   ⛔ 未实测：正文文件缺失/损坏在真机上的真实发生率。
  //   ⚠ 产品口径待拍板（简报 114）：若要「仍能打开」，正确设计是给
  //     updateNote 传一个「本次内容是降级来的」标记，禁止它被写回，
  //     而不是让摘要冒充全文。
  if (body === null) throw new NoteBodyUnavailableError(note.bodyPath)
  return body
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

/**
 * 记录「本地笔记 → 后端镜像」的 in-flight promise。
 *
 * 为什么需要它：`createNote` 为了不让云端拖慢主链路，**不 await** 镜像；
 * 但紧随其后的 /summarize 需要后端已经有这行（否则 404）。
 * ⇒ 产出方登记、消费者 await，两边都拿到同一个 promise，不会各发一次。
 *
 * 用 Map 而不是模块级单变量：一次录音只涉及一条笔记，但同页可能连续录多条，
 * 单变量会让第二条覆盖第一条的 promise。
 *
 * 条目在 settle 后删掉：避免长会话里无限增长（录音是用户主动行为，
 * 频率低，但仍不该留）。
 */
const noteMirrorReady = new Map<string, Promise<void>>()

/**
 * 等某条笔记在后端落行。**永不抛异常** —— 镜像失败时 summarize 照常尝试，
 * 让服务端自己回 404 / 报错，错误信息更贴近真实原因。
 */
export async function awaitNoteMirror(noteId: string): Promise<void> {
  const p = noteMirrorReady.get(noteId)
  if (!p) return
  try {
    await p
  } catch {
    // 已在 mirrorNoteToBackend 的 catch 里记过日志，这里不重复。
  } finally {
    noteMirrorReady.delete(noteId)
  }
}

/**
 * `/api/embed` 的**切段触发阈值**（与服务端硬编码同值）。
 *
 * ⚠ 出处：`backend/internal/server/server_assistant.go:2853`
 *   `if len(body.Text) > 16000 { 400 "text too long (max 16000 bytes)" }`
 *   —— 这是**服务端**的数，前端此前**完全不知道**，超了就是一次必然失败的请求。
 *   两处硬编码要不要收敛成一处契约，见简报 119（属主口径，本节未代拍）。
 *
 * ⚠⚠ **单位陷阱（§917 的本体）**：服务端那道闸判的是 **UTF-8 字节**
 * （Go 的 `len(string)`，实测 `len("中") == 3`）⇒ 16000 字节只装得下
 * **5333 个中文字**；而**服务端那段错误信息原先写的也是「chars」**
 * ⇒ 信息与判定不同形 ⇒ 任何「按字符数理解」的实现都会超闸 3 倍。
 *
 * ⚠ **本常量现在只是「要不要切成多段」的码元阈值**；
 *   **真正不可越过的红线是字节闸**（`note-storage-policy.ts` 的
 *   `EMBED_BYTE_LIMIT`）⇒ 每一个送进 `/api/embed` 的块都必须 ≤ 它（§917 落地）。
 *   ⇒ 常量名里的「CHAR」是**历史遗留的名字**（那时按字符理解），别再照字面理解它。
 */
const EMBED_TEXT_CHAR_LIMIT = 16000

async function embedAndStore(noteId: string, content: string): Promise<void> {
  // ⚠⚠ §898：超长文本**不再截断**，而是**按段分别嵌入**。
  //
  //   §896 的做法是「截到 16000 字就发」⇒ 长会议的后半段**永远进不了索引**
  //   （尾部不是「弱相关」，是**完全不相关**）。
  //   ⇒ 现在改成：短文本仍是一次调用一个向量（行为与从前逐字一致），
  //     超过单发上限的按段切开，逐段嵌入，**整篇每一段都被索引到**。
  //
  //   ⚠ 为什么必须「每段一个向量」而不是「所有段平均成 1 个」：
  //   实测（局部代理嵌入，4 小时会议 18 段，第 10 段埋一句关键信息）：
  //     截断 0.0452 / 质心 0.0492 / 整篇单向量 0.0914 / 段级 max **0.1442**
  //   而一条中等相关的短笔记是 0.0394 ⇒ 截断与质心只比它高 15%~25%
  //   ⇒ 长会议在有若干短笔记的库里会被挤出 top-k。
  //   详见 `vector-index.ts::search` 上方的注释与 §898 文档。
  const chunks = splitForEmbedding(content, { limit: EMBED_TEXT_CHAR_LIMIT })
  if (chunks.length === 0) return
  const hashes = chunks.map((c) => sha1Hex(c.text))

  // ⚠⚠ §899：**只重新嵌入「变了的段」**，没变的段直接复用库里的向量。
  //
  //   这是「编辑一条 4 小时会议」从 15 次调用降到 ~1 次的关键。
  //   ⚠ 复用成立的前提是**段边界稳定** —— 固定偏移切段时改一个字会让
  //     后面所有段都变（实测 0/22 段存活）；段落锚定切段时 20/21 段存活。
  //     ⇒ 切段方式必须是段落锚定的（见 `splitForEmbedding`）。
  const prev = await vectorIndex.existingChunks(noteId)
  const first = await embedChunks(chunks, prev)
  // ⚠⚠ **换模型 ⇒ 旧向量不在同一个空间里** ⇒ 混用会让相似度失去意义
  //   ⇒ 一律全部重嵌（安全侧）。决策在纯函数里（`embed-reuse.ts`），
  //   判据直接验它的行为（源码门对同形改动无效，§95.3 的老教训）。
  if (needsFullRebuild(first.apiModel, prev?.model ?? null, first.reused)) {
    const rebuilt = await embedChunks(chunks, null)
    await vectorIndex.add(noteId, rebuilt.vectors, rebuilt.model, hashes)
    return
  }
  await vectorIndex.add(noteId, first.vectors, first.model, hashes)
}

/**
 * 逐段嵌入；`prev` 给了就**复用没变的段**（不调 API）。
 *
 * @returns apiModel 本次 API 返回的模型（null = 全程复用，没调过 API）；
 *          reused 实际复用了多少段。两者交给 `needsFullRebuild` 判定要不要全部重嵌。
 */
async function embedChunks(
  chunks: { text: string }[],
  prev: { hashes: string[]; vectors: Float32Array[]; model: string } | null,
): Promise<{ vectors: Float32Array[]; model: string; reused: number; apiModel: string | null }> {
  const hashes = chunks.map((c) => sha1Hex(c.text))
  const vectors: Float32Array[] = []
  let model = prev?.model ?? ''
  let apiModel: string | null = null
  let reused = 0
  for (let i = 0; i < chunks.length; i++) {
    const h = hashes[i]
    if (prev) {
      const idx = prev.hashes.indexOf(h)
      if (idx >= 0) {
        vectors.push(prev.vectors[idx])
        reused++
        continue
      }
    }
    const res = await http<{ embedding: number[]; model: string }>('/api/embed', {
      method: 'POST',
      body: JSON.stringify({ text: chunks[i].text }),
    })
    apiModel = res.model
    model = res.model
    vectors.push(Float32Array.from(res.embedding))
  }
  // ⚠ 全程没调 API（全部复用）⇒ apiModel 保持 null ⇒ 不会有换模型问题。
  return { vectors, model, reused, apiModel }
}

/**
 * 把本地笔记镜像到后端，然后才触发分类。
 *
 * ## 2026-10-06：这里修的是一条**整条链路恒 404** 的缺陷
 *
 * 改动前是 `http('/api/notes/${id}/classify', {method:'POST'}).catch(()=>{})`，
 * 直接对一条**后端从不知道的**笔记调 classify。而后端
 * handleNoteClassify / handleNoteSummarize 都是先
 * `notesStore.GetByIDScoped(id)` 再 `404 note not found`。
 *
 * 笔记是**纯本地**的：`createNote` 只写 SQLite（localDB），
 * `newNoteId()` 在本地生成 id，全仓**没有任何一处** POST /api/notes
 * （`notesApi.create/update/list/...` 的真实调用方数量是 0），
 * 后端 notes 表因此永远没有这条行。
 *
 * 后果分两层，第二层才致命：
 *   1. `.catch(() => {})` 把 404 吞掉 ⇒ classify 静默失效，无人察觉；
 *   2. **录音停止后的 AI 总结因此也从来没成功过** ——
 *      NoteListView.presentVoiceDraft 调 /api/notes/{id}/summarize，
 *      同样 404，走 catch 显示「总结失败」。
 *      需求「录音时即时总结」在随手记侧从来只是界面上的一个动作。
 *
 * ⇒ 必须先把笔记镜像上去，classify 与 summarize 才有得可查。
 *   顺序有依赖：先 POST /api/notes 落行，再调 classify。
 *
 * 为什么镜像**不阻塞** createNote 的返回：笔记正文已落 SQLite，
 * 后端只是 AI 能力的元数据源。云端不可达时笔记照常可用，
 * 只是没有 AI 总结/分类 —— 不能让主链路跟着炸。
 * 但 summarize 依赖它，所以它是 fire-and-forget 的**前置**，
 * 由 presentVoiceDraft 那一侧等它（见 note-ai-mirror.ts）。
 */
async function mirrorNoteToBackend(note: LocalNote): Promise<void> {
  const res = await http<{ id?: string }>('/api/notes', {
    method: 'POST',
    body: JSON.stringify({
      id: note.id,
      title: note.title ?? undefined,
      // 2026-10-06：后端 Note 有了独立的 content 字段（DB 的 content 列
      // 一直是 TEXT 无长度上限，此前是**读出来就被丢弃**，见
      // notes/note.go 的 Content 注释）。把完整正文传过去，
      // 长语音笔记的行动项抽取与 agent 检索才拿得到后半段。
      content: note.content,
      // snippet 仍然只传前 200 字：它是列表渲染用的摘要，
      // 不是正文（DB 里两者是**两列**，不是同一份数据的两个读法）。
      snippet: note.content.slice(0, NOTE_MIRROR_SNIPPET_RUNES),
      contentType: note.contentType,
      domain: note.domain ?? undefined,
      tags: note.tags ?? undefined,
      audioPath: note.audioPath ?? undefined,
      audioDuration: note.audioDurationMs ?? 0,
      createdByVoice: note.createdByVoice,
    }),
  })
  await http(`/api/notes/${note.id}/classify`, { method: 'POST' }).catch((e) => {
    // 自动分类没上报成功 ⇒ 这条笔记不会被服务端归类（正文照常落库，主链路不受影响）。
    // 没有别处会提示 ⇒ 留痕。
    console.warn('[notes] 笔记自动分类未能上报：', e)
  })
  void res
}

/**
 * 镜像给后端的 `snippet` 长度（**只**用于 snippet，不是正文长度）。
 *
 * ⚠ 2026-10-07（§168）**就地更正**：本段此前写的是
 *   「后端 notes 表的 `content` NOT NULL 存的就是 Snippet（store.go 的
 *   `content := n.Snippet`），服务端没有更长的字段可存」。
 *   **那段描述的是 2026-10-06 之前的行为，早已不成立**：
 *
 *   · `backend/internal/notes/store.go:107` 实际是 `content := n.Content`，
 *     而**不是** `n.Snippet`；上面还有一句回落：`content` 为空才用 `Snippet`
 *     （为了让只填 Snippet 的旧调用方不撞 NOT NULL）。
 *   · `content` 与 `snippet` 在 DB 里是**两列**（`store.go:259`
 *     「正文与 snippet 是两列（2026-10-06）」），不是同一份数据的两个读法。
 *   · `Note.BodyForLLM` **优先读 `Content`**，只有空才回落 `Snippet`
 *     ⇒ 长语音笔记的行动项抽取与 agent 检索拿得到后半段。
 *
 * ⇒ 所以「这里截断不是缺陷」这个**结论**仍然成立，但**理由变了**：
 *   截断的从来只是 `snippet`（列表渲染缓存，服务端 `snippetRunes = 200`
 *   明写「the snippet length the local cache uses for list rendering」），
 *   而**正文 `content` 原样送达、不截断**。
 *
 * ★ 为什么值得专门写这段：紧邻上方 `mirrorNoteToBackend` 载荷里那段注释
 *   描述的是**修好后**的正确语义，两段正面相邻却互相矛盾
 *   ⇒ 只落到下面这段的人会拿到完全错的模型。
 */
const NOTE_MIRROR_SNIPPET_RUNES = 200
