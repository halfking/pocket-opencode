/**
 * meetings API — 会议摘要/推荐/精翻，代理 pocketd → kxmemory / LLM
 */
import { http } from './http'
import type { ActionItem, LiveSummary, MeetingSegment, RecommendItem } from '../features/meetings/meetings-store'

export interface SummaryResult {
  summary: string
  keyPoints: string[]
  actionItems: ActionItem[]
  decisions: string[]
  openQuestions: string[]
}

export interface RefineResult {
  refinedTranscript: string
  translations: Record<string, string>
  structuredMinutes: {
    agenda: string[]
    decisions: string[]
    actionItems: ActionItem[]
    nextMeeting: string | null
  }
  todos: ActionItem[]
  noteId?: string
  tasksCreated?: number
}

export interface MeetingSyncPayload {
  id: string
  title?: string
  location?: string
  participants?: string[]
  startedAt: number
  durationMs?: number
  summary?: string
  refinedTranscript?: string
  noteId?: string
  status?: string
}

export const meetingsApi = {
  /** 增量滚动摘要 */
  async summarize(
    meetingId: string,
    segments: MeetingSegment[],
    prevSummary?: string,
    meta?: { title?: string; participants?: string[]; location?: string },
  ): Promise<SummaryResult> {
    try {
      const raw = await http<Record<string, unknown>>(`/api/meetings/${meetingId}/summary`, {
        method: 'POST',
        body: JSON.stringify({
          segments: toApiSegments(segments),
          prev_summary: prevSummary,
          meta,
        }),
      })
      return normalizeSummary(raw)
    } catch {
      return fallbackSummarize(segments, prevSummary)
    }
  },

  /** 智能推荐 */
  async recommend(
    meetingId: string,
    segments: MeetingSegment[],
    summary?: string,
  ): Promise<RecommendItem[]> {
    try {
      const res = await http<{ items: RecommendItem[] }>(
        `/api/meetings/${meetingId}/recommend`,
        {
          method: 'POST',
          body: JSON.stringify({ segments: toApiSegments(segments), summary }),
        },
      )
      return res.items ?? []
    } catch {
      return []
    }
  },

  /** 事后精翻 */
  async refine(
    meetingId: string,
    segments: MeetingSegment[],
    targetLangs: string[] = ['en'],
    meta?: { title?: string; participants?: string[]; location?: string },
  ): Promise<RefineResult> {
    try {
      const raw = await http<Record<string, unknown>>(`/api/meetings/${meetingId}/refine`, {
        method: 'POST',
        body: JSON.stringify({
          segments: toApiSegments(segments),
          target_langs: targetLangs,
          meta,
        }),
      })
      return normalizeRefine(raw, segments)
    } catch {
      return fallbackRefine(segments)
    }
  },

  /** 云同步会议元数据（不含音频/转写全文） */
  async syncMeeting(payload: MeetingSyncPayload): Promise<void> {
    await http('/api/meetings', {
      method: 'POST',
      body: JSON.stringify({
        id: payload.id,
        title: payload.title,
        location: payload.location,
        participants: payload.participants ?? [],
        startedAt: payload.startedAt,
        durationMs: payload.durationMs ?? 0,
        summary: payload.summary,
        refinedTranscript: payload.refinedTranscript,
        noteId: payload.noteId,
        status: payload.status ?? 'completed',
      }),
    })
  },
}

function toApiSegments(segments: MeetingSegment[]) {
  return segments.map((s) => ({
    speaker: s.speakerLabel ?? '说话人',
    text: s.text,
    lang: s.lang,
    start_ms: s.startMs,
    end_ms: s.endMs,
  }))
}

function normalizeSummary(raw: Record<string, unknown>): SummaryResult {
  return {
    summary: String(raw.summary ?? ''),
    keyPoints: (raw.key_points ?? raw.keyPoints ?? []) as string[],
    actionItems: normalizeActionItems(raw.action_items ?? raw.actionItems),
    decisions: (raw.decisions ?? []) as string[],
    openQuestions: (raw.open_questions ?? raw.openQuestions ?? []) as string[],
  }
}

