import { http } from './http'
import type {
  ChatAgent,
  ChatAgentListResponse,
  ChatAgentCreateRequest,
  ChatAgentUpdateRequest,
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
}
