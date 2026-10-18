import { cjkBigramTokens } from './note-fts-query.ts'

/** 纯文本且不超过该字数时整篇进 SQLite，否则摘要进库、正文进文件。 */
export const INLINE_CHAR_LIMIT = 1000
export const SUMMARY_CHAR_LIMIT = 280

export type StorageTier = 'inline' | 'file'

export interface NoteStorageInput {
  title?: string | null
  body: string
  tags?: string[] | null
  hasMedia: boolean
}

export interface NoteStorageDecision {
  tier: StorageTier
  displayContent: string
  summary: string
  /** 进 FTS 的文本：纯内容 + 合成 bigram（§886 的 CJK 子串匹配靠它）。 */
  searchText: string
  /**
   * 送去做向量的文本：纯内容，**不含** bigram（§896/D66）。
   * ⚠ 与 `searchText` 同源同一次重建，只是投影不同 —— 不要从这里再派生别的。
   */
  embedText: string
  persistBodyFile: boolean
}

/**
 * 送去做向量的**纯内容**（标题 + 正文 + 标签），**不含** FTS 的 bigram 尾巴。
 *
 * ⚠⚠ §896/D66：`buildSearchText` 的尾巴是 `cjkBigramTokens` 合成出来的
 *   **机器噪声**（最多 80 个 gram、约 239 字符），它是给 FTS5 做子串匹配用的
 *   ⇒ 把同一份文本喂给 embedding 模型，等于让一半的输入是乱码。
 *   实测（正文 200 字时）：实收文本里 **53.2%** 是合成 gram。
 *   ⇒ 两个消费者要两种文本，FTS 侧一个字都没变。
 */
export function buildEmbedText(
  title: string | null | undefined,
  body: string,
  tags?: string[] | null,
): string {
  return [title?.trim(), body.trim(), (tags ?? []).filter(Boolean).join(' ')].filter(Boolean).join('\n')
}

export function buildSearchText(
  title: string | null | undefined,
  body: string,
  tags?: string[] | null,
): string {
  const core = buildEmbedText(title, body, tags)
  const grams = cjkBigramTokens(`${title || ''} ${body}`)
  return grams ? `${core}\n${grams}` : core
}

export function summarizeForDisplay(body: string, maxChars = SUMMARY_CHAR_LIMIT): string {
  const trimmed = body.trim()
  if (!trimmed) return ''
  const paragraph = trimmed.split(/\n\s*\n/)[0]?.trim() ?? trimmed
  if (paragraph.length <= maxChars) return paragraph
  return paragraph.slice(0, maxChars)
}

export function decideNoteStorage(input: NoteStorageInput): NoteStorageDecision {
  const body = input.body ?? ''
  const searchText = buildSearchText(input.title, body, input.tags)
  const embedText = buildEmbedText(input.title, body, input.tags)
  const needsFile = input.hasMedia || body.length > INLINE_CHAR_LIMIT
  if (!needsFile) {
    return {
      tier: 'inline',
      displayContent: body,
      summary: summarizeForDisplay(body),
      searchText,
      embedText,
      persistBodyFile: false,
    }
  }
  const summary = summarizeForDisplay(body)
  return {
    tier: 'file',
    displayContent: summary,
    summary,
    searchText,
    embedText,
    persistBodyFile: true,
  }
}