function normalizeRefine(raw: Record<string, unknown>, segments: MeetingSegment[]): RefineResult {
  const sm = (raw.structured_minutes ?? raw.structuredMinutes ?? {}) as Record<string, unknown>
  const fallback = segments.map((s) => `[${s.speakerLabel}] ${s.text}`).join('\n')
  return {
    refinedTranscript: String(raw.refined_transcript ?? raw.refinedTranscript ?? fallback),
    translations: (raw.translations ?? {}) as Record<string, string>,
    structuredMinutes: {
      agenda: (sm.agenda ?? []) as string[],
      decisions: (sm.decisions ?? []) as string[],
      actionItems: normalizeActionItems(sm.action_items ?? sm.actionItems),
      nextMeeting: (sm.next_meeting ?? sm.nextMeeting ?? null) as string | null,
    },
    todos: normalizeActionItems(raw.todos),
    noteId: (raw.note_id ?? raw.noteId) as string | undefined,
    tasksCreated: Number(raw.tasks_created ?? raw.tasksCreated ?? 0) || undefined,
  }
}

function normalizeActionItems(raw: unknown): ActionItem[] {
  if (!Array.isArray(raw)) return []
  return raw.map((a) =>
    typeof a === 'string' ? { text: a } : a as ActionItem,
  )
}

/** LLM 兜底：直接调 /api/llm/chat */
async function fallbackSummarize(
  segments: MeetingSegment[],
  prevSummary?: string,
): Promise<SummaryResult> {
  const transcript = segments.map((s) =>
    `[${s.speakerLabel ?? '说话人'}] ${s.text}`,
  ).join('\n')

  const prompt = prevSummary
    ? `以下是会议转写的新增内容，请在已有摘要基础上更新：\n\n已有摘要：\n${prevSummary}\n\n新增转写：\n${transcript}\n\n请返回 JSON：{"summary":"","key_points":[],"action_items":[],"decisions":[],"open_questions":[]}`
    : `请为以下会议转写生成摘要，返回 JSON：{"summary":"","key_points":[],"action_items":[],"decisions":[],"open_questions":[]}\n\n转写：\n${transcript}`

  try {
    const res = await http<{ content: string }>('/api/llm/chat', {
      method: 'POST',
      body: JSON.stringify({ messages: [{ role: 'user', content: prompt }] }),
    })
    return parseSummaryJson(res.content)
  } catch {
    return {
      summary: transcript.slice(0, 200) || '暂无摘要',
      keyPoints: [],
      actionItems: [],
      decisions: [],
      openQuestions: [],
    }
  }
}

async function fallbackRefine(segments: MeetingSegment[]): Promise<RefineResult> {
  const transcript = segments.map((s) =>
    `[${s.speakerLabel ?? '说话人'}] ${s.text}`,
  ).join('\n')

  try {
    const res = await http<{ content: string }>('/api/llm/chat', {
      method: 'POST',
      body: JSON.stringify({
        messages: [{
          role: 'user',
          content: `请润色以下会议转写，返回 JSON：{"refined_transcript":"","translations":{},"structured_minutes":{"agenda":[],"decisions":[],"action_items":[],"next_meeting":null},"todos":[]}\n\n${transcript}`,
        }],
      }),
    })
    const parsed = JSON.parse(extractJson(res.content))
    return {
      refinedTranscript: parsed.refined_transcript ?? transcript,
      translations: parsed.translations ?? {},
      structuredMinutes: {
        agenda: parsed.structured_minutes?.agenda ?? [],
        decisions: parsed.structured_minutes?.decisions ?? [],
        actionItems: parsed.structured_minutes?.action_items ?? parsed.structured_minutes?.actionItems ?? [],
        nextMeeting: parsed.structured_minutes?.next_meeting ?? null,
      },
      todos: parsed.todos ?? [],
    }
  } catch {
    return {
      refinedTranscript: transcript,
      translations: {},
      structuredMinutes: { agenda: [], decisions: [], actionItems: [], nextMeeting: null },
      todos: [],
    }
  }
}

function parseSummaryJson(content: string): SummaryResult {
  try {
    const parsed = JSON.parse(extractJson(content))
    return {
      summary: parsed.summary ?? '',
      keyPoints: parsed.key_points ?? parsed.keyPoints ?? [],
      actionItems: (parsed.action_items ?? parsed.actionItems ?? []).map((a: ActionItem | string) =>
        typeof a === 'string' ? { text: a } : a,
      ),
      decisions: parsed.decisions ?? [],
      openQuestions: parsed.open_questions ?? parsed.openQuestions ?? [],
    }
  } catch {
    return { summary: content.slice(0, 500), keyPoints: [], actionItems: [], decisions: [], openQuestions: [] }
  }
}

function extractJson(text: string): string {
  const match = text.match(/\{[\s\S]*\}/)
  return match ? match[0] : text
}

export function toLiveSummary(result: SummaryResult): LiveSummary {
  return {
    summary: result.summary,
    keyPoints: result.keyPoints,
    actionItems: result.actionItems,
    decisions: result.decisions,
    openQuestions: result.openQuestions,
    updatedAt: Date.now(),
  }
}
