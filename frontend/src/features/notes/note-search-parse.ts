export interface NoteSearchMatch {
  id: string
  reason: string
}

export function parseNoteSearchResponse(raw: string, allowedIds: string[]): {
  summary: string
  matches: NoteSearchMatch[]
} {
  const allowed = new Set(allowedIds)
  const start = raw.indexOf('{')
  const end = raw.lastIndexOf('}')
  if (start < 0 || end <= start) return { summary: '', matches: [] }
  try {
    const parsed = JSON.parse(raw.slice(start, end + 1)) as {
      summary?: unknown
      matches?: unknown
    }
    const summary = typeof parsed.summary === 'string' ? parsed.summary.trim() : ''
    const matches: NoteSearchMatch[] = []
    if (Array.isArray(parsed.matches)) {
      for (const item of parsed.matches) {
        if (!item || typeof item !== 'object') continue
        const rec = item as { id?: unknown; reason?: unknown }
        if (typeof rec.id !== 'string' || !allowed.has(rec.id)) continue
        matches.push({
          id: rec.id,
          reason: typeof rec.reason === 'string' ? rec.reason : '',
        })
      }
    }
    return { summary, matches }
  } catch {
    return { summary: '', matches: [] }
  }
}

export function fallbackSearchBriefing(hitCount: number): string {
  if (hitCount === 0) return '没有找到相关笔记。'
  return `找到 ${hitCount} 条相关笔记，离线仅展示全文匹配。`
}
