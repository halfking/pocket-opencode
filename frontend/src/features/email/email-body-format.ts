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
  if (!looksLikeMime(src)) return src
  const parts = splitMimeParts(src)
  if (!parts.length) return src

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

function looksLikeMime(s: string): boolean {
  const head = s.slice(0, 4000)
  if (/content-type\s*:/i.test(head) || /content-transfer-encoding\s*:/i.test(head)) return true
  return /^--[\w'+=.-]+/m.test(s.slice(0, 200))
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
  return html.replace(
    /(src\s*=\s*)(["'])\s*cid:([^"'\s>]+)\s*\2/gi,
    (whole, prefix: string, quote: string, cid: string) => {
      const part = byId.get(normalizeCid(safeDecodeCid(cid)))
      if (!part || !part.contentType.startsWith('image/')) return whole
      // 图片字节是二进制的，不能按文本解码；base64 直接取，7bit/8bit 才转码。
      const isBase64 = part.encoding.toLowerCase() === 'base64'
      const raw64 = isBase64
        ? part.body.replace(/\s+/g, '')
        : b64FromBytes(base64ToBytesSafe(part.body))
      if (!raw64) return whole
      // base64 长度 ≈ 原始字节 * 4/3
      const approxBytes = Math.floor((raw64.length * 3) / 4)
      if (approxBytes > MAX_INLINE_IMAGE_BYTES || approxBytes > budget) return whole
      budget -= approxBytes
      return `${prefix}${quote}data:${part.contentType};base64,${raw64}${quote}`
    },
  )
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
