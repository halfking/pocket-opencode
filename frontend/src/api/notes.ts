/**
 * Notes API — voice notes and knowledge base.
 * Backed by kxmemory FastAPI for AI (classify/SSOT/graph) and pocketd
 * SQLite for offline metadata caching. See notes module design in the
 * personal-assistant plan.
 */
import { http } from './http'

/**
 * 笔记即时总结的客户端超时。
 *
 * 服务端 handleNoteSummarize 给到 60 秒（server_assistant.go:610 的
 * `context.WithTimeout(r.Context(), 60*time.Second)`）。取 90 秒留余量：
 * 客户端计时从请求发出开始，服务端的从 handler 进来开始，两者之间还有
 * 网络与鉴权开销，**取相等值时客户端实际总是先到点**。
 */
export const NOTE_SUMMARIZE_TIMEOUT_MS = 90_000

export type NoteDomain = 'work' | 'study' | 'life' | 'idea'
export type NoteContentType = 'voice' | 'text' | 'mixed'

/**
 * 笔记总结里抽出的一条行动项。
 *
 * 字段名与会议侧 meetings-store.ActionItem 一致（text/assignee/due），
 * 便于两侧复用同一套期限解析（features/meetings/meeting-due-plan.ts）。
 * due 是**用户原话**（如「明天下午三点」），不在这里换算成时间戳。
 */
export interface NoteActionItem {
  text: string
  assignee?: string
  due?: string
}

export interface Note {
  id: string
  userId: string
  workspaceId?: string
  title?: string
  content: string
  contentType: NoteContentType
  domain?: NoteDomain
  category?: string
  tags?: string[]
  parentId?: string
  voiceSessionId?: string
  audioFilePath?: string
  audioDuration?: number
  isLatest: boolean
  versionNumber: number
  createdAt: string
  updatedAt: string
  createdByVoice: boolean
}

export interface NoteInput {
  content: string
  title?: string
  contentType?: NoteContentType
  domain?: NoteDomain
  tags?: string[]
  voiceSessionId?: string
  audioFilePath?: string
  audioDuration?: number
}

export const notesApi = {
  list(domain?: NoteDomain): Promise<{ notes: Note[] }> {
    const qs = domain ? `?domain=${domain}` : ''
    return http(`/api/notes${qs}`)
  },
  get(id: string): Promise<Note> {
    return http(`/api/notes/${id}`)
  },
  create(input: NoteInput): Promise<Note> {
    return http('/api/notes', {
      method: 'POST',
      body: JSON.stringify(input),
    })
  },
  update(id: string, patch: Partial<NoteInput>): Promise<Note> {
    return http(`/api/notes/${id}`, {
      method: 'PUT',
      body: JSON.stringify(patch),
    })
  },
  delete(id: string): Promise<void> {
    return http(`/api/notes/${id}`, { method: 'DELETE' })
  },
  /** Ask kxmemory to classify + auto-tag a note. */
  classify(id: string): Promise<{ domain: NoteDomain; category: string; tags: string[] }> {
    return http(`/api/notes/${id}/classify`, { method: 'POST' })
  },
  /**
   * 即时总结（语音草稿收尾后立刻调用）。
   * 后端走 llmBFF 智能路由；失败时返回空 summary，前端不阻塞流程。
   *
   * 2026-10-03：这里原先没传 timeoutMs，吃的是默认 30s，而服务端
   * handleNoteSummarize 是 `context.WithTimeout(r.Context(), 60*time.Second)`
   * （server_assistant.go:610）。**客户端比服务端先放弃**，所以推理一慢就变成
   * 「服务端算完了、前端报失败」——这正是用户报的「没有即时总结」里最难查的
   * 那一类：后端日志写着 200，界面却拿不到 summary。
   *
   * 2026-10-06：新增 action_items。需求「录音时总结并把时间点自动加进日程」
   * 在随手记侧此前没有落点 —— 服务端只回一个 summary 字符串，前端拿不到
   * 任何期限，「明天下午三点」这类时间点在语音笔记里直接消失。字段与会议侧
   * 的 ActionItem 同名同义，due 保留用户原话（中文），换算在前端做
   * （后端与设备可能不在同一时区）。
   */
  summarize(
    id: string,
    signal?: AbortSignal,
  ): Promise<{ summary: string; model?: string; action_items?: NoteActionItem[] }> {
    return http(`/api/notes/${id}/summarize`, {
      method: 'POST',
      timeoutMs: NOTE_SUMMARIZE_TIMEOUT_MS,
      signal,
    })
  },
  /** Hybrid search across notes. */
  search(query: string): Promise<{ notes: Note[] }> {
    return http(`/api/notes/search?q=${encodeURIComponent(query)}`)
  },
}
