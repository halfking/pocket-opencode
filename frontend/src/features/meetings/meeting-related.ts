import { truncateCodeUnits } from '../../utils/text.ts'
import type { RecommendItem } from './meetings-store'

const RELATED_STOP = new Set([
  '今天', '明天', '昨天', '下周', '我们', '进行', '以及', '这个', '那个', '负责', '提交', '方案',
])

export function relatedQueryFromTranscript(texts: string[]): string {
  const joined = texts.map((t) => t.trim()).filter(Boolean).slice(-6).join(' ')
  const compact = joined.replace(/\s+/g, ' ').trim()
  if (!compact) return ''
  // 这里原本是 compact.slice(0, 40)，而上面的守卫已经保证 compact.length <= 16
  // ⇒ 那次截断恒等于恒等变换。留着它只会让人以为「这行在截断」，
  // 也让 §258 那类形态普查把这一处误报成命中。
  if (compact.length <= 16) return compact
  const clause = compact.split(/[，。；;！？!?、]/).find((part) => part.trim().length >= 4) || compact
  const tokens = clause.match(/[A-Za-z][A-Za-z0-9]{1,}|[\u4e00-\u9fff]{2,4}/g) || []
  const kept: string[] = []
  for (const token of tokens) {
    if (RELATED_STOP.has(token) || kept.includes(token)) continue
    kept.push(token)
  }
  // 走 truncateCodeUnits：这条查询串的产物会进 URLSearchParams
  // （wikipediaSearchUrl）⇒ 落单代理对会被静默换成 U+FFFD（§258.1 实测），
  // 表现为参考资料检索「静默查不到」，没有任何报错。
  // 注意 tokens 的正则 [\u4e00-\u9fff] 不匹配 emoji，所以只有 clause 兜底
  // 这条路会带进代理对 —— 触发面窄，但不是零。
  return truncateCodeUnits(kept.slice(-2).join(' ') || clause, 40)
}

export function wikipediaSearchUrl(query: string): string {
  const q = truncateCodeUnits(query.trim(), 40)
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
