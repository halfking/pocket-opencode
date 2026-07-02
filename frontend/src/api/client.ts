import { useAuthStore } from '../stores/auth'
import { ApiError } from './http'

const API_BASE = import.meta.env.VITE_API_BASE || ""

/**
 * fetch 包装：注入 Bearer token + 统一错误处理。
 * 
 * 旧 client.ts 直接裸 fetch，导致这批接口永远不带 Authorization 头。
 * 第五轮修复：统一注入 token。
 * 第六轮优化：包装响应错误为 ApiError（与 http.ts 一致），便于调用方处理。
 */
async function authFetch(input: string, init: RequestInit = {}): Promise<Response> {
  const auth = useAuthStore()
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(init.headers as Record<string, string> | undefined),
  }
  if (auth.token) headers["Authorization"] = `Bearer ${auth.token}`
  
  const response = await fetch(input, { ...init, headers })
  
  // 非 2xx 响应抛 ApiError（与 http.ts 行为一致）
  if (!response.ok) {
    let message = response.statusText
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch {
      // 响应不是 JSON，用 statusText
    }
    throw new ApiError(response.status, message)
  }
  
  return response
}

export interface Task {
  id: string
  title: string
  description?: string
  status: string
  priority?: string
  workstreamId?: string
  createdAt?: string
  updatedAt?: string
  pendingApprovals?: number
  sessionCount?: number
  owner?: string
}

export interface Instance {
  id: string
  displayName: string
  environment: string
  npsClientId: number
  capabilities: string[]
  health: string
  lastHeartbeatAt: string
}

export interface Session {
  id: string
  title: string
  status: string
}

export interface SessionLink {
  taskId: string
  instanceId: string
  sessionId: string
  role: string
}

export interface ModelConfig {
  providers: Provider[]
  defaultProvider?: string
  timeout?: number
}

export interface Provider {
  id: string
  name: string
  enabled: boolean
  apiKey?: string
  baseURL?: string
  models: ModelDefinition[]
  priority?: number
}

export interface ModelDefinition {
  id: string
  displayName: string
  enabled: boolean
  maxTokens?: number
  temperature?: number
  contextWindow?: number
  pricing?: {
    input: number
    output: number
  }
}

export const api = {
  async getTasks(instanceId?: string): Promise<Task[]> {
    const url = new URL(`${API_BASE}/api/tasks`, window.location.origin)
    if (instanceId) url.searchParams.set("instance_id", instanceId)
    const res = await authFetch(url.toString().replace(window.location.origin, ""))
    const data = await res.json()
    return data.tasks || []
  },

  async getTask(id: string): Promise<Task> {
    const res = await authFetch(`${API_BASE}/api/tasks/${id}`)
    return res.json()
  },

  async createTask(task: Partial<Task>): Promise<Task> {
    const res = await authFetch(`${API_BASE}/api/tasks`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(task),
    })
    return res.json()
  },

  async getTaskSessions(taskId: string): Promise<SessionLink[]> {
    const res = await authFetch(`${API_BASE}/api/tasks/${taskId}/sessions`)
    const data = await res.json()
    return data.sessions || []
  },

  async getInstances(): Promise<Instance[]> {
    const res = await authFetch(`${API_BASE}/api/instances`)
    const data = await res.json()
    return data.instances || []
  },

  async getSessions(instanceBaseURL: string): Promise<Session[]> {
    const url = `${API_BASE}/api/sessions/?instance=${encodeURIComponent(instanceBaseURL)}`
    const res = await authFetch(url)
    const data = await res.json()
    return data.sessions || []
  },

  async attachSession(taskId: string, instanceId: string, sessionId: string, role: string = "primary"): Promise<void> {
    const res = await authFetch(`${API_BASE}/api/tasks/${taskId}/attach-session`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ instanceId, sessionId, role }),
    })
  },

  async getModelConfig(instanceId: string): Promise<ModelConfig> {
    const res = await authFetch(`${API_BASE}/api/config/models?instance_id=${instanceId}`)
    const data = await res.json()
    return data.config
  },

  async updateModelConfig(instanceId: string, config: ModelConfig): Promise<void> {
    const res = await authFetch(`${API_BASE}/api/config/models?instance_id=${instanceId}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ config }),
    })
  },

  async reloadConfig(instanceId: string): Promise<void> {
    const res = await authFetch(`${API_BASE}/api/config/reload?instance_id=${instanceId}`, {
      method: "POST",
    })
  },

  async testModel(instanceId: string, providerId: string, modelId: string): Promise<void> {
    const res = await authFetch(`${API_BASE}/api/config/models/test?instance_id=${instanceId}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ providerId, modelId }),
    })
  },

  // 新增：获取所有会话列表（支持过滤和分页）
  async getAllSessions(instanceId?: string, limit = 20, offset = 0): Promise<{ sessions: Session[], total: number, limit: number, offset: number }> {
    const params = new URLSearchParams()
    if (instanceId) params.append('instance_id', instanceId)
    params.append('limit', limit.toString())
    params.append('offset', offset.toString())
    
    const res = await authFetch(`${API_BASE}/api/sessions?${params}`)
    return res.json()
  },

  // 新增：附加会话到任务
  async attachSessionToTask(taskId: string, sessionId: string, instanceId: string): Promise<void> {
    const res = await authFetch(`${API_BASE}/api/tasks/${taskId}/attach-session`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ 
        instanceId, 
        sessionId, 
        role: "primary" 
      }),
    })
  },
}
