export type NoteDomain = 'work' | 'study' | 'life' | 'idea'

const TECH_TAGS: Record<string, string> = {
  go: 'Go', golang: 'Go', python: 'Python', javascript: 'JavaScript',
  typescript: 'TypeScript', vue: 'Vue', react: 'React', docker: 'Docker',
  kubernetes: 'Kubernetes', k8s: 'Kubernetes', postgresql: 'PostgreSQL',
  postgres: 'PostgreSQL', redis: 'Redis', mysql: 'MySQL', aws: 'AWS',
  api: 'API', ai: 'AI', llm: 'LLM', git: 'Git', linux: 'Linux',
}

const DOMAIN_RULES: Array<{ domain: NoteDomain; keys: string[] }> = [
  { domain: 'work', keys: ['会议', '周会', '讨论', 'sprint', 'agenda', '会议纪要', '参会', '议程', '决策'] },
  { domain: 'idea', keys: ['想法', '主意', '灵感', '突发奇想', '想到', '建议'] },
  { domain: 'study', keys: ['学习', '教程', '笔记', '知识点', '总结', '理解', '概念', '原理'] },
]

function hasAny(text: string, keys: string[]): boolean {
  return keys.some((k) => text.includes(k))
}

export function extractLocalTags(content: string): string[] {
  const lower = content.toLowerCase()
  if (!lower.trim()) return []
  const tags: string[] = []
  const seen = new Set<string>()
  for (const [keyword, tag] of Object.entries(TECH_TAGS)) {
    if (lower.includes(keyword) && !seen.has(tag)) {
      tags.push(tag)
      seen.add(tag)
    }
  }
  return tags
}

export function inferDomain(content: string): NoteDomain {
  const lower = content.toLowerCase()
  for (const rule of DOMAIN_RULES) {
    if (hasAny(lower, rule.keys)) return rule.domain
  }
  return 'life'
}

export function suggestTitle(content: string, max = 24): string {
  const first = content.trim().split(/[。.!！?\n]/)[0]?.trim() ?? ''
  if (!first) return ''
  return first.length <= max ? first : first.slice(0, max)
}

export function mergeTags(manual: string[], extracted: string[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const tag of [...manual, ...extracted]) {
    const t = tag.trim()
    if (!t || seen.has(t)) continue
    seen.add(t)
    out.push(t)
  }
  return out
}
