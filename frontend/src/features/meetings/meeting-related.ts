import type { RecommendItem } from './meetings-store'

const RELATED_STOP = new Set([
  '今天', '明天', '昨天', '下周', '我们', '进行', '以及', '这个', '那个', '负责', '提交', '方案',
])

export function relatedQueryFromTranscript(texts: string[]): string {
  const joined = texts.map((t) => t.trim()).filter(Boolean).slice(-6).join(' ')
  const compact = joined.replace(/\s+/g, ' ').trim()
  if (!compact) return ''
  if (compact.length <= 16) return compact.slice(0, 40)
  const clause = compact.split(/[，。；;！？!?、]/).find((part) => part.trim().length >= 4) || compact
  const tokens = clause.match(/[A-Za-z][A-Za-z0-9]{1,}|[\u4e00-\u9fff]{2,4}/g) || []
  const kept: string[] = []
  for (const token of tokens) {
    if (RELATED_STOP.has(token) || kept.includes(token)) continue
    kept.push(token)
  }
  return (kept.slice(-2).join(' ') || clause).slice(0, 40)
}

export function wikipediaSearchUrl(query: string): string {
  const q = query.trim().slice(0, 40)
  if (!q) return ''
  const params = new URLSearchParams({
    action: 'opensearch',
    search: q,
    limit: '3',
    namespace: '0',
    format: 'json',
    origin: '*',
  })
  return `https://zh.wikipedia.org/w/api.php?${params}`
}

export function parseWikipediaOpenSearch(data: unknown): RecommendItem[] {
  if (!Array.isArray(data) || data.length < 4) return []
  const titles = Array.isArray(data[1]) ? data[1] : []
  const snippets = Array.isArray(data[2]) ? data[2] : []
  const urls = Array.isArray(data[3]) ? data[3] : []
  const items: RecommendItem[] = []
  for (let i = 0; i < titles.length; i++) {
    const title = String(titles[i] ?? '').trim()
    if (!title) continue
    items.push({
      type: 'web',
      id: `wiki-${title}`,
      title,
      snippet: String(snippets[i] ?? ''),
      score: 1 / (i + 1),
      url: String(urls[i] ?? ''),
    })
  }
  return items
}

export function mergeRecommendations(
  primary: RecommendItem[],
  extra: RecommendItem[],
  limit = 6,
): RecommendItem[] {
  const seen = new Set<string>()
  const out: RecommendItem[] = []
  for (const item of [...primary, ...extra]) {
    const key = `${item.type}:${item.id}`
    if (seen.has(key)) continue
    seen.add(key)
    out.push(item)
    if (out.length >= limit) break
  }
  return out
}
