/** 邮件正文展示与转发草稿：识别 HTML、引用原文、解析收件人。 */
import { decodePartBytes, isLegacyCharset } from './email-charset.ts'

const CAT: Record<string, string> = {
  work: '工作',
  bill: '账单',
  notification: '通知',
  personal: '私人',
  marketing: '广告',
  spam: '垃圾',
}

export function formatEmailDate(ms: number): string {
  const d = new Date(ms)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

export function emailCatLabel(c: string | null | undefined): string {
  if (!c) return ''
  return CAT[c] || c
}

export function looksLikeHtml(s: string): boolean {
  const head = (s || '').trim().slice(0, 2000)
  if (!head) return false
  if (/<[a-z][\s\S]*>/i.test(head) === false) return false
  // 「hello < world」这种比较符号不算 HTML。
  return /<\/?[a-z][a-z0-9]*\b[^>]*>/i.test(head)
}

export function quotedForwardBody(input: {
  from: string
  date: string
  subject: string
  body: string
}): string {
  return [
    '',
    '---------- 转发邮件 ----------',
    `发件人: ${input.from}`,
    `日期: ${input.date}`,
    `主题: ${input.subject}`,
    '',
    input.body,
  ].join('\n')
}

export function parseForwardRecipients(raw: string): string[] {
  return raw
    .split(/[,;，；\s]+/)
    .map((s) => s.trim())
    .filter((s) => s.includes('@'))
}

// ---------------------------------------------------------------------------
// MIME 解析
//
// 2026-09-30 真机审计重写。此前用一条正则直接去捞 `Content-Type: text/html`
// 之后的段落，有两个必然踩中的缺陷：
//   1. **换行符一变成 LF 就整体错位**。那条正则要求「头/体」之间正好是
//      CRLFCRLF；遇到纯 LF 的报文时头部会一路吞到下一个空行为止，结果把
//      **别的部件的 base64 载荷当正文返回**（实测返回过 `%PDF-1.4`）——
//      也就是详情页「内容缺失/串行」的来源之一。
//   2. 完全不认识 multipart/* 的部件树，既拿不准层级，也就无从建立
//      Content-ID 索引去还原 cid: 内联图。
// 现改为真正的边界感分割：按父级 boundary 递归切分，头/体以「第一处空行」
// 为界，CRLF 与 LF 一视同仁。
// ---------------------------------------------------------------------------

interface MimePart {
  contentType: string
  contentId: string
  encoding: string
  /** Content-Type 里声明的 charset；8bit 部件必须按它解码，否则中文乱码。 */
  charset: string
  body: string
}

/** 单张内联图上限；超限保留 cid:（渲染为裂图也优于让整封 HTML 撑爆内存）。 */
const MAX_INLINE_IMAGE_BYTES = 1_500_000
/** 整封邮件内联图总量上限，避免 base64 膨胀 33% 后撑爆 WebView 内存。 */
const MAX_TOTAL_INLINE_BYTES = 6_000_000

/** 头/体分界：第一个空行。CRLF 与 LF 都认。 */
const BLANK_LINE = /\r?\n\r?\n/

/** 从 IMAP BODY[] / RFC 5322 抽出可读正文；优先 HTML。 */
export function extractEmailBody(raw: string): string {
  const src = (raw || '').trim()
  if (!src) return ''
  // 两条 fail-open，出口都必须剥首部：
  // 「不是 MIME」与「解析不出部件」都可能是「一封没有 MIME 头的报文」，
  // 直接 return src 就会把 From/Subject/Date 当正文渲染（见 stripMessageHeader）。
  if (!looksLikeMime(src)) return stripMessageHeader(src)
  const parts = splitMimeParts(src)
  if (!parts.length) return stripMessageHeader(src)

  const html = parts.find((p) => p.contentType === 'text/html')
  if (html) {
    const decoded = decodeHtmlPart(html)
    return resolveCidImages(decoded, parts)
  }
  const text = parts.find((p) => p.contentType === 'text/plain')
  if (text) return decodeTransfer(text.body, text.encoding, text.charset)
  // 部件树里只有附件（image/* / application/*）时，这里原本 `return src`，
  // 与上面两个出口同样的毛病：把协议头 + 附件载荷当正文。剥掉首部，
  // 剩下的空正文由调用方按「无正文」处理，比展示一屏 Received: 强。
  return stripMessageHeader(src)
}

/**
 * HTML 部件解码：先按头部 charset 解一版，再看文档内的 `<meta charset>`。
 *
 * 为什么必须两遍：`<meta charset="gb2312">` 本身是被编码字节的一部分，只有先解开
 * 才读得到它。老系统极常见「头写 utf-8 + 文档内写 GB2312」，此时必须用**原始字节**
 * 按 gb2312 重解一遍——拿已解码的字符串是救不回来的（U+FFFD 无法反解）。
 */
function decodeHtmlPart(part: MimePart): string {
  const first = decodeTransfer(part.body, part.encoding, part.charset)
  const meta = metaCharsetOf(first)
  if (!meta) return first
  // 头部没说、或说的是 UTF-8，而文档内声明了别的 → 以文档内为准，用字节重解。
  const headerEnc = part.charset.toLowerCase()
  const shouldRedecode = !headerEnc || headerEnc === 'utf-8' || headerEnc === 'us-ascii'
  if (!shouldRedecode) return first
  const red = decodeTransfer(part.body, part.encoding, meta)
  // 只有确实更干净才采用，避免把本来正确的正文换坏。
  return replacementCount(red) < replacementCount(first) ? red : first
}

function replacementCount(s: string): number {
  return (s.match(/�/g) || []).length
}

/** 提取 HTML 文档内声明的 charset（`<meta charset>` 或 http-equiv 形式）。 */
function metaCharsetOf(html: string): string {
  const m =
    /<meta[^>]+charset\s*=\s*["']?\s*([a-zA-Z0-9_\-:]+)/i.exec(html) ||
    /<meta[^>]+content\s*=\s*["'][^"']*charset\s*=\s*([a-zA-Z0-9_\-:]+)/i.exec(html)
  return m?.[1] || ''
}

/**
 * 这封报文是不是 MIME（多部件 / 有 MIME 头）。
 *
 * 2026-10-03 真机实测的缺陷就在这个窗口上：原来只在**前 4000 字符**里
 * 找 Content-Type / Content-Transfer-Encoding。经 Gmail + Coremail 转发的
 * 邮件前面堆了 40 组 Received / ARC-Seal / DKIM-Signature，Content-Type
 * 被推到 4000 字符之外，于是整封被判成「不是 MIME」，解析整条跳过——
 * 详情页于是把协议头当正文渲染：首屏全是
 * `Received: from mail-yx2-f41.google.com (unknown [74.125.224.169])`
 * 与 `ARC-Seal: ...`，真实正文一屏都看不到（实测正文长 95869 字符）。
 *
 * 修法不是把窗口调大（猜一个更大的数，下次照样被顶出去），而是
 * **按结构找头段**：RFC 5322 里头部是第一个空行之前的内容，Content-Type
 * 无论堆多少个 Received 都必然落在里面。取 256KB 上限是为了给畸形报文
 * 一个硬边界，不是「预计头部长度」。
 */
const MIME_HEAD_SCAN_LIMIT = 256 * 1024

/**
 * RFC 5322 的**核心**头字段名。用来判断「这是一封报文」而不是「一段正文」。
 *
 * 为什么不直接看「第一行像不像头字段行」：一封纯文本邮件的正文完全可以
 * 写成 `Note: 已确认\r\n\r\n明天见` 这种形态，按「首行是 field-name」判就会把
 * 正文的第一段剥掉。而 From / Subject / Date 这类字段名在正文里出现得极少，
 * 拿已知字段名当锚点，误判面小一个数量级。
 */
const RFC5322_CORE_HEADER = /^(?:from|to|cc|bcc|sender|reply-to|subject|date|message-id|in-reply-to|references|return-path|received|mime-version|content-type|content-transfer-encoding|content-id|content-disposition)\s*:/i

/**
 * 判断输入是不是一封 RFC 5322 报文（首部字段行 + 第一个空行 + 正文）。
 *
 * ## 为什么要单独判
 *
 * 「不是 MIME」不等于「整段都是正文」。一封**没有 Content-Type** 的老式报文
 * （部分老网关、老脚本发送方会省掉）同样是报文，它的 From / Subject / Date
 * 属于协议头，不是正文。
 *
 * 2026-10-03 实测（与 54d2fc51 同一个缺陷类的另一个入口条件）：
 * `extractEmailBody` 对这样一封邮件的 `looksLikeMime` 返回 false，于是走
 * `return src` 把整段原文当正文返回——详情页首屏是
 * `From: zhang@example.com` / `Subject: …` / `Date: …`。
 *
 * 上一轮修的是「头太长把 Content-Type 顶出窗口」，这一条是「压根没有 MIME
 * 头」；两个入口都通向同一个 fail-open：`return src`。
 */
function looksLikeRFC5322Message(s: string): boolean {
  const { headers, body } = splitHeadBody(s)
  if (!body.trim()) return false
  return headers.split(/\r?\n/).some((line) => RFC5322_CORE_HEADER.test(line.trim()))
}

/**
 * 把报文的首部剥掉，只留正文；不是报文就原样返回。
 *
 * 这是所有「判定失败」的出口都必须过的那一道闸——**判定失败时的返回值本身
 * 也要当成判据来审**。`return src` 看起来是「保守地什么都不做」，实际上
 * 把协议头原样交给用户，比判错更难看。
 */
function stripMessageHeader(s: string): string {
  if (!looksLikeRFC5322Message(s)) return s
  const stripped = splitHeadBody(s).body
  // 剥掉顶层首部后，剩下的往往还是**一层 multipart 包裹**：边界行 + 子部件头。
  //
  // 2026-10-03 实测（只有附件的邮件）：剥完首部拿到的仍是
  //   --B2 / Content-Type: application/pdf ... / Content-Transfer-Encoding: base64 ...
  // 直接当正文渲染，用户看到的是一屏 MIME 语法而不是「这封信只有附件」。
  // 所以这里再剥一层：剥掉**成对的**边界行（首行开边界、末行 `--X--` 结束边界），
  // 并丢掉每个子部件的头部。
  //
  // 之所以在剥完首部**之后**才认边界：只有到这里才知道它真是 MIME 包裹，
  // 而不是在猜正文的开头是不是一条 `---` 分隔线（签名档、Markdown 水平线）。
  return stripMultipartWrapper(stripped)
}

/**
 * 剥掉 multipart 包裹：去边界行与子部件头，保留子部件体。
 *
 * 只在首行是边界**且**能找到对应结束边界时才动手（`cuts.length >= 2`），
 * 否则原样返回——「看起来像 MIME 但切不开」比「不动」更危险。
 */
function stripMultipartWrapper(body: string): string {
  // trimStart 会一并吃掉 U+FEFF / BOM，不需要额外 replace。
  const trimmed = body.trimStart()
  const openMatch = /^(--[\w'+=.-]+)[ \t]*\r?\n/.exec(trimmed)
  if (!openMatch) return body
  const b = openMatch[1]
  const marker = new RegExp(`^[ \\t]*${escapeRe(b)}[ \\t]*(--)?[ \\t]*(?=\\r?\\n|$)`, 'gm')
  const cuts: Array<{ start: number; end: number; closing: boolean }> = []
  let m: RegExpExecArray | null
  while ((m = marker.exec(trimmed)) !== null) {
    cuts.push({ start: m.index, end: m.index + m[0].length, closing: !!m[1] })
    if (m[1]) break
  }
  if (cuts.length < 2) return body
  const out: string[] = []
  for (let i = 0; i < cuts.length - 1; i++) {
    const start = cuts[i].end + 1
    const end = cuts[i + 1].start
    if (end <= start) continue
    out.push(splitHeadBody(trimmed.slice(start, end)).body)
  }
  return out.join('\n').trim()
}

function looksLikeMime(s: string): boolean {
  // 结构判据：头部止于第一个空行（CRLF 与 LF 都认）。
  const head = splitHeadBody(s.slice(0, MIME_HEAD_SCAN_LIMIT)).headers
  // 字段名行首锚定，理由见 headerField。这里判 true 之后 walkMime 会用**同一条**
  // 规则去读，若这里用松散正则就会造出「判定为 MIME、却读不出任何部件」的
  // 状态——那种状态最终会落到 return src，把整封协议头当正文。
  if (headerField(head, 'content-type') || headerField(head, 'content-transfer-encoding')) return true
  if (/^mime-version[ \t]*:/im.test(head)) return true
  // 非 multipart 的单部件 MIME 也可能只有边界式首行。
  //
  // 判据是「**首行**就是边界」，所以锚在字符串开头即可，**不设字符窗口**。
  // 原来写的是 `/^--[\w'+=.-]+/m.test(s.slice(0, 200))`：窗口是当年随手挑的
  // 一个「够用」数，作用是让失败静默——边界真在 200 字符之后时判据恒假，
  // 而报出来的现象是「不是 MIME」，指向完全错误的方向。
  // 去掉 `/m` 同时避免了另一种误判：正文里的分隔线（`---` 签名档、
  // Markdown 的水平线）会让整段正文被判成 MIME。
  return /^--[\w'+=.-]+/.test(s)
}

/** 递归解析 MIME 树，返回叶子部件（text/*、image/* 等实际内容）。 */
function splitMimeParts(raw: string): MimePart[] {
  const out: MimePart[] = []
  walkMime(raw, out, 0)
  return out
}

function walkMime(raw: string, out: MimePart[], depth: number): void {
  // 防御畸形/超深嵌套邮件导致的栈溢出
  if (depth > 6) return
  const { headers, body } = splitHeadBody(raw)
  const contentType = headerField(headers, 'content-type')?.[1]?.split(';')[0]?.trim().toLowerCase() || ''
  const encoding = (headerField(headers, 'content-transfer-encoding')?.[1] || '').trim()
  const contentId = normalizeCid(headerField(headers, 'content-id')?.[1] || '')
  const charset = headerCharset(headers)

  if (contentType.startsWith('multipart/')) {
    const boundary = /boundary\s*=\s*"?([^";\r\n]+)"?/i.exec(headers)?.[1]?.trim()
    if (!boundary) return
    for (const chunk of splitByBoundary(body, boundary)) {
      walkMime(chunk, out, depth + 1)
    }
    return
  }
  if (!contentType) return
  out.push({ contentType, contentId, encoding, charset, body })
}

/**
 * 读一个头字段，**只认行首的字段名**。
 *
 * ## 为什么必须是行首锚定（2026-10-03 真实数据实测，49 封里 23 封命中）
 *
 * 原来用的是 `/content-type\s*:\s*([^\s;]+)/i` —— 不锚行首，且 `\s*` 容忍
 * 字段名与冒号之间的空格。QQ 的 `X-QQ-XMRINFO` 追踪头正好把一堆字段名**当作
 * 值**写在一行里（实测原文）：
 *
 *   h=date : from : reply-to : to : message-id : subject : mime-version :
 *   content-type : list-unsubscribe : from : ...
 *
 * 于是正则抓到的是 `list-unsubscribe`（甚至更长的
 * `date:from:mime-version:message-id:subject:to`），顶层 MIME 类型被读成
 * `list-unsubscribe` / `reply-to` 这类垃圾值 —— `startsWith('multipart/')`
 * 不成立，`walkMime` 于是**不递归切分**，一个 text 部件都找不到。
 *
 * 后果不是「正文为空」而是更糟的形态：`extractEmailBody` 落到末尾的
 * `return src`，把整封报文原样交给详情页。于是首屏是
 * `Received: from ptr2.edm.infoq.com.cn ...` 和 QQ 追踪头里成百行的
 * base64 噪声，正文则是一整片未解码的 `=E7=82=B9=E5=87=BB` QP 转义
 * （实测最严重一封 94364 字节的报文，正文 94356 字符里 4115 处 `=XX`），
 * 完全无法阅读。这正是「邮件详情里有大量原始字节」的成因。
 *
 * ## 判据为什么是「行首」而不是「更严格的字段名白名单」
 *
 * RFC 5322 的 field-name 语法本就不允许空格，`content-type :` 根本不是合法
 * 字段名——**行首锚定是协议本身给出的判据**，不需要维护一张头字段白名单
 * （真实邮件里 `X-*` / `ARC-*` / `List-*` 头多到写不完，白名单必然漏）。
 * 附带好处：顶层解析和 `walkMime` 用的是同一条规则，不会出现
 * 「外层认得出、内层读不出」的自相矛盾。
 *
 * 折叠行（RFC 5322 §2.2.3，以空白开头的续行）仍被 `splitHeadBody` 原样保留在
 * headers 里；本函数只锚定**字段名所在行的行首**，续行内容不会被误认成新字段。
 */
function headerField(headers: string, name: string): RegExpExecArray | null {
  // 字段名只允许 ASCII 可见字符且不含冒号；`m` 让 ^ 锚定每一行的行首。
  return new RegExp(`^${name}[ \\t]*:[ \\t]*([^\\r\\n]*)`, 'im').exec(headers)
}

/**
 * 头部里的 charset。优先取 Content-Type 的 `charset=` 参数——正文部件的权威声明
 * 就写在这里；某些客户端还会额外发一个裸 `charset=` 头，一并兜住。
 */
function headerCharset(headers: string): string {
  const ct = headerField(headers, 'content-type')?.[1] || ''
  const fromType = /charset\s*=\s*"?([^";\s]+)"?/i.exec(ct)?.[1] || ''
  if (fromType) return fromType
  // 裸 charset 头同样只认行首（理由见 headerField）。
  return /^charset[ \t]*=[ \t]*"?([^";\s]+)"?/im.exec(headers)?.[1] || ''
}

