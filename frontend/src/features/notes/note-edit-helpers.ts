import type { NoteMediaInput } from './notes-types.ts'
import { extractLocalTags, inferDomain, mergeTags, suggestTitle } from './note-tags.ts'

export function parseTagsInput(raw: string): string[] {
  return raw.split(/[,，]/).map((t) => t.trim()).filter(Boolean)
}

export function tagsFromArray(tags: string[] | null | undefined): string {
  return tags && tags.length ? tags.join(', ') : ''
}

/**
 * §890/D60：data URL 格式坏掉时的**具名**错误。
 *
 * ⚠ 为什么必须是具名的：改动前这类输入会**静默**产出 0 字节 Blob，
 *   而下游 `onSave()` 的 catch 里写的是 `apiError(e, 'errors.saveFailed')`
 *   —— 收到裸 `DOMException: Invalid character` 时，日志里既不知道是哪个附件、
 *   也不知道是「编码前就坏了」还是「解码时坏了」。
 */
export class DataUrlFormatError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'DataUrlFormatError'
  }
}

/** 只取开头 40 字符进错误信息：data URL 可能有几 MB，全量进 message 会撑爆日志。 */
function excerpt(s: string): string {
  return s.length <= 40 ? JSON.stringify(s) : `${JSON.stringify(s.slice(0, 40))}…（共 ${s.length} 字符）`
}

export function dataUrlToBlob(dataUrl: string): Blob {
  // ⚠⚠ §890/D60：原写法是 `const [head, body] = dataUrl.split(',')` + `atob(body || '')`。
  //
  //   实测四种输入全部**静默产出 0 字节 Blob**、不抛错、不告警：
  //     'data:image/png;base64'（没有逗号）   → size=0  type=image/png
  //     ''（空串）                            → size=0  type=application/octet-stream
  //     'data:'                               → size=0  type=application/octet-stream
  //     'https://cdn…/a.png'（外链）           → size=0  type=application/octet-stream
  //   而 base64 部分被非法字符污染时 `atob` **会抛** ⇒ **格式坏掉一半大声一半无声**。
  //
  //   这与 §888 的 D58 同型：把「读到了但内容是空的」与「没读到」混为一谈。
  //   收法也同 §888 的 `utils/base64.ts`（缺 base64 部分 ⇒ reject）：**缺 payload 一律抛**。
  //
  //   下游后果（已修，见 NoteEditView::onSave）：
  //     attachmentsToMedia → media.blob.size = 0（不超上限、不被跳过）
  //     → persistNotePayload 照常写文件 + INSERT 一行 local_note_files
  //     ⇒ **0 字节图片被当成功落盘**，用户以为图存上了。
  //
  //   对照：同族的 `native/pocket-native.ts::dataURLToFile` 一直有守卫
  //   （`if (!m) return null`）—— 本仓不是不会写，是这里漏了。
  const comma = dataUrl.indexOf(',')
  if (comma < 0) {
    throw new DataUrlFormatError(
      `data URL 里没有逗号分隔的 payload（收到 ${excerpt(dataUrl)}）`,
    )
  }
  const head = dataUrl.slice(0, comma)
  const body = dataUrl.slice(comma + 1)
  if (!body) {
    throw new DataUrlFormatError(
      `data URL 的 payload 是空的：${excerpt(dataUrl)}`,
    )
  }
  const mime = /data:([^;,]+)/.exec(head)?.[1] || 'application/octet-stream'
  let bin: string
  try {
    bin = atob(body)
  } catch {
    // ⚠ 这里原本会把裸 DOMException 抛给上层。**保持「大声」**（它是真失败），
    //   但换成具名错误并说明是哪一步 —— 与 utils/base64.ts 同风格。
    // ⚠⚠ 注意：URL 编码传输的 data URL（`data:image/svg+xml,%3Csvg%3E`）也走这里。
    //   它需要的是 `decodeURIComponent` 而**不是** `atob`，属行为变更，本节不代拍（简报 113）。
    throw new DataUrlFormatError(
      `data URL 的 payload 不是合法 base64：${excerpt(dataUrl)}`,
    )
  }
  const bytes = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
  return new Blob([bytes], { type: mime })
}

export function attachmentsToMedia(raw: { dataUrl: string; name: string }[]): NoteMediaInput[] {
  // ⚠ §890：`mime` 这行对 `https://…` 外链会把**整个 URL** 当成 mime 用
  //   （`split(';')[0]` 没有分号 ⇒ 原样返回），并最终写进 `local_note_files.mime`。
  //   dataUrlToBlob 现在会先对无逗号的输入抛错，所以这条路径**已经到不了这里**；
  //   保留本行是为了让 mime 只有一个推导口径（head 的 media type）。
  return raw.map((a) => {
    const blob = dataUrlToBlob(a.dataUrl)
    return {
      kind: 'image' as const,
      blob,
      mime: blob.type || 'image/jpeg',
    }
  })
}

export function fileToVideoMedia(file: File): NoteMediaInput {
  return { kind: 'video', blob: file, mime: file.type || 'video/mp4' }
}

export async function extractTagsForForm(content: string, currentTags: string): Promise<{
  tagsInput: string
  title: string
  domain: ReturnType<typeof inferDomain>
}> {
  const local = extractLocalTags(content)
  let extra: string[] = []
  try {
    const { extractTagsWithAi } = await import('./note-search.ts')
    extra = await extractTagsWithAi(content)
  } catch { /* offline */ }
  return {
    tagsInput: mergeTags(parseTagsInput(currentTags), [...local, ...extra]).join(', '),
    title: suggestTitle(content),
    domain: inferDomain(content),
  }
}
