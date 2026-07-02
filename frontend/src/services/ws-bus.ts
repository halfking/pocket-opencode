/**
 * ws-bus.ts — 🦞 WebSocket 事件集中路由层
 *
 * 职责：
 *   - 在 main.ts 启动时一次性把 wsClient 上需要监听的事件全部注册好
 *   - 每类事件路由到对应的 store / 业务模块
 *   - 避免散落在各个 view 里各自 wsClient.on/off
 *
 * 不引入新依赖；纯订阅/分发逻辑。
 */
import wsClient from '../api/websocket'
import * as notesStore from '../features/notes/notes-store'
import * as emailsStore from '../features/email/emails-store'

/** 服务器推送过来的 note.created 事件载荷（最小集）。 */
export interface ServerNotePayload {
  id: string
  workspace_id?: string | null
  title?: string | null
  content?: string
  content_type?: string
  domain?: string | null
  category?: string | null
  tags?: string[] | string | null
  created_at?: number
  updated_at?: number
}

/** 服务器推送过来的 email.classified 事件载荷。 */
export interface ServerEmailClassifiedPayload {
  email_id: string
  category?: string | null
  importance?: string | null
  summary?: string | null
}

let _initialized = false

/**
 * 把服务器推送的 note 字段规整成 LocalNote（兼容 snake_case / camelCase）。
 * 不写磁盘：本地已有这条笔记的原始数据，只更新内存/索引。
 */
function normalizeServerNote(p: ServerNotePayload): notesStore.LocalNote | null {
  if (!p || !p.id) return null
  const now = Date.now()
  const tags = Array.isArray(p.tags)
    ? p.tags
    : (typeof p.tags === 'string' && p.tags ? JSON.parse(p.tags) : null)
  return {
    id: p.id,
    workspaceId: p.workspace_id ?? null,
    title: p.title ?? null,
    content: p.content ?? '',
    contentType: p.content_type ?? 'text',
    domain: p.domain ?? null,
    category: p.category ?? null,
    tags,
    audioPath: null,
    audioDurationMs: 0,
    createdByVoice: false,
    createdAt: p.created_at ?? now,
    updatedAt: p.updated_at ?? now,
  }
}

/**
 * 启动 ws-bus。重复调用安全。
 * 在 main.ts 中调用一次即可。
 */
export function initWsBus(): void {
  if (_initialized) return
  _initialized = true

  // note.created — 后端完成笔记的归类/打标/索引后推回前端
  wsClient.on('note.created', async (payload: ServerNotePayload) => {
    try {
      const note = normalizeServerNote(payload)
      if (!note) return
      await notesStore.handleServerEvent(note)
    } catch (e) {
      console.warn('[ws-bus] note.created handler failed:', e)
    }
  })

  // email.classified — 后端完成邮件分类/重要度/摘要后推回前端
  wsClient.on('email.classified', async (payload: ServerEmailClassifiedPayload) => {
    try {
      if (!payload || !payload.email_id) return
      await emailsStore.handleClassifiedEvent({
        email_id: payload.email_id,
        category: payload.category ?? null,
        importance: payload.importance ?? null,
        summary: payload.summary ?? null,
      })
    } catch (e) {
      console.warn('[ws-bus] email.classified handler failed:', e)
    }
  })

  // vault.synced — 用户主动触发的云同步，无需本地响应（前端不感知）
  // 显式订阅是为了把已知事件集中到一处，便于排查
  wsClient.on('vault.synced', () => {
    // no-op: vault 同步由用户在 VaultListView 显式触发，结果回显在 UI
  })
}

/** 测试 / HMR 用：重置初始化标志。 */
export function _resetWsBusForTest(): void {
  _initialized = false
}