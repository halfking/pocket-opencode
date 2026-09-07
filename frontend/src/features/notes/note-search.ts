import { llmBffApi, type ChatStreamDelta } from '../../api/llm-bff'
import type { LocalNote, SearchResult } from './notes-types'
import { searchHybrid } from './notes-search'
import { extractiveBriefing } from './note-fts-query'
import {
  fallbackSearchBriefing,
  parseNoteSearchResponse,
  type NoteSearchMatch,
} from './note-search-parse'

export type { NoteSearchMatch }
export { fallbackSearchBriefing, parseNoteSearchResponse }

export interface NoteSearchBriefing {
  summary: string
  matches: NoteSearchMatch[]
  results: SearchResult[]
  offline: boolean
}

async function streamAssistant(kind: string, system: string, user: string): Promise<string> {
  const chunks: string[] = []
  await new Promise<void>((resolve, reject) => {
    llmBffApi.streamChat(
      {
        kind,
        messages: [
          { role: 'system', content: system },
          { role: 'user', content: user },
        ],
      },
      {
        onDelta(delta: ChatStreamDelta) {
          if (delta.content) chunks.push(delta.content)
        },
        onError: reject,
        onDone: () => resolve(),
      },
    )
  })
  return chunks.join('')
}

export async function extractTagsWithAi(content: string): Promise<string[]> {
  const raw = await streamAssistant(
    'note_tags',
    '从笔记中提取 3-8 个简短中文或英文标签。只输出 JSON 数组，例如 ["周报","OKR"]。不要解释。',
    content.slice(0, 4000),
  )
  const start = raw.indexOf('[')
  const end = raw.lastIndexOf(']')
  if (start < 0 || end <= start) return []
  try {
    const parsed = JSON.parse(raw.slice(start, end + 1)) as unknown
    if (!Array.isArray(parsed)) return []
    return parsed.filter((t): t is string => typeof t === 'string' && t.trim().length > 0)
  } catch {
    return []
  }
}

export async function searchNotesWithIntent(
  query: string,
  workspaceId = 'default',
): Promise<NoteSearchBriefing> {
  const results = await searchHybrid(query, 20, workspaceId)
  const snippets = results.map((r) => r.note)
  const localBrief = extractiveBriefing(query, snippets)
  if (results.length === 0) {
    return { summary: localBrief, matches: [], results, offline: true }
  }
  const allowedIds = results.map((r) => r.note.id)
  const catalog = results.map((r, i) => {
    const n: LocalNote = r.note
    return `${i + 1}. id=${n.id} title=${n.title || ''} snippet=${(n.summary || n.content).slice(0, 180)}`
  }).join('\n')
  try {
    const raw = await streamAssistant(
      'note_search',
      '你是笔记检索助手。根据用户问题，对候选笔记做意图匹配。只输出 JSON：{"summary":"用中文写一段综合摘要，多数情况下读这段就够","matches":[{"id":"...","reason":"为何相关"}]}。id 必须来自候选列表。不要编造笔记内容。',
      `问题：${query}\n候选：\n${catalog}`,
    )
    const parsed = parseNoteSearchResponse(raw, allowedIds)
    const rank = new Map(parsed.matches.map((m, i) => [m.id, i]))
    const ordered = parsed.matches.length
      ? [...results].sort((a, b) => {
          const ai = rank.has(a.note.id) ? (rank.get(a.note.id) as number) : 999
          const bi = rank.has(b.note.id) ? (rank.get(b.note.id) as number) : 999
          return ai - bi
        })
      : results
    return {
      summary: parsed.summary || localBrief,
      matches: parsed.matches,
      results: ordered,
      offline: !parsed.summary,
    }
  } catch {
    return { summary: localBrief || fallbackSearchBriefing(results.length), matches: [], results, offline: true }
  }
}
