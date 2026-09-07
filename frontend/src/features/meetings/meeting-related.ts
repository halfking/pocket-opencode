import type { RecommendItem } from './meetings-store'

export function relatedQueryFromTranscript(texts: string[]): string {
  const joined = texts.map((t) => t.trim()).filter(Boolean).slice(-6).join(' ')
  return joined.replace(/\s+/g, ' ').slice(0, 80)
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
