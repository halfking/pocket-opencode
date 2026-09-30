/**
 * 邮件正文多格式渲染（2026-10-01 需求：内容展示支持多种格式）。
 *
 * 背景：此前详情页只有两条渲染路径——HTML 走 v-html，纯文本走 <pre>。
 * 真实邮箱里还有大量**介于两者之间**的格式，全部退化成 <pre> 的一坨裸文本：
 *
 *  1. **引用原文**（回复/转发链）。`text/plain` 里的 `>` 前缀和
 *     「在 xxx 写道：」分隔线是邮件线程的标准结构，裸显示时和正文混在一起，
 *     用户分不清哪句是对方说的、哪句是历史。
 *  2. **Markdown**。越来越多通知/AI 邮件正文用 Markdown 排版，
 *     过去完全没有渲染（`-` 列表、`` ` `` 行内代码、`**加粗**` 全是原样字符）。
 *  3. **类表格的定宽文本**。账单、发票、行程确认邮件大量用空格对齐，
 *     <pre> 能保住对齐但字体/字号是正文级的，可读性差。
 *  4. **签名档与退订尾注**。占正文很大篇幅且无用。
 *
 * 本模块只做「结构识别 + 生成语义化 HTML」，**不做安全过滤**——
 * 净化仍由 email-detail-format.ts 的 DOMPurify 负责（顺序不能反）。
 * 纯字符串处理，可在 node --test 下直接测。
 */

export type BodyFormat = 'html' | 'markdown' | 'text'

/** 引用块的分隔标记：Outlook「在 X 写道：」/ Gmail「X wrote:」等。 */
const QUOTE_ATTRIBUTION = [
  /^-{2,}\s*原始邮件\s*-{2,}\s*$/gm,
  /^-{2,}\s*转发邮件\s*-{2,}\s*$/gm,
  /^在.{0,60}写道[：:]\s*$/gm,
  /^\s*On .{0,80} wrote:\s*$/gm,
  /^>{0,1}\s*-----+\s*(原始邮件|Original Message|Forwarded message)\s*-+\s*$/gm,
]

/** 常见签名档起手行。命中后到文末都算签名。 */
const SIGNATURE_MARKERS = [
  /^--\s*$/m, // RFC 3676 分隔签名 "-- "
  /^发自\s*$/m,
  /^Sent from my /im,
  /^此邮件.*发件人[：:]/m,
  /^Get\s+Outlook\s+for\s+(iOS|Android)/i,
  /^您诚挚的|^此致$/m,
]

/** 退订尾注起手。 */
const UNSUBSCRIBE_MARKERS = [
  /^unsubscribe|退订|取消订阅/im,
  /you received this email because|您收到此邮件是因为/i,
]

// ---------------------------------------------------------------------------
// 格式探测
// ---------------------------------------------------------------------------

/**
 * 判定正文格式。
 *
 * 顺序有讲究：先判 HTML，再判 Markdown，最后落纯文本。HTML 优先是因为
 * 「HTML 里含 Markdown 语法」是常态（邮件营销模板常混排 `**` ），
 * 反过来判会把真 HTML 当 Markdown 处理，把标签当普通字符显示。
 */
export function detectBodyFormat(body: string): BodyFormat {
  const s = (body || '').trim()
  if (!s) return 'text'
  if (/<(?:html|body|div|p|table|br|span|a|img|h[1-6])\b[^>]*>/i.test(s.slice(0, 2000))) {
    return 'html'
  }
  return looksLikeMarkdown(s) ? 'markdown' : 'text'
}

/**
 * Markdown 特征判定。
 *
 * 阈值取「至少命中 1 条强特征，或 2 条弱特征」：单条强特征（围栏代码块、
 * 标题行、表格分隔行）足够定性；只靠 `- ` 这种弱特征则要求更多佐证，
 * 否则普通邮件里随手一个破折号就会被当成 Markdown 列表渲染，反而更乱。
 */
export function looksLikeMarkdown(s: string): boolean {
  const text = (s || '').trim()
  if (!text) return false
  let strong = 0
  let weak = 0
  if (/^```/m.test(text)) strong++
  if (/^#{1,6}\s+\S/m.test(text)) strong++
  if (/^\s*\|.+\|\s*$/m.test(text) && /^\s*\|[\s:|-]+\|\s*$/m.test(text)) strong++
  // 弱特征按**出现次数**累计，且单项封顶 2。
  //
  // 为什么不能只按「模式命中过没有」算：`- 第一项\n- 第二项` 只有一种列表模式，
  // 按布尔计数 weak 只有 1，永远达不到阈值 2 —— 于是最典型的 Markdown（一份
  // 就是列表的通知邮件）反被判成纯文本，这正是探测要避免的漏判。
  const cap = (n: number) => Math.min(n, 2)
  weak += cap((text.match(/^\s*[-*+]\s+\S/gm) || []).length)
  weak += cap((text.match(/^\s*\d+[.)]\s+\S/gm) || []).length)
  weak += cap((text.match(/\*\*[^*\n]+\*\*/g) || []).length)
  weak += cap((text.match(/`[^`\n]+`/g) || []).length)
  weak += cap((text.match(/^\s*>\s?\S/gm) || []).length)
  return strong >= 1 || weak >= 2
}