/** 以第一处空行为界拆头/体。 */
function splitHeadBody(raw: string): { headers: string; body: string } {
  const m = BLANK_LINE.exec(raw)
  if (!m || m.index === undefined) return { headers: raw, body: '' }
  return { headers: raw.slice(0, m.index), body: raw.slice(m.index + m[0].length) }
}

/**
 * 按 boundary 切分子部件。边界行形如 `--B` 或 `--B--`（结束标记）；
 * 内容从边界行之后开始，到下一个边界行之前结束，两侧换行一并剥掉。
 */
function splitByBoundary(body: string, boundary: string): string[] {
  const marker = new RegExp(`^[ \\t]*--${escapeRe(boundary)}[ \\t]*(--)?[ \\t]*(?=\\r?\\n|$)`, 'gm')
  const cuts: Array<{ start: number; end: number; closing: boolean }> = []
  let m: RegExpExecArray | null
  while ((m = marker.exec(body)) !== null) {
    cuts.push({ start: m.index, end: m.index + m[0].length, closing: !!m[1] })
    if (m[1]) break // 结束边界之后没有内容了
  }
  if (cuts.length < 2) return []
  const parts: string[] = []
  for (let i = 0; i < cuts.length - 1; i++) {
    const start = cuts[i].end + 1
    const end = cuts[i + 1].start
    if (end <= start) continue
    parts.push(body.slice(start, end))
  }
  return parts
}

