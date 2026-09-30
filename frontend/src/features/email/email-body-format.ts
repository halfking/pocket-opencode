/** 邮件正文展示与转发草稿：识别 HTML、引用原文、解析收件人。 */

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
  body: string
}

/** 单张内联图上限；超限保留 cid:（渲染为裂图也优于让整封 HTML 撑爆内存）。 */
const MAX_INLINE_IMAGE_BYTES = 1_500_000
/** 整封邮件内联图总量上限，避免 base64 膨胀 33% 后撑爆 WebView 内存。 */
const MAX_TOTAL_INLINE_BYTES = 6_000_000

/** 头/体分界：第一个空行。CRLF 与 LF 都认。 */
const BLANK_LINE = /\r?\n\r?\n/

/** 从 IMAP BODY[TEXT] / RFC 5322 抽出可读正文；优先 HTML。 */
export function extractEmailBody(raw: string): string {
  const src = (raw || '').trim()
  if (!src) return ''
  if (!looksLikeMime(src)) return src
  const parts = splitMimeParts(src)
  if (!parts.length) return src

  const html = parts.find((p) => p.contentType === 'text/html')
  if (html) return resolveCidImages(decodeTransfer(html.body, html.encoding), parts)
  const text = parts.find((p) => p.contentType === 'text/plain')
  if (text) return decodeTransfer(text.body, text.encoding)
  return src
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

  if (contentType.startsWith('multipart/')) {
    const boundary = /boundary\s*=\s*"?([^";\r\n]+)"?/i.exec(headers)?.[1]?.trim()
    if (!boundary) return
    for (const chunk of splitByBoundary(body, boundary)) {
      walkMime(chunk, out, depth + 1)
    }
    return
  }
  if (!contentType) return
  out.push({ contentType, contentId, encoding, body })
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

function decodeTransfer(body: string, encoding: string): string {
  const enc = encoding.toLowerCase()
  if (enc === 'base64') {
    try {
      return decodeBase64(body.replace(/\s+/g, '')).trim()
    } catch {
      return body.trim()
    }
  }
  if (enc === 'quoted-printable') return decodeQuotedPrintable(body).trim()
  return body.trim()
}

function decodeBase64(compact: string): string {
  // Node 环境走 Buffer（运行时探测，避免引入 @types/node 依赖）；浏览器走 atob。
  const NodeBuffer = (globalThis as { Buffer?: { from(s: string, enc: string): { toString(enc: string): string } } }).Buffer
  if (NodeBuffer) {
    return NodeBuffer.from(compact, 'base64').toString('utf8')
  }
  const bin = atob(compact)
  return new TextDecoder('utf-8').decode(Uint8Array.from(bin, (c) => c.charCodeAt(0)))
}

function decodeQuotedPrintable(s: string): string {
  const merged = s.replace(/=\r?\n/g, '')
  const bytes: number[] = []
  let i = 0
  while (i < merged.length) {
    if (merged[i] === '=' && /^[0-9A-Fa-f]{2}/.test(merged.slice(i + 1, i + 3))) {
      bytes.push(parseInt(merged.slice(i + 1, i + 3), 16))
      i += 3
      continue
    }
    const cp = merged.codePointAt(i) ?? 0
    const enc = new TextEncoder().encode(String.fromCodePoint(cp))
    for (const b of enc) bytes.push(b)
    i += cp > 0xffff ? 2 : 1
  }
  return new TextDecoder('utf-8', { fatal: false }).decode(new Uint8Array(bytes))
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
      const raw64 = part.encoding.toLowerCase() === 'base64'
        ? part.body.replace(/\s+/g, '')
        : b64FromText(decodeTransfer(part.body, part.encoding))
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

/** 非 base64 部件转 base64（走 UTF-8 字节，兼容 data URI 要求）。 */
function b64FromText(text: string): string {
  try {
    const bytes = new TextEncoder().encode(text)
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