// ---------------------------------------------------------------------------
// 引用 / 签名 / 退订 的切分
// ---------------------------------------------------------------------------

export interface BodySections {
  /** 用户真正要读的部分。 */
  main: string
  /** 引用原文（可能为空）。 */
  quoted: string
  /** 签名档（可能为空）。 */
  signature: string
  /** 退订尾注（可能为空）。 */
  footer: string
}

/** 找出引用块起点：任一引用标记首次出现的位置。 */
function findQuoteStart(lines: string[]): number {
  let best = -1
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    if (!line.trim()) continue
    for (const re of QUOTE_ATTRIBUTION) {
      re.lastIndex = 0
      if (re.test(line)) {
        if (best === -1 || i < best) best = i
        break
      }
    }
    // 连续引用行（`>` 开头）也算引用起点。
    if (/^\s*>\s?/.test(line)) {
      if (best === -1 || i < best) best = i
      break
    }
  }
  return best
}

/** 找出签名档起点（引用块之后才算，避免正文里的 "--" 误判）。 */
function findSignatureStart(lines: string[], from: number): number {
  for (let i = from; i < lines.length; i++) {
    for (const re of SIGNATURE_MARKERS) {
      re.lastIndex = 0
      if (re.test(lines[i])) return i
    }
  }
  return -1
}

/** 找出退订尾注起点。 */
function findUnsubscribeStart(lines: string[], from: number): number {
  for (let i = from; i < lines.length; i++) {
    for (const re of UNSUBSCRIBE_MARKERS) {
      re.lastIndex = 0
      if (re.test(lines[i])) return i
    }
  }
  return -1
}

/**
 * 把纯文本正文切成「正文 / 引用 / 签名 / 退订」四段。
 *
 * 为什么必须切而不能整段渲染：回复链邮件里 80% 的篇幅是历史引用，
 * 不折叠的话用户每读一封回信都要滚过一大片无关内容——这是邮件客户端
 * 「隐藏引用」选项存在的原因。
 *
 * 边界优先级：正文 < 引用 < 签名 < 退订。退订与签名都从引用之后找，
 * 且退订优先（有的邮件签名在退订之后，取更靠后的那段会把退订当签名）。
 */
export function splitBodySections(text: string): BodySections {
  const src = (text || '').replace(/\r\n/g, '\n')
  if (!src.trim()) {
    return { main: '', quoted: '', signature: '', footer: '' }
  }
  const lines = src.split('\n')
  const total = lines.length

  const quoteAt = findQuoteStart(lines)
  // 签名与退订只在「引用之后」找：正文里的 "--" 分隔线不能当签名。
  const afterQuote = quoteAt === -1 ? 0 : quoteAt
  const unsubAt = findUnsubscribeStart(lines, afterQuote)
  const sigAt = findSignatureStart(lines, afterQuote)

  /**
   * 尾注起点是否可信。
   *
   * 判据是「从该行到文末，非空段落数 ≤ 1」：真签名/退订是文末那一小坨；
   * 而正文中间的 "--" 后面通常还跟着好几段实质内容。
   *
   * 特意**不**用「占比过半」：短邮件（3~6 行）的签名天然占一半以上，
   * 按比例一刀切会把绝大多数真实签名误杀。
   */
  const tailIsPlausible = (at: number): boolean => {
    if (at < 0 || at > total - 2) return false
    let paragraphs = 0
    for (let i = at; i < total; i++) {
      if (!lines[i].trim()) continue
      // 前导空行不算新段落
      if (i === at || !lines[i - 1].trim()) paragraphs++
      if (paragraphs > 1) return false
    }
    return true
  }

  // 引用与尾注的先后：签名常在退订之前，取各自独立的可信起点。
  const sigOk = sigAt >= afterQuote && tailIsPlausible(sigAt)
  const unsubOk = unsubAt >= afterQuote && tailIsPlausible(unsubAt)
  const sigStart = sigOk ? sigAt : -1
  const unsubStart = unsubOk ? unsubAt : -1

  /** 正文终点：优先截在引用前，其次截在尾注前。 */
  const tailStart = [sigStart, unsubStart].filter((i) => i >= 0).sort((a, b) => a - b)[0] ?? -1
  const mainEnd = quoteAt >= 0 ? quoteAt : tailStart >= 0 ? tailStart : total
  const quotedEnd = tailStart >= 0 ? tailStart : total

  // 引用块到 tail 之前；签名/退订按各自起点顺序切。
  const quoted = quoteAt >= 0 ? lines.slice(quoteAt, quotedEnd).join('\n').trim() : ''
  // 签名从 sigStart 到（更靠后的）unsubStart 为止；退订从 unsubStart 到文末。
  const signature =
    sigStart >= 0
      ? lines.slice(sigStart, unsubStart > sigStart ? unsubStart : total).join('\n').trim()
      : ''
  const footer = unsubStart >= 0 ? lines.slice(unsubStart).join('\n').trim() : ''

  return {
    main: lines.slice(0, mainEnd).join('\n').trim(),
    quoted,
    signature,
    footer,
  }
}

