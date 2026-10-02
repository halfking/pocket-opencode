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
  return src
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
  return splitHeadBody(s).body
}

function looksLikeMime(s: string): boolean {
  // 结构判据：头部止于第一个空行（CRLF 与 LF 都认）。
  const head = splitHeadBody(s.slice(0, MIME_HEAD_SCAN_LIMIT)).headers
  if (/content-type\s*:/i.test(head) || /content-transfer-encoding\s*:/i.test(head)) return true
  if (/^mime-version\s*:/i.test(head)) return true
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
  const contentType = (/content-type\s*:\s*([^\s;]+)/i.exec(headers)?.[1] || '').toLowerCase()
  const encoding = /content-transfer-encoding\s*:\s*(\S+)/i.exec(headers)?.[1] || ''
  const contentId = normalizeCid(/content-id\s*:\s*([^\r\n]+)/i.exec(headers)?.[1] || '')
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
 * 头部里的 charset。优先取 Content-Type 的 `charset=` 参数——正文部件的权威声明
 * 就写在这里；某些客户端还会额外发一个裸 `charset=` 头，一并兜住。
 */
function headerCharset(headers: string): string {
  const ct = /content-type\s*:\s*([^\r\n]*)/i.exec(headers)?.[1] || ''
  const fromType = /charset\s*=\s*"?([^";\s]+)"?/i.exec(ct)?.[1] || ''
  if (fromType) return fromType
  return /^\s*charset\s*=\s*"?([^";\s]+)"?/im.exec(headers)?.[1] || ''
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

  // 1) 带引号的 src="cid:..."（原始实现覆盖的形态）
  let out = html.replace(
    /(src\s*=\s*)(["'])\s*cid:([^"'\s>]+)\s*\2/gi,
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
  out = out.replace(
    /(url\(\s*)["']?\s*cid:([^"')]+?)\s*["']?\s*(\))/gi,
    (whole, prefix: string, cid: string, close: string) => {
      const data = inline(cid)
      return data ? `${prefix}${data}${close}` : whole
    },
  )

  // 3) 无引号的 src=cid:...。HTML 允许属性值不带引号，部分发信方就这么写。
  out = out.replace(
    /(\ssrc\s*=\s*)cid:([^\s>]+)/gi,
    (whole, prefix: string, cid: string) => {
      const data = inline(cid)
      return data ? `${prefix}${data}` : whole
    },
  )

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