function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

/** Content-ID 头部可能带 RFC 2047 编码尾巴；只取 <> 内并统一小写。 */
function normalizeCid(raw: string): string {
  const angled = /<([^>]*)>/.exec(raw)
  const value = (angled ? angled[1] : raw).trim().replace(/^"|"$/g, '')
  if (!value) return ''
  // 少数客户端把 @ / = 写成 =40 / =3D（QP 习惯）
  return value.replace(/=40/g, '@').replace(/=3D/g, '=').toLowerCase()
}

function decodeTransfer(body: string, encoding: string, charset: string): string {
  const enc = encoding.toLowerCase()
  if (enc === 'base64') {
    const compact = body.replace(/\s+/g, '')
    if (!compact) return ''
    try {
      // 按声明的 charset 解字节，而不是一律 UTF-8 —— GBK 正文写死 utf-8 就是乱码。
      return decodePartBytes(base64ToBytes(compact), charset).trim()
    } catch {
      return body.trim()
    }
  }
  if (enc === 'quoted-printable') return decodeQuotedPrintable(body, charset).trim()
  // 8bit / 7bit / binary。
  //
  // 注意：这里的 body 已经是 **JS 字符串**（服务端把 raw 当 string 走 JSON 传过来的），
  // 所以绝大多数情况下它就是可读文本，直接返回即可。绝不能无条件 `charCodeAt & 0xff`
  // —— 那会把已经是合法 UTF-16 的中文砍成半个字节，正是把「HTML 正文」变成
  // 「HTML c\xef」的元凶。
  //
  // 只有一种情况需要还原字节：声明了历史编码（GBK 等）**且**字符串里每个码位都
  // ≤ 0xFF —— 说明它确实是「一字节一字符」地穿过 JSON 的原始 GBK 字节，
  // 这时才用 latin1 取回字节再按声明解码。
  if (isLegacyCharset(charset) && isSingleByteString(body)) {
    return decodePartBytes(latin1ToBytes(body), charset).trim()
  }
  return body.trim()
}