// ---------------------------------------------------------------------------
// 渲染
// ---------------------------------------------------------------------------

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

/** 裸 URL 转 <a>。要求两侧是非 URL 字符，避免把已存在的 href 再套一层。 */
function linkify(text: string): string {
  return text.replace(
    /(^|[\s(<])((?:https?:\/\/|www\.)[^\s<>()]+[^\s<>().,;:!?])/gi,
    (_m, pre: string, url: string) => {
      const href = url.startsWith('http') ? url : `https://${url}`
      return `${pre}<a href="${escapeHtml(href)}" rel="noopener noreferrer">${escapeHtml(url)}</a>`
    },
  )
}

/**
 * 纯文本 → 语义化 HTML。
 *
 * 关键点：
 *  - 段落按空行切，**不按单换行**。邮件里单换行通常是软排版换行
 *    （quoted-printable 会按 76 列硬折），按单换行切会把一句话拆成两段。
 *  - 行首连续空格缩进视为代码块，保住对齐。
 *  - URL 自动成链。转义在最后统一做，避免先转义后链接匹配不到 `://`。
 */
export function renderTextBody(text: string): string {
  const src = (text || '').replace(/\r\n/g, '\n').replace(/\t/g, '    ')
  if (!src.trim()) return ''
  const blocks: string[] = []
  for (const raw of src.split(/\n{2,}/)) {
    const block = raw.replace(/\s+$/, '')
    if (!block.trim()) continue
    const lines = block.split('\n')
    // 整体缩进 → 代码块（发票/日志类邮件靠空格对齐信息）。
    if (lines.every((l) => !l.trim() || /^\s{4,}\S/.test(l))) {
      blocks.push(`<pre>${escapeHtml(block)}</pre>`)
      continue
    }
    // 「•」「-」「数字.」开头的行 → 列表。
    const listItems = lines.filter((l) => /^\s*(?:[-*•‣▪]|\d+[.)])\s+\S/.test(l))
    if (listItems.length && listItems.length >= Math.ceil(lines.length / 2)) {
      const ordered = /^\s*\d+[.)]\s+\S/.test(lines.find((l) => l.trim()) || '')
      const tag = ordered ? 'ol' : 'ul'
      const items = listItems
        .map((l) => `<li>${linkify(escapeHtml(l.replace(/^\s*(?:[-*•‣▪]|\d+[.)])\s+/, '')))}</li>`)
        .join('')
      blocks.push(`<${tag}>${items}</${tag}>`)
      continue
    }
    blocks.push(`<p>${linkify(escapeHtml(block)).replace(/\n/g, '<br>')}</p>`)
  }
  return blocks.join('')
}

/**
 * 极简 Markdown → HTML。
 *
 * **刻意不引第三方 markdown 库**：邮件正文是不可信输入，完整的 CommonMark
 * 解析器会引入大量内联 HTML/raw-HTML 逃逸面，而这里只需要标题、列表、
 * 强调、行内代码、链接、围栏代码这几种（探测阶段也只按这些特征判定）。
 * 自己写反而能把输出面收窄到「全部转义后再加有限标签」。
 */
