/**
 * meetings API — 会议摘要/推荐/精翻，代理 pocketd → kxmemory / LLM
 */
import { http, isAbortError } from './http'
import type { ActionItem, LiveSummary, MeetingSegment, RecommendItem } from '../features/meetings/meetings-store'

/**
 * 会议链路长请求的客户端超时。
 *
 * 2026-10-03 普查出来的：这两个调用点原先**都没传 timeoutMs**，一律吃
 * http.ts 的默认 30 秒，而后端各自的预算是
 *
 *   handleMeetingSummary  context.WithTimeout(r.Context(), 45*time.Second)  ← 45s
 *   handleMeetingRefine   context.WithTimeout(r.Context(), 90*time.Second)  ← 90s
 *
 * （server_meeting.go:224 / 313）
 *
 * 两条全中：客户端 30s < 服务端 45s/90s。表现是「服务端写完了、前端报失败」
 * ——会议摘要偶尔转不出来、事后精翻一按就报错，而且只要推理真的用满预算就
 * **每次都失败**。
 *
 * 取值留出余量而不是与服务端相等：客户端计时含网络与鉴权开销，取相等值时
 * 客户端实际总是先到点。
 *
 * 同文件还有第三条 handleTranscribeMeeting（120s）目前**前端没有调用点**
 * （录音走 /api/stt/*），所以这里不给它配常量——留一个没人引用的常量只会
 * 让下一个人以为它生效了。
 */
export const MEETING_SUMMARY_TIMEOUT_MS = 90_000
export const MEETING_REFINE_TIMEOUT_MS = 150_000

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
  /** true = 云端不可用，仅本地拼装转写，非真正精翻 */
  fromFallback?: boolean
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
    signal?: AbortSignal,
  ): Promise<SummaryResult> {
    try {
      const raw = await http<Record<string, unknown>>(`/api/meetings/${meetingId}/summary`, {
        method: 'POST',
        body: JSON.stringify({
          segments: toApiSegments(segments),
          prev_summary: prevSummary,
          meta,
        }),
        timeoutMs: MEETING_SUMMARY_TIMEOUT_MS,
        signal,
      })
      return normalizeSummary(raw)
    } catch (e) {
      // 中止不是「失败」：走到 fallback 会把用户主动取消，悄悄变成一份
      // 降级摘要写进会议里——那比不做还糟（用户以为取消了，库里却有内容）。
      if (isAbortError(e)) throw e
      return fallbackSummarize(segments, prevSummary, signal)
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
    signal?: AbortSignal,
  ): Promise<RefineResult> {
    try {
      const raw = await http<Record<string, unknown>>(`/api/meetings/${meetingId}/refine`, {
        method: 'POST',
        body: JSON.stringify({
          segments: toApiSegments(segments),
          target_langs: targetLangs,
          meta,
        }),
        timeoutMs: MEETING_REFINE_TIMEOUT_MS,
        signal,
      })
      return normalizeRefine(raw, segments)
    } catch (e) {
      if (isAbortError(e)) throw e
      return fallbackRefine(segments, signal)
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
  signal?: AbortSignal,
): Promise<SummaryResult> {
  const transcript = segments.map((s) =>
    `[${s.speakerLabel ?? '说话人'}] ${s.text}`,
  ).join('\n')

  const prompt = prevSummary
    ? `你是会议记录助手。只根据转写更新摘要，禁止编造。结合已有摘要与新增转写，返回 JSON：{"tldr":"","topics":[],"summary":"","key_points":[],"action_items":[{"text":"","assignee":"","due":""}],"decisions":[],"open_questions":[]}\n\n已有摘要：\n${prevSummary}\n\n新增转写：\n${transcript}`
    : `你是会议记录助手。只根据转写生成摘要，禁止编造。返回 JSON：{"tldr":"","topics":[],"summary":"","key_points":[],"action_items":[{"text":"","assignee":"","due":""}],"decisions":[],"open_questions":[]}\n\n转写：\n${transcript}`

  try {
    const res = await http<{ content: string }>('/api/llm/chat', {
      method: 'POST',
      body: JSON.stringify({
        kind: 'meeting_summary',
        messages: [{ role: 'user', content: prompt }],
      }),
      signal,
    })
    return parseSummaryJson(res.content)
  } catch (e) {
    // 降级链自己也可能撞上中止（用户在兜底重试期间点了取消）。这里如果照旧
    // 吞掉，就会把一次取消变成「转写前 200 字的摘要」——用户在界面上点了取消，
    // 会议里却多了一段内容。宁可让中止继续往外抛。
    if (isAbortError(e)) throw e
    return {
      summary: transcript.slice(0, 200) || '暂无摘要',
      keyPoints: [],
      actionItems: [],
      decisions: [],
      openQuestions: [],
    }
  }
}

async function fallbackRefine(segments: MeetingSegment[], signal?: AbortSignal): Promise<RefineResult> {
  const transcript = segments.map((s) =>
    `[${s.speakerLabel ?? '说话人'}] ${s.text}`,
  ).join('\n')

  try {
    const res = await http<{ content: string }>('/api/llm/chat', {
      method: 'POST',
      body: JSON.stringify({
        kind: 'meeting_refine',
        messages: [{
          role: 'user',
          content: `请润色以下会议转写（语篇规整 + 中英对照），返回 JSON：{"refined_transcript":"","translations":{},"structured_minutes":{"agenda":[],"decisions":[],"action_items":[],"next_meeting":null},"todos":[]}\n\n${transcript}`,
        }],
      }),
      signal,
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
      fromFallback: true,
    }
  } catch (e) {
    // 同 fallbackSummarize：中止必须继续往外抛，不能落到下面那份
    // fromFallback 的拼装结果上。
    if (isAbortError(e)) throw e
    return {
      refinedTranscript: transcript,
      translations: {},
      structuredMinutes: { agenda: [], decisions: [], actionItems: [], nextMeeting: null },
      todos: [],
      fromFallback: true,
    }
  }
}

function parseSummaryJson(content: string): SummaryResult {
  try {
    const parsed = JSON.parse(extractJson(content))
    return {
      summary: parsed.summary ?? parsed.tldr ?? '',
      keyPoints: parsed.key_points ?? parsed.keyPoints ?? parsed.topics ?? [],
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