function isSingleByteString(s: string): boolean {
  for (let i = 0; i < s.length; i++) {
    if (s.charCodeAt(i) > 0xff) return false
  }
  return true
}

/**
 * header+body 是从 JS 字符串切出来的，8bit 正文里的非 ASCII 字节已经被
 * UTF-8 重新编码过一次。这里用 latin1 逐字符取回原始字节值，
 * 再交给 decodePartBytes 按真实 charset 解释。
 *
 * 说明：这条路径在「服务端已把 raw 当 string 传输」的前提下是正确的——
 * 字节值可逆。若改走 base64 传输原始字节，可省掉这一步（见 extractEmailBodyBase64）。
 */
function latin1ToBytes(s: string): Uint8Array {
  const out = new Uint8Array(s.length)
  for (let i = 0; i < s.length; i++) out[i] = s.charCodeAt(i) & 0xff
  return out
}

function base64ToBytes(compact: string): Uint8Array {
  const NodeBuffer = (globalThis as { Buffer?: { from(s: string, enc: string): Uint8Array } }).Buffer
  if (NodeBuffer) {
    const b = NodeBuffer.from(compact, 'base64')
    return new Uint8Array(b.buffer, b.byteOffset, b.byteLength)
  }
  const bin = atob(compact)
  const out = new Uint8Array(bin.length)
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i)
  return out
}