export function renderMarkdownBody(md: string): string {
  const src = (md || '').replace(/\r\n/g, '\n')
  if (!src.trim()) return ''
  const out: string[] = []
  const lines = src.split('\n')
  let i = 0

  const inline = (t: string): string =>
    // 顺序要紧：代码片段先抽出来，否则 `**` 会被强调规则吃掉。
    t
      .split(/(`[^`\n]+`)/g)
      .map((seg, idx) => {
        if (idx % 2 === 1) return `<code>${escapeHtml(seg.slice(1, -1))}</code>`
        let s = escapeHtml(seg)
        s = s.replace(/\[([^\]\n]+)\]\((https?:\/\/[^)\s]+)\)/g,
          (_m, label: string, href: string) =>
            `<a href="${escapeHtml(href)}" rel="noopener noreferrer">${label}</a>`)
        s = s.replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>')
        s = s.replace(/(^|[^*])\*([^*\n]+)\*(?!\*)/g, '$1<em>$2</em>')
        return s
      })
      .join('')

  while (i < lines.length) {
    const line = lines[i]
    // 围栏代码块
    if (/^```/.test(line)) {
      const buf: string[] = []
      i++
      while (i < lines.length && !/^```/.test(lines[i])) buf.push(lines[i++])
      i++ // 吃掉收尾 ```
      out.push(`<pre><code>${escapeHtml(buf.join('\n'))}</code></pre>`)
      continue
    }
    // ATX 标题
    const h = /^(#{1,6})\s+(.*)$/.exec(line)
    if (h) {
      const level = h[1].length
      out.push(`<h${level}>${inline(h[2])}</h${level}>`)
      i++
      continue
    }
    // 水平线
    if (/^\s*([-*_])\s*(\1\s*){2,}$/.test(line)) {
      out.push('<hr>')
      i++
      continue
    }
    // 表格：表头 + 分隔行 + 若干数据行
    if (/^\s*\|.*\|\s*$/.test(line) && /^\s*\|[\s:|-]+\|\s*$/.test(lines[i + 1] || '')) {
      const rows: string[] = []
      const cells = (r: string) => r.trim().replace(/^\||\|$/g, '').split('|').map((c) => c.trim())
      const head = cells(line)
      i += 2
      while (i < lines.length && /^\s*\|.*\|\s*$/.test(lines[i])) rows.push(lines[i++])
      const th = head.map((c) => `<th>${inline(c)}</th>`).join('')
      const tb = rows
        .map((r) => `<tr>${cells(r).map((c) => `<td>${inline(c)}</td>`).join('')}</tr>`)
        .join('')
      out.push(`<table><thead><tr>${th}</tr></thead><tbody>${tb}</tbody></table>`)
      continue
    }
    // 无序 / 有序列表
    if (/^\s*(?:[-*+]|\d+[.)])\s+\S/.test(line)) {
      const ordered = /^\s*\d+[.)]\s+/.test(line)
      const tag = ordered ? 'ol' : 'ul'
      const items: string[] = []
      while (i < lines.length && /^\s*(?:[-*+]|\d+[.)])\s+\S/.test(lines[i])) {
        items.push(`<li>${inline(lines[i].replace(/^\s*(?:[-*+]|\d+[.)])\s+/, ''))}</li>`)
        i++
      }
      out.push(`<${tag}>${items.join('')}</${tag}>`)
      continue
    }
    // 引用块
    if (/^\s*>\s?/.test(line)) {
      const buf: string[] = []
      while (i < lines.length && /^\s*>\s?/.test(lines[i])) {
        buf.push(lines[i].replace(/^\s*>\s?/, ''))
        i++
      }
      out.push(`<blockquote>${renderMarkdownBody(buf.join('\n'))}</blockquote>`)
      continue
    }
    // 段落
    if (!line.trim()) {
      i++
      continue
    }
    const para: string[] = []
    while (i < lines.length && lines[i].trim() && !/^\s*(?:[-*+]|\d+[.)])\s+\S/.test(lines[i]) && !/^#{1,6}\s/.test(lines[i]) && !/^```/.test(lines[i]) && !/^\s*>\s?/.test(lines[i])) {
      para.push(lines[i++])
    }
    out.push(`<p>${inline(para.join('\n')).replace(/\n/g, '<br>')}</p>`)
  }
  return out.join('')
}

/** 按格式分派到对应渲染器。 */
export function renderBodyByFormat(body: string, format?: BodyFormat): string {
  const f = format ?? detectBodyFormat(body)
  if (f === 'html') return body
  if (f === 'markdown') return renderMarkdownBody(body)
  return renderTextBody(body)
}

/**
 * 剥掉引用行的 `>` 前缀，得到「干净的引用文本」。
 * 用于翻译时只翻正文（引用翻不翻都无所谓，且占 token）。
 */
export function stripQuoteMarkers(text: string): string {
  return (text || '')
    .split('\n')
    .map((l) => l.replace(/^\s*>\s?/, ''))
    .join('\n')
}
