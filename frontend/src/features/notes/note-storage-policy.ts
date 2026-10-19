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
 * §898：单次 `/api/embed` 的长度上限（与服务端硬编码一致）。
 * ⚠ 出处：`backend/internal/server/server_assistant.go:2853`
 *   `if len(body.Text) > 16000 { 400 "text too long (max 16000 bytes)" }`
 * ⚠⚠ **单位是 UTF-8 字节，不是字符**（§916 实测：Go 的 `len(string)`）⇒
 *   16000 字节只装得下 **5333 个中文字**。
 *   ⚠ 这个常量**按码元**在用（`text.length` / `text.slice`）⇒ 切段是否
 *     真的按字节做，是另一件事 —— 见 §917。
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
 * 按「行」分组：累积单元直到接近 `target` 字符。
 *
 * ⚠⚠⚠ §900：**锚点是任意换行（`\\n+`），不是空行（`\\n\\s*\\n`）。**
 *   §899 用的是空行锚点，它的 95% 复用率是在**合成的、用 `\n\n` 分段的语料**上量的。
 *   而**真实会议转写一个空行都没有** —— 实测 `.artifacts/asr-s788/seg-asr/merged.txt`
 *   （一场真实会议的合并转写，3392 字 / 52 行）：
 *     单 `\n` 出现 **52** 次，`\n\n` 出现 **0** 次，**空行率 0%**；
 *   每行就是一个 ASR 分段（中位 66 字）。
 *   ⇒ 空行锚点在真实数据上**一个边界都找不到** ⇒ 退化成「整块按大小切」
 *   ⇒ 实测 20352 字真实转写改一个字：空行锚定 **0/2 段存活（需重嵌 2 次）**，
 *     单换行锚定 **5/6 段存活（需重嵌 1 次）**。
 *   ⇒ 「自然单元」对转写文本而言是**行**，不是空行。
 *
 * ⚠⚠ **返回的是原文里的真实偏移**，组的文本就是 `text.slice(start, end)`
 *   —— 不重新拼接分隔符。
 *   ⇒ §899 第一版用 `split` + `join('\n\n')` 重建，于是：
 *     · 原文分隔符是 `'\n \n'`（3 字符）时，重建变成 `'\n\n'`（2 字符）
 *       ⇒ 段的文本与原文**不再逐字相同**，且累积偏移会**漂移**；
 *     ⇒ 段哈希因此与原文对不上，增量重嵌的「未变段」判定会失准。
 *   ⇒ 现在直接记录原文里的区间。
 *
 * ⚠ **完全没有换行的文本**（一整块）⇒ 退化成 1 组 ⇒ 由调用方按大小再切。
 */
function groupByParagraph(text: string, target: number): { start: number; end: number }[] {
  // ⚠⚠⚠ §901：**按可用结构逐级退化** —— 先按「行」，没有行就按「句号」。
  //
  //   为什么要有第二级：两条转写链的**行结构不同**，实测：
  //     · 会议链 `renderTranscript`（meeting-dedup.ts:429）⇒ `[说话人] 文本` 每段一行、\`\n\` 拼接 ✔
  //     · 随手记增量链 `appendTranscript`（note-recording.ts:74）
  //       ⇒ `${committed}${finalChunk}`，**零分隔符直接拼接** ⇒ 整份是**一整行** ⛔
  //   ⇒ 只认「行」的锚点在随手记链上**一个边界都找不到**：
  //     实测（零换行的真实转写 20046 字、798 个句号）改两个词：
  //       行锚点 2 段，**0/2 段存活** ⇒ 增量重嵌退化
  //       句号锚点 6 段，**5/6 段存活** ⇒ 照常生效
  //
  //   优先级「行优先」的理由：转写的自然单元是 **ASR 分段**，而分段边界就是行。
  //   句号是行内更细的结构，只有在没有行时才用。
  let bounds = splitOnBounds(text, /\n+/)
  if (bounds.length < 2) bounds = splitOnBounds(text, /[。！？!?]+[”"』」）)]*/)
  return accumulateToTarget(bounds, text, target)
}

/** 按 `re` 把文本切成若干区间；分隔符本身不进任何区间。 */
function splitOnBounds(text: string, re: RegExp): { start: number; end: number }[] {
  const bounds: { start: number; end: number }[] = []
  const scan = new RegExp(re.source, re.flags.includes('g') ? re.flags : re.flags + 'g')
  let m: RegExpExecArray | null
  let start = 0
  while ((m = scan.exec(text)) !== null) {
    // ⚠ 零宽匹配会让这个循环永远不推进 ⇒ 直接跳过（两个正则都不会产生零宽，
    //   但这里必须防御：判据的变异里出现过同形的写法）。
    if (m[0].length === 0) {
      scan.lastIndex++
      continue
    }
    if (m.index > start) bounds.push({ start, end: m.index })
    start = m.index + m[0].length
  }
  if (start < text.length) bounds.push({ start, end: text.length })
  return bounds
}

/** 把若干小单元累积成约 `target` 字符的大组（**保持原文偏移**，不重建文本）。 */
function accumulateToTarget(
  bounds: { start: number; end: number }[],
  text: string,
  target: number,
): { start: number; end: number }[] {
  const groups: { start: number; end: number }[] = []
  let cs = -1
  let ce = -1
  for (const b of bounds) {
    if (cs < 0) {
      cs = b.start
      ce = b.end
      continue
    }
    if (b.end - cs > target) {
      groups.push({ start: cs, end: ce })
      cs = b.start
      ce = b.end
    } else {
      ce = b.end
    }
  }
  if (cs >= 0) groups.push({ start: cs, end: ce })
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
