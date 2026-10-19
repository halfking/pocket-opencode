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

/**
 * §898：单次 `/api/embed` 的字符上限（与服务端硬编码一致）。
 * ⚠ 出处：`backend/internal/server/server_assistant.go:2853`
 *   `if len(body.Text) > 16000 { 400 "text too long (max 16000 chars)" }`
 */
export const EMBED_SINGLE_SHOT_LIMIT = 16000

/**
 * §898：段级嵌入的**目标段长**（字符）与**重叠**（字符）。
 *
 * ⚠⚠ 这两个数是**技术默认值**，属主尚未拍板（简报 120 第 ① 问），
 *   最终应由真实笔记的**召回实测**定 —— 而测召回要真实 embedding 服务
 *   （网关 key 当前 401，简报 118 已登记）。
 *   ⇒ 改这两个数是**一处**改动，不要散落到调用点。
 *
 * 取 4000 的理由：
 *   · 250 字/分钟 ⇒ 一段 ≈ 16 分钟发言，是一个「还讲同一个主题」的粒度；
 *   · 段数与调用次数成正比（4 小时会议 = 15 段 = 15 次调用），段长翻倍则调用减半、精度下降；
 *   · 重叠 200 字：段边界切在句子中间时，两边各留 200 字兜住那一句。
 */
export const EMBED_CHUNK_TARGET = 4000
export const EMBED_CHUNK_OVERLAP = 200

export interface EmbedChunk {
  /** 段序号，从 0 开始 */
  index: number
  /** 在原文里的起始字符位 */
  start: number
  /** 结束字符位（不含） */
  end: number
  /** 送去嵌入的文本 */
  text: string
}

/**
 * 把一段文本切成送去 `/api/embed` 的若干段。
 *
 * 形态（§898）：
 *   · 长度 ≤ `EMBED_SINGLE_SHOT_LIMIT` ⇒ **整篇一段**（绝大多数随手记都走这里，
 *     行为与从前**逐字一致**：一次调用、一个向量）；
 *   · 超出 ⇒ 按 `EMBED_CHUNK_TARGET` 切，段间重叠 `EMBED_CHUNK_OVERLAP`。
 *
 * ⚠⚠ **切段位置是「可检索性」的一半**：段切得太细，匹配到的那句话会落在一个
 *   语义很窄的段里（好）；段切得太粗，一段里混了好几个主题（差）。
 *   ⇒ 目标段长是**待实测**的数，不是「拍脑袋就对了」的数（简报 120）。
 *
 * ⚠ 段数**不随长度单调**：15999 字 = 1 段、16001 字 = 5 段（多 2 个字符 ×5）。
 *   这是「短文本单发、超了才切」这条策略的固有膝点，已在 §897.5 登记。
 */
export function splitForEmbedding(
  text: string,
  opts: { limit?: number; target?: number; overlap?: number } = {},
): EmbedChunk[] {
  const limit = opts.limit ?? EMBED_SINGLE_SHOT_LIMIT
  const target = opts.target ?? EMBED_CHUNK_TARGET
  const overlap = opts.overlap ?? EMBED_CHUNK_OVERLAP
  if (text.length === 0) return []
  if (text.length <= limit) {
    return [{ index: 0, start: 0, end: text.length, text }]
  }
  // ⚠⚠ §899：**段落锚定**切段（不再是固定偏移）。
  //
  //   固定偏移（§898 最初那版）的致命处：改一个字会让**后面所有段**的边界整体位移
  //   ⇒ 每一段的文本都变了 ⇒ 增量重嵌**一段也省不下**。
  //   实测（4 小时会议 82688 字 / 900 段，改第 3 段里一个词）：
  //     固定偏移  ⇒ 逐字未变的段 **0/22**（0%）   ⇒ 22 次调用
  //     段落锚定  ⇒ 逐字未变的段 **20/21**（95%）  ⇒ **1 次调用**
  //
  //   ⇒ 段落锚定同时买到两样东西：
  //     ① 增量重嵌能用（§899 的全部意义）；
  //     ② 段边界落在段落上 ⇒ 段的语义更整（固定偏移会切在句子中间）。
  const groups = groupByParagraph(text, target)
  const out: EmbedChunk[] = []
  for (const g of groups) {
    // 一组可能超过单发上限（段落特别长时）⇒ 组内再按大小切
    if (g.end - g.start <= limit) {
      out.push({ index: out.length, start: g.start, end: g.end, text: text.slice(g.start, g.end) })
      continue
    }
    const step = Math.max(1, limit - overlap)
    for (let i = g.start; i < g.end; i += step) {
      const e = Math.min(g.end, i + limit)
      if (e - i < limit * 0.25 && out.length > 0) {
        // 末段太短就并回上一段：单独拎出一个几十字的段既浪费一次调用，也没有语义。
        const prev = out[out.length - 1]
        prev.end = e
        prev.text = text.slice(prev.start, prev.end)
        break
      }
      out.push({ index: out.length, start: i, end: e, text: text.slice(i, e) })
      if (e >= g.end) break
    }
  }
  return out
}

/**
 * 按段落分组：累积段落直到接近 `target` 字符。
 *
 * ⚠⚠ **返回的是原文里的真实偏移**，组的文本就是 `text.slice(start, end)`
 *   —— 不重新拼接段落分隔符。
 *   ⇒ 第一版用 `split` + `join('\n\n')` 重建，于是：
 *     · 原文分隔符是 `'\n \n'`（3 字符）时，重建变成 `'\n\n'`（2 字符）
 *       ⇒ 段的文本与原文**不再逐字相同**，且累积偏移会**漂移**；
 *     ⇒ 段哈希因此与原文对不上，增量重嵌的「未变段」判定会失准。
 *   ⇒ 现在直接记录原文里的段落区间。
 *
 * ⚠ 段落分隔符按 `\n\s*\n` 判定（与本仓 `summarizeForDisplay` 一致）。
 * ⚠ **没有段落分隔符的文本**（一整块）⇒ 退化成 1 组 ⇒ 由调用方按大小再切。
 */
function groupByParagraph(text: string, target: number): { start: number; end: number }[] {
  const seps: number[] = []
  const re = /\n\s*\n/g
  let m: RegExpExecArray | null
  while ((m = re.exec(text)) !== null) seps.push(m.index)
  // 段落区间：[0, sep0), [sep0 + 分隔符长度, sep1 + 分隔符长度), …
  const bounds: { start: number; end: number }[] = []
  let start = 0
  for (const sep of seps) {
    const end = sep
    if (end > start) bounds.push({ start, end })
    // 跳过分隔符本身（含它后面的空白）
    let next = sep + 1
    while (next < text.length && /\s/.test(text[next])) next++
    start = next
  }
  if (start < text.length) bounds.push({ start, end: text.length })

  const groups: { start: number; end: number }[] = []
  let curStart = -1
  let curEnd = -1
  for (const b of bounds) {
    if (curStart < 0) {
      curStart = b.start
      curEnd = b.end
      continue
    }
    if (b.end - curStart > target) {
      groups.push({ start: curStart, end: curEnd })
      curStart = b.start
      curEnd = b.end
    } else {
      curEnd = b.end
    }
  }
  if (curStart >= 0) groups.push({ start: curStart, end: curEnd })
  return groups
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
