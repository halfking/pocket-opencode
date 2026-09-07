import { resolveApiBase } from '../config/api-base'
import { useAuthStore } from '../stores/auth'
import { ApiError, assertNotHTML } from './http'

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

  // 2xx 但为 HTML（如移动端漏注入 API base 时 Capacitor 返回 index.html）
  // 也要拦下，否则调用方 res.json() 只会抛难懂的 "Unexpected token"。
  return assertNotHTML(response)
}

export interface Task {
  id: string
  title: string
  description?: string
  status: string
  priority?: string
  workstreamId?: string
  source?: 'acc' | 'opencode' | 'local'
  category?: string
  createdAt?: string
  updatedAt?: string
  pendingApprovals?: number
  sessionCount?: number
  owner?: string
  /** UI-only: 实例显示名（TasksView 本地 enrich） */
  instanceName?: string
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
  timeUpdatedMs?: number
  instanceId?: string
  instanceName?: string
}

export interface MobileSessionListResponse {
  data: Session[]
  total: number
  sinceMs?: number
  serverTimeMs?: number
}

export interface MobileSessionSearchResponse {
  data: Array<Session & {
    ID?: string
    Title?: string
    Status?: string
    TimeUpdated?: number
    time?: { updated?: number }
  }>
  query: string
  total: number
}

export interface SessionLink {
  taskId: string
  instanceId: string
  sessionId: string
  role: string
}

export interface TaskSessionBundle {
  current: TaskSessionBundleRow[]
  historical: TaskSessionBundleRow[]
  localOnly: TaskSessionBundleRow[]
  usageTotals: { input: number; output: number; cache: number | null }
}

export interface TaskSessionBundleRow {
  id: string
  title: string
  lane: 'current' | 'historical' | 'local'
  agentKind?: string
  agentSessionId?: string
  gwSessionId?: string
  instanceId?: string
  role?: string
  startedAt?: string
  endedAt?: string
  tokensIn: number
  tokensOut: number
  tokensCache: number | null
}

export interface TaskSessionMessage {
  id: string
  ts: number
  type: string
  role: string
  name?: string
  text: string
}

export interface TaskSessionTranscript {
  session?: { id: string; title: string; kind?: string }
  messages: TaskSessionMessage[]
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
  async getTasks(
    instanceId?: string,
    opts: { workstreamId?: string; source?: 'acc' | 'opencode' | 'local' } = {},
  ): Promise<Task[]> {
    const url = new URL(`${resolveApiBase()}/api/tasks`, window.location.origin)
    if (instanceId) url.searchParams.set('instance_id', instanceId)
    if (opts.workstreamId) url.searchParams.set('workstream_id', opts.workstreamId)
    if (opts.source) url.searchParams.set('source', opts.source)
    const res = await authFetch(url.toString().replace(window.location.origin, ''))
    const data = await res.json()
    return data.tasks || []
  },

