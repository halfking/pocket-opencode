const CJK = /[\u3400-\u9fff]/

/** unicode61 把连续汉字当成一个 token，必须把双字词写进 search_text 才能 MATCH。 */
export function cjkBigramTokens(text: string): string {
  const chars = (text.match(/[\u3400-\u9fff]/g) ?? []).join('')
  if (chars.length < 2) return chars
  const grams: string[] = []
  for (let i = 0; i < chars.length - 1 && grams.length < 80; i++) {
    grams.push(chars.slice(i, i + 2))
  }
  return grams.join(' ')
}

export function buildFtsQuery(raw: string): string {
  const parts: string[] = []
  const latin = raw.match(/[A-Za-z0-9_]+/g) ?? []
  const skip = new Set(['AND', 'OR', 'NOT', 'NEAR'])
  for (const w of latin) {
    if (skip.has(w.toUpperCase())) continue
    parts.push(`"${w.replace(/"/g, '""')}"`)
  }

  const cjk = (raw.match(/[\u3400-\u9fff]+/g) ?? []).join('')
  if (cjk.length === 1) parts.push(`"${cjk}"`)
  else if (cjk.length > 1) {
    const grams: string[] = []
    for (let i = 0; i < cjk.length - 1 && grams.length < 8; i++) {
      grams.push(`"${cjk.slice(i, i + 2)}"`)
    }
    parts.push(grams.join(' OR '))
  }

  if (parts.length === 0) {
    const fallback = raw.trim().replace(/["*]/g, '')
    return fallback ? `"${fallback}"` : ''
  }
  return parts.join(' ')
}

export function needsCjkLike(raw: string): boolean {
  return CJK.test(raw)
}

export function mergeSearchHits<T extends { note: { id: string } }>(
  primary: T[],
  extra: T[],
  limit: number,
): T[] {
  const seen = new Set(primary.map((r) => r.note.id))
  const out = [...primary]
  for (const r of extra) {
    if (seen.has(r.note.id)) continue
    seen.add(r.note.id)
    out.push(r)
  }
  return out.slice(0, limit)
}

export function extractiveBriefing(
  query: string,
  notes: Array<{ title?: string | null; content: string; summary?: string | null }>,
): string {
  if (notes.length === 0) return '没有找到相关笔记。'
  const lines = notes.slice(0, 3).map((n, i) => {
    const title = (n.title || '').trim() || `笔记 ${i + 1}`
    const body = (n.summary || n.content || '').trim()
    const sentence = body.split(/[。.!！?\n]/)[0]?.trim() || body.slice(0, 40)
    return `${i + 1}. ${title}：${sentence}`
  })
  return `围绕「${query.trim()}」找到 ${notes.length} 条笔记。\n${lines.join('\n')}`
}
