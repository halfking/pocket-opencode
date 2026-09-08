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

/** 从 IMAP BODY[TEXT] / RFC 5322 抽出可读正文；优先 HTML。 */
export function extractEmailBody(raw: string): string {
  const src = (raw || '').trim()
  if (!src) return ''
  if (!looksLikeMime(src)) return src
  const html = extractMimePart(src, 'text/html')
  if (html) return html
  const text = extractMimePart(src, 'text/plain')
  if (text) return text
  return src
}

function looksLikeMime(s: string): boolean {
  const head = s.slice(0, 4000)
  if (/content-type\s*:/i.test(head) || /content-transfer-encoding\s*:/i.test(head)) return true
  return /^--[\w'+=.-]+/m.test(s.slice(0, 200))
}

function extractMimePart(raw: string, type: string): string {
  const escaped = type.replace('/', '\\/')
  const re = new RegExp(
    `Content-Type:\\s*${escaped}[^\\n]*\\r?\\n([\\s\\S]*?)\\r?\\n\\r?\\n([\\s\\S]*?)(?=\\r?\\n--|$)`,
    'i',
  )
  const m = raw.match(re)
  if (!m) return ''
  return decodeTransfer(m[2], /Content-Transfer-Encoding:\s*(\S+)/i.exec(m[1])?.[1] || '')
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
  if (typeof Buffer !== 'undefined') {
    return Buffer.from(compact, 'base64').toString('utf8')
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