function decodeQuotedPrintable(s: string, charset: string): string {
  const merged = s.replace(/=\r?\n/g, '')
  const bytes: number[] = []
  let i = 0
  while (i < merged.length) {
    if (merged[i] === '=' && /^[0-9A-Fa-f]{2}/.test(merged.slice(i + 1, i + 3))) {
      bytes.push(parseInt(merged.slice(i + 1, i + 3), 16))
      i += 3
      continue
    }
    // 字面字符：若码位 ≤ 0x8bit 就是「一字节一字符」的原始字节，直接收下；
    // 否则说明它已经是合法的多字节文本（QP 允许直接混排 UTF-8），
    // 按 UTF-8 展开——绝不能 `& 0xff`，那会把中文腰斩成半个字节。
    const code = merged.charCodeAt(i)
    if (code <= 0xff) {
      bytes.push(code)
    } else {
      for (const b of new TextEncoder().encode(merged[i])) bytes.push(b)
    }
    i += 1
  }
  return decodePartBytes(new Uint8Array(bytes), charset)
}

// ---------------------------------------------------------------------------
// cid: 内联图片解析（2026-09-30 真机审计 P0）
//
// 邮件详情「缺图」的根因：HTML 正文里的内联图片一律写成
//   <img src="cid:image001.png@01D8...">
// cid 是 MIME 部件的 Content-ID，**只在邮件 MIME 树里有意义**。把
// text/html 单独抽出来丢进 WebView 后，cid: 对浏览器就是一条无法解析的
// URL —— 没有任何东西会去注册它，于是每一张内联图都变成裂图。而
// multipart/related 恰恰是企业邮件、发票、通知类 HTML 邮件的默认结构，
// 所以「正文在、图全空」是必现而非偶发。
//
// 修法：用 MIME 树里所有部件的 Content-ID 建索引，命中 cid: 时把对应
// 部件解码成 data: URI 内联回去，WebView 无需再发起任何网络请求。
// ---------------------------------------------------------------------------