  async getTask(id: string): Promise<Task> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks/${id}`)
    return res.json()
  },

  async createTask(task: Partial<Task>): Promise<Task> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(task),
    })
    return res.json()
  },

  async updateTask(id: string, data: Partial<Task>): Promise<Task> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks/${id}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(data),
    })
    return res.json()
  },

  async deleteTask(id: string): Promise<void> {
    await authFetch(`${resolveApiBase()}/api/tasks/${id}`, {
      method: "DELETE",
    })
  },

  async getTaskSessions(taskId: string): Promise<SessionLink[]> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks/${taskId}/sessions`)
    const data = await res.json()
    return data.sessions || []
  },

  async getTaskSessionBundle(taskId: string): Promise<TaskSessionBundle> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks/${taskId}/session-bundle`)
    return res.json()
  },

  async getTaskSessionTranscript(taskId: string, sessionId: string, types?: string, kind?: string): Promise<TaskSessionTranscript> {
    const q = new URLSearchParams()
    if (types) q.set("types", types)
    if (kind) q.set("kind", kind)
    const qs = q.toString() ? `?${q.toString()}` : ""
    const res = await authFetch(`${resolveApiBase()}/api/tasks/${taskId}/sessions/${encodeURIComponent(sessionId)}/transcript${qs}`)
    return res.json()
  },

  async extractTaskSessionTitle(taskId: string, sessionId: string): Promise<{ title: string }> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks/${taskId}/sessions/${encodeURIComponent(sessionId)}/extract-title`, { method: "POST" })
    return res.json()
  },

  async summarizeTaskSession(taskId: string, sessionId: string): Promise<{ summary: string }> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks/${taskId}/sessions/${encodeURIComponent(sessionId)}/summarize`, { method: "POST" })
    return res.json()
  },

  async getInstances(): Promise<Instance[]> {
    const res = await authFetch(`${resolveApiBase()}/api/instances`)
    const data = await res.json()
    return data.instances || []
  },

  async getSessions(instanceBaseURL: string): Promise<Session[]> {
    const url = `${resolveApiBase()}/api/sessions/?instance=${encodeURIComponent(instanceBaseURL)}`
    const res = await authFetch(url)
    const data = await res.json()
    return data.sessions || []
  },

  async attachSession(taskId: string, instanceId: string, sessionId: string, role: string = "primary"): Promise<void> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks/${taskId}/attach-session`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ instanceId, sessionId, role }),
    })
  },

  async getModelConfig(instanceId: string): Promise<ModelConfig> {
    const res = await authFetch(`${resolveApiBase()}/api/config/models?instance_id=${instanceId}`)
    const data = await res.json()
    return data.config
  },

  async updateModelConfig(instanceId: string, config: ModelConfig): Promise<void> {
    const res = await authFetch(`${resolveApiBase()}/api/config/models?instance_id=${instanceId}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ config }),
    })
  },

  async reloadConfig(instanceId: string): Promise<void> {
    const res = await authFetch(`${resolveApiBase()}/api/config/reload?instance_id=${instanceId}`, {
      method: "POST",
    })
  },

  async testModel(instanceId: string, providerId: string, modelId: string): Promise<void> {
    const res = await authFetch(`${resolveApiBase()}/api/config/models/test?instance_id=${instanceId}`, {
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

    const res = await authFetch(`${resolveApiBase()}/api/sessions?${params}`)
    return res.json()
  },

  // 删除移动端会话
  async deleteSession(sessionId: string, instanceId: string): Promise<void> {
    const qs = new URLSearchParams({ instance_id: instanceId })
    await authFetch(
      `${resolveApiBase()}/api/mobile/sessions/${encodeURIComponent(sessionId)}?${qs}`,
      { method: 'DELETE' },
    )
  },

  /**
   * 中断会话当前 agent 循环（POST /api/mobile/sessions/:id/interrupt，
   * 服务端经 opencode adapter 调上游 /session/:id/abort）。
   * 指挥中心长按「停止」走这里（设计方案 v2 §4.2-4 的落地通道）。
   */
  async interruptSession(sessionId: string, instanceId: string): Promise<void> {
    const qs = new URLSearchParams({ instance_id: instanceId })
    await authFetch(
      `${resolveApiBase()}/api/mobile/sessions/${encodeURIComponent(sessionId)}/interrupt?${qs}`,
      { method: 'POST' },
    )
  },

  /**
   * 移动会话同步视图。与 GET /api/mobile/sessions 契约一致，返回
   * id/title/status/timeUpdatedMs；instance 是移动控制面必填边界。
   */
  async getMobileSessions(instanceId: string): Promise<MobileSessionListResponse> {
    const params = new URLSearchParams({ instance_id: instanceId })
    const res = await authFetch(`${resolveApiBase()}/api/mobile/sessions?${params}`)
    return res.json()
  },

  /** 服务端会话搜索（标题/ID 子串），仅在已选择 instance 时使用。 */
  async searchMobileSessions(instanceId: string, query: string): Promise<MobileSessionSearchResponse> {
    const params = new URLSearchParams({ instance_id: instanceId, q: query })
    const res = await authFetch(`${resolveApiBase()}/api/mobile/sessions/search?${params}`)
    return res.json()
  },

  // 附加会话到任务
  async attachSessionToTask(taskId: string, sessionId: string, instanceId: string): Promise<void> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks/${taskId}/attach-session`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        instanceId,
        sessionId,
        role: "primary"
      }),
    })
  },

  // ---- Phase 5: LLM Gateway 配置管理 ----
  /** 读当前 LLM Gateway 配置（API Key 已被后端掩码） */
  async getGatewayConfig(): Promise<GatewayConfig> {
    const res = await authFetch(`${resolveApiBase()}/api/llm-gateway/config`)
    return res.json()
  },

  /** 连通性测试：拉一次 /v1/models 验证 baseURL + apiKey */
  async testGateway(): Promise<GatewayTestResult> {
    const res = await authFetch(`${resolveApiBase()}/api/llm-gateway/test`, { method: 'POST' })
    return res.json()
  },

  /**
   * 保存配置：baseURL 必填；apiKey 留空表示保留旧值。
   * 后端会立即触发 OpenCode 配置热更新（PUT /config 或写文件 + reload）。
   */
  async saveGatewayConfig(body: {
    baseURL: string
    apiKey?: string
    models?: string[]
    format?: string
    preferredModels?: string[]
  }): Promise<{ ok: boolean; baseURL: string; models: string[] }> {
    const res = await authFetch(`${resolveApiBase()}/api/llm-gateway/config`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })
    return res.json()
  },

  /** 读已缓存的模型列表 */
  async getGatewayModels(): Promise<{ baseURL: string; models: string[] }> {
    const res = await authFetch(`${resolveApiBase()}/api/llm-gateway/models`)
    return res.json()
  },

  // ---- Biometric (WebAuthn) ----
  /** 列出当前用户已注册的生物识别（指纹/人脸）凭据 */
  async listBiometricCredentials(): Promise<BiometricCredentialMeta[]> {
    const res = await authFetch(`${resolveApiBase()}/api/auth/biometric/credentials`)
    const body = await res.json()
    return Array.isArray(body?.credentials) ? body.credentials : []
  },
}

// ---- Phase 5: LLM Gateway 类型 ----
export interface GatewayConfig {
  baseURL: string
  apiKeySet: boolean
  apiKey: string          // 后端掩码后字符串，如 sk-****5678
  models: string[]
  source: 'pocketd'
  /** 网关调用协议（llm-gateway-go 端点族；openai-chat 为默认且当前唯一实现） */
  format?: string
  /** 用户勾选的常用模型；非空时模型选择器只显示这些 */
  preferredModels?: string[]
  /** 服务端支持下拉框选项（GET /config 返回） */
  formats?: string[]
}

export interface GatewayTestResult {
  ok: boolean
  status?: number
  models?: string[]
  error?: string
  response?: string
}

// ---- Biometric (WebAuthn) ----
export interface BiometricCredentialMeta {
  id: string
  /** 用户给凭据起的别名（"我的 Pixel" / "公司手机"） */
  name?: string
  createdAt?: string
  lastUsedAt?: string
}
