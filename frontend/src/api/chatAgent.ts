import { http } from './http'
import type {
  ChatAgent,
  ChatAgentListResponse,
  ChatAgentCreateRequest,
  ChatAgentUpdateRequest,
  SyncPayload,
  SyncResult,
  SyncStatus,
} from '../types/chatAgent'

export const chatAgentApi = {
  /**
   * 列出所有角色（内置 + 当前 workspace 的自定义角色）
   * @param department 可选部门筛选（如 'engineering'）
   */
  async list(department?: string): Promise<ChatAgent[]> {
    const params = department ? `?department=${encodeURIComponent(department)}` : ''
    const res = await http<ChatAgentListResponse>(`/api/chat-agents${params}`)
    return res.agents || []
  },

  /**
   * 获取单个角色详情
   */
  async get(id: string): Promise<ChatAgent> {
    return http<ChatAgent>(`/api/chat-agents/${id}`)
  },

  /**
   * 创建自定义角色
   */
  async create(req: ChatAgentCreateRequest): Promise<ChatAgent> {
    return http<ChatAgent>('/api/chat-agents', {
      method: 'POST',
      body: JSON.stringify(req),
    })
  },

  /**
   * 更新自定义角色（仅 isBuiltin=false）
   */
  async update(id: string, req: ChatAgentUpdateRequest): Promise<ChatAgent> {
    return http<ChatAgent>(`/api/chat-agents/${id}`, {
      method: 'PUT',
      body: JSON.stringify(req),
    })
  },

  /**
   * 删除自定义角色（仅 isBuiltin=false）
   */
  async delete(id: string): Promise<void> {
    await http(`/api/chat-agents/${id}`, { method: 'DELETE' })
  },

  // ────────────────────────────────────────────────────────────────────────
  // Acc 云端同步（PG 启用时才可用；未启用时端点返回 503）
  // ────────────────────────────────────────────────────────────────────────

  /**
   * 上传自定义角色列表到 Acc 云端。
   * @throws ApiError 409 当本地 version 早于服务端版本（冲突）
   */
  async syncUpload(payload: SyncPayload): Promise<SyncResult> {
    return http<SyncResult>('/api/chat-agents/sync/upload', {
      method: 'POST',
      body: JSON.stringify(payload),
    })
  },

  /**
   * 从 Acc 云端拉取自定义角色列表。
   * 服务端无记录时返回 version=0 + 空 agents。
   */
  async syncDownload(): Promise<SyncPayload> {
    return http<SyncPayload>('/api/chat-agents/sync/download')
  },

  /**
   * 查询同步状态（用于 UI 角标显示）
   */
  async syncStatus(): Promise<SyncStatus> {
    return http<SyncStatus>('/api/chat-agents/sync/status')
  },
}
