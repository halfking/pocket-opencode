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

/**
 * §127：`/summarize` **无条件**做的自动记账，三键（server_assistant.go:704-755）。
 *
 * 「无条件」的准确含义：`s.financeStore` 在 `server.go:299` 恒非 nil，
 * `finance.NewRecognizer().Parse()` 对「中午吃饭花了 38 块」这种普通口语就会命中，
 * 而服务端**没有给任何调用方开关**。⇒ 调一次 summarize 就可能动用户的账本。
 *
 * ⚠ `transactions` 服务端**总是**返回数组（可能是空的），所以
 *   「有没有入账」只能看 `transactions.length`，**不能看键在不在** ——
 *   键恒在（与批 88「值类型 + omitempty 吃掉 0」同一族）。
 *
 * ⚠ 以前这份形状被**声明了两遍**（NoteDetailView 的本地 `NoteSummarizeResp`
 *   与这里的返回值），是 §117.9 记的分叉。声明留在本地不搬，是因为
 *   详情页走裸 `http` 而不是这一层；但**字段语义**以本注释为准。
 */
export interface NoteSummarizeBookkeeping {
  /** 入账记录（可能为空数组）。 */
  transactions: Array<{
    id: string
    /** 'income' | 'expense' */
    type: string
    /** 正数；方向看 type。 */
    amount: number
    /** 餐饮 / 交通 / 购物 / 工资 / 项目收入 */
    category: string
  }>
  /** '' = 正文没命中金额，没入账；'created' = 本次新建；'existing' = 此前已记，未重复。 */
  bookkeeping: '' | 'created' | 'existing'
  /** 本次识别与已入账记录不一致（金额或收支方向）⇒ 金额以记账页为准。 */
  bookkeeping_mismatch: boolean
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
   *
   * 2026-10-07（§127）：返回值另含**自动记账**三键，见 NoteSummarizeBookkeeping。
   * 服务端没有给调用方开关，笔记正文命中金额就会入账 ⇒ 调用方有义务把它
   * 告诉用户，否则用户的账上会凭空多一笔而屏幕上一个字都不说。
   */
  summarize(
    id: string,
    signal?: AbortSignal,
  ): Promise<{ summary: string; model?: string; action_items?: NoteActionItem[] }
    & Partial<NoteSummarizeBookkeeping>
    & { summary_fallback?: boolean }> {
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
