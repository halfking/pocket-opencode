/**
 * agents.ts — S0-D Agent Bridge API client.
 *
 * 对接后端 /api/agents 子树：
 *   GET    /api/agents              列出当前 workspace 的 agent
 *   POST   /api/agents              注册新 agent（绑定到 instance_id）
 *   GET    /api/agents/{id}         详情
 *   POST   /api/agents/{id}/send    发 prompt（创建 session + 自动 attach task）
 *   DELETE /api/agents/{id}         注销
 */
import { http } from './http'

export interface Agent {
  id: string
  workspace_id: string
  instance_id: string
  name: string
  role: 'generic' | 'planner' | 'developer' | 'tester' | 'reviewer'
  status: 'unknown' | 'online' | 'offline' | 'busy'
  capabilities: string[]
  created_at: number
  updated_at: number
}

export interface SendResult {
  agent_id: string
  instance_id: string
  session_id: string
  task_id?: string
  attached: boolean
}

export const agentsApi = {
  list: () => http<{ agents: Agent[] }>('/api/agents').then((r) => r.agents),

  create: (input: {
    instance_id: string
    name: string
    role?: Agent['role']
    capabilities?: string[]
  }) =>
    http<Agent>('/api/agents', {
      method: 'POST',
      body: JSON.stringify(input),
    }),

  get: (id: string) => http<Agent>(`/api/agents/${id}`),

  send: (
    id: string,
    input: {
      prompt: string
      task_id?: string
      role?: string
      agent?: string
      model_id?: string
      provider_id?: string
      directory?: string
    },
  ) =>
    http<SendResult>(`/api/agents/${id}/send`, {
      method: 'POST',
      body: JSON.stringify(input),
    }),

  delete: (id: string) =>
    http<{ deleted: string }>(`/api/agents/${id}`, { method: 'DELETE' }),
}