export function resolveCidImages(html: string, parts: MimePart[]): string {
  if (!html || html.indexOf('cid:') === -1 || !parts.length) return html
  const byId = new Map<string, MimePart>()
  for (const p of parts) {
    if (p.contentId && !byId.has(p.contentId)) byId.set(p.contentId, p)
  }
  if (!byId.size) return html

  let budget = MAX_TOTAL_INLINE_BYTES
  const inline = (cid: string): string | null => {
    const part = byId.get(normalizeCid(safeDecodeCid(cid)))
    if (!part || !part.contentType.startsWith('image/')) return null
    // 图片字节是二进制的，不能按文本解码；base64 直接取，7bit/8bit 才转码。
    const isBase64 = part.encoding.toLowerCase() === 'base64'
    const raw64 = isBase64
      ? part.body.replace(/\s+/g, '')
      : b64FromBytes(base64ToBytesSafe(part.body))
    if (!raw64) return null
    // base64 长度 ≈ 原始字节 * 4/3
    const approxBytes = Math.floor((raw64.length * 3) / 4)
    if (approxBytes > MAX_INLINE_IMAGE_BYTES || approxBytes > budget) return null
    budget -= approxBytes
    return `data:${part.contentType};base64,${raw64}`
  }

  // 1) 带引号的 src="cid:..." / background="cid:..."
  //
  //    background 一起收：旧式邮件客户端（Outlook HTML 导出）大量用
  //    <body background="cid:logo@corp">，漏掉它就是首屏那个 logo 位置裂图。
  let out = html.replace(
    /((?:src|background)\s*=\s*)(["'])\s*cid:([^"'\s>]+)\s*\2/gi,
    (whole, prefix: string, quote: string, cid: string) => {
      const data = inline(cid)
      return data ? `${prefix}${quote}${data}${quote}` : whole
    },
  )

  // 2) CSS 的 url(cid:...)。营销/通知类 HTML 邮件的背景图大量走
  //    <td style="background-image:url(cid:logo@corp)">。style 属性在
  //    email-detail-format.ts 的 ALLOWED_ATTR 白名单里，净化会放行，
  //    但 cid: 对 WebView 依然不可解析 —— 少了这一步就是「正文在、图全空」。
  //    这里刻意不区分引号有无：CSS 里 url() 的引号是可选的，两种都要覆盖。
  //
  //    还必须认**HTML 实体形式的引号**：`style="...url(&quot;cid:x&quot;)"`。
  //    2026-10-03 实测：Word/Outlook 导出的 HTML 就是这么写的（属性分隔符被
  //    实体化成 &quot;），原来的 ["']? 只认字面引号，于是整批背景图裂掉。
  //    这是「邮件详情缺图」的另一个高频入口。
  const cssQuote = `(?:["']|&(?:quot|apos|#34|#39);)?`
  const urlRe = new RegExp(
    `(url\\(\\s*)${cssQuote}\\s*cid:([^"')\\s]+?)\\s*${cssQuote}\\s*(\\))`,
    'gi',
  )
  out = out.replace(urlRe, (whole, prefix: string, cid: string, close: string) => {
    const data = inline(cid)
    return data ? `${prefix}${data}${close}` : whole
  })

  // 3) 无引号的 src=cid:...。HTML 允许属性值不带引号，部分发信方就这么写。
  out = out.replace(
    /(\ssrc\s*=\s*)cid:([^\s>]+)/gi,
    (whole, prefix: string, cid: string) => {
      const data = inline(cid)
      return data ? `${prefix}${data}` : whole
    },
  )

  // 4) srcset="cid:a 1x, cid:b 2x"。现代响应式邮件的标配。
  //
  //    之前完全没覆盖，而且**被第 1 条的 src= 兜底掩盖了**：响应式邮件总是
  //    同时写 srcset 与 src，只测「这一张图有没有解析出来」会显示正常，
  //    实际 srcset 里那两个 cid 引用仍然是裂图（2026-10-03 实测残留 2 处）。
  out = out.replace(/(\ssrcset\s*=\s*)(["'])([^"']*)\2/gi, (whole: string, prefix: string, quote: string, value: string) => {
    const patched = value.replace(/cid:([^"'\s,]+)/gi, (m: string, cid: string) => inline(cid) || m)
    return patched === value ? whole : `${prefix}${quote}${patched}${quote}`
  })

  return out
}

/** cid 里的 %xx 是 URL 编码，但 @ 与 . 常常裸露；非法转义时退回原串。 */
function safeDecodeCid(cid: string): string {
  try {
    return decodeURIComponent(cid)
  } catch {
    return cid
  }
}

/** 非 base64 部件的原始字节（7bit/8bit 图片是 latin1 可逆的）。 */
function base64ToBytesSafe(body: string): Uint8Array {
  return latin1ToBytes(body)
}

/** 原始字节转 base64（data URI 用）。 */
function b64FromBytes(bytes: Uint8Array): string {
  try {
    let bin = ''
    const CHUNK = 0x8000
    for (let i = 0; i < bytes.length; i += CHUNK) {
      bin += String.fromCharCode(...bytes.subarray(i, i + CHUNK))
    }
    return btoa(bin)
  } catch {
    return ''
  }
}
