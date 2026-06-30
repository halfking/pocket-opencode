import type { PocketTaskSummary } from "../../../../shared/schema"

const API_BASE = import.meta.env.VITE_API_BASE || ""

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
    const res = await fetch(url.toString().replace(window.location.origin, ""))
    if (!res.ok) throw new Error(`Failed to fetch tasks: ${res.statusText}`)
    const data = await res.json()
    return data.tasks || []
  },

  async getTask(id: string): Promise<Task> {
    const res = await fetch(`${API_BASE}/api/tasks/${id}`)
    if (!res.ok) throw new Error(`Failed to fetch task: ${res.statusText}`)
    return res.json()
  },

  async createTask(task: Partial<Task>): Promise<Task> {
    const res = await fetch(`${API_BASE}/api/tasks`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(task),
    })
    if (!res.ok) throw new Error(`Failed to create task: ${res.statusText}`)
    return res.json()
  },

  async getTaskSessions(taskId: string): Promise<SessionLink[]> {
    const res = await fetch(`${API_BASE}/api/tasks/${taskId}/sessions`)
    if (!res.ok) throw new Error(`Failed to fetch task sessions: ${res.statusText}`)
    const data = await res.json()
    return data.sessions || []
  },

  async getInstances(): Promise<Instance[]> {
    const res = await fetch(`${API_BASE}/api/instances`)
    if (!res.ok) throw new Error(`Failed to fetch instances: ${res.statusText}`)
    const data = await res.json()
    return data.instances || []
  },

  async getSessions(instanceBaseURL: string): Promise<Session[]> {
    const url = `${API_BASE}/api/sessions/?instance=${encodeURIComponent(instanceBaseURL)}`
    const res = await fetch(url)
    if (!res.ok) throw new Error(`Failed to fetch sessions: ${res.statusText}`)
    const data = await res.json()
    return data.sessions || []
  },

  async attachSession(taskId: string, instanceId: string, sessionId: string, role: string = "primary"): Promise<void> {
    const res = await fetch(`${API_BASE}/api/tasks/${taskId}/attach-session`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ instanceId, sessionId, role }),
    })
    if (!res.ok) throw new Error(`Failed to attach session: ${res.statusText}`)
  },

  async getModelConfig(instanceId: string): Promise<ModelConfig> {
    const res = await fetch(`${API_BASE}/api/config/models?instance_id=${instanceId}`)
    if (!res.ok) throw new Error(`Failed to get config: ${res.statusText}`)
    const data = await res.json()
    return data.config
  },

  async updateModelConfig(instanceId: string, config: ModelConfig): Promise<void> {
    const res = await fetch(`${API_BASE}/api/config/models?instance_id=${instanceId}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ config }),
    })
    if (!res.ok) throw new Error(`Failed to update config: ${res.statusText}`)
  },

  async reloadConfig(instanceId: string): Promise<void> {
    const res = await fetch(`${API_BASE}/api/config/reload?instance_id=${instanceId}`, {
      method: "POST",
    })
    if (!res.ok) throw new Error(`Failed to reload config: ${res.statusText}`)
  },

  async testModel(instanceId: string, providerId: string, modelId: string): Promise<void> {
    const res = await fetch(`${API_BASE}/api/config/models/test?instance_id=${instanceId}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ providerId, modelId }),
    })
    if (!res.ok) throw new Error(`Failed to test model: ${res.statusText}`)
  },

  // 新增：获取所有会话列表（支持过滤和分页）
  async getAllSessions(instanceId?: string, limit = 20, offset = 0): Promise<{ sessions: Session[], total: number, limit: number, offset: number }> {
    const params = new URLSearchParams()
    if (instanceId) params.append('instance_id', instanceId)
    params.append('limit', limit.toString())
    params.append('offset', offset.toString())
    
    const res = await fetch(`${API_BASE}/api/sessions?${params}`)
    if (!res.ok) throw new Error(`Failed to fetch all sessions: ${res.statusText}`)
    return res.json()
  },

  // 新增：附加会话到任务
  async attachSessionToTask(taskId: string, sessionId: string, instanceId: string): Promise<void> {
    const res = await fetch(`${API_BASE}/api/tasks/${taskId}/attach-session`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ 
        instanceId, 
        sessionId, 
        role: "primary" 
      }),
    })
    if (!res.ok) throw new Error(`Failed to attach session to task: ${res.statusText}`)
  },
}
