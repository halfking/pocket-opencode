/**
 * BUG-D 修复（2026-09-30 真机验收）：本文件原先直接用裸 `resolveApiBase()`，
 * 而 `api/http.ts` 用的是带兜底的 `resolveRuntimeApiBase()`。两条 API 调用
 * 路径解析 base 的方式不一致，导致：
 *   - 构建缺少 VITE_API_BASE 时，http.ts 的请求回退到生产入口
 *     https://pocket.itestu.cn，client.ts 的请求却落到同源 ''，
 *     在 Capacitor 里就是 WebView 自己的 https://localhost，
 *     /api 返回本地 index.html（HTML 而非 JSON）。
 * 即测试机上会出现「一半请求打到生产、一半打到本地壳」的分裂行为。
 *
 * 这里统一到 resolveRuntimeApiBase：与 http.ts 走同一个解析入口，
 * Capacitor 源且解析为空时回退生产入口（api-base.ts:104 的既有约定）。
 * 保留本地名 `resolveApiBase` 以免改动全部调用点。
 */
import { resolveRuntimeApiBase as resolveApiBase } from '../config/api-base'
import { useAuthStore } from '../stores/auth'
import { ApiError, assertNotHTML, forceReauth } from './http'
import { buildTasksUrl, type TaskListFilters } from './tasks-url.ts'

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
    // BUG-AX（2026-10-01 13:05 真机实测）：401 必须走 forceReauth，
    // 否则调用方只会拿到一个 ApiError 并把它吞掉。实测后果是
    // 「后端换了 JWT secret → 设备上旧 token 全 401 → 任务页 catch 后
    //   把 tasks 置空 → 页面显示『暂无运行中的任务』『全部正常 · 0』，
    //   既不报错也不跳登录」，用户拿着死 token 卡死却看不出发生了什么。
    // 这正是 http.ts 里 BUG-I 描述的死法；那次修复只落在 http() 这条链上，
    // 而本文件整个面（任务/会话/实例…）都走 authFetch，全部绕过了兜底。
    if (response.status === 401) forceReauth()
    throw new ApiError(response.status, message)
  }

  // 2xx 但为 HTML（如移动端漏注入 API base 时 Capacitor 返回 index.html）
  // 也要拦下，否则调用方 res.json() 只会抛难懂的 "Unexpected token"。
  return assertNotHTML(response)
}

/**
 * Work = task: one entity, classified by `type` (docs/学习muse/03-架构方案.md §1).
 * The previous `category` / `owner` fields were declared here but never
 * returned by the backend — any UI built on them silently rendered blank. They
 * are replaced by the fields the server actually stores and validates.
 */
export type TaskTypeGroup = 'work' | 'life' | 'learning' | 'other'

/** 协作角色，与 backend/internal/task/workitem.go 的 Role* 常量一致。 */
export type TaskParticipantRole = 'owner' | 'assignee' | 'watcher'

export interface TaskParticipant {
  userId: string
  role: TaskParticipantRole
  createdAt?: number
}

/**
 * 目标的派生进度（不落库，由子任务状态聚合）。total 为 0 表示这条不是目标，
 * 此时 percent 恒为 0 —— 没有子任务不是「全部完成」。
 */
export interface GoalProgress {
  parentId: string
  total: number
  done: number
  percent: number
  blocked: number
}

/** 审批投影：agent 上游的审批请求在任务域的只读视图。 */
export interface TaskApproval {
  instanceId: string
  sessionId: string
  requestId: string
  kind: 'permission' | 'question'
  state: string
  decision?: string
  version: number
  createdAt: number
  updatedAt: number
}

/**
 * 活动流条目。payload 是服务端原样透传的 JSON，本组件只读 comment/status，
 * 所以这里刻意保持宽松——新增事件类型不该让前端编译失败。
 */
export interface WorkItemEvent {
  workspaceId?: string
  taskId: string
  eventId: string
  eventType: string
  actorUserId?: string
  payload?: {
    comment?: string
    status?: string
    userId?: string
    taskTitle?: string
    childId?: string
    childTitle?: string
  } | null
  createdAt: number
}

export interface Task {
  id: string
  title: string
  description?: string
  status: string
  priority?: string
  workstreamId?: string
  source?: 'acc' | 'opencode' | 'local'
  /** Closed enum; the server rejects anything else with 400. Empty = 'other'. */
  type?: string
  /** Server-derived fold group for the type above. */
  typeGroup?: TaskTypeGroup
  /** Accountable person (user id). Defaults to the task owner. */
  ownerId?: string
  /** Collaborator user ids. */
  assignees?: string[]
  /** Unix seconds. */
  dueAt?: number
  remindAt?: number
  parentId?: string
  /** note | email | rss | meeting | agent | manual | import */
  originKind?: string
  originRef?: string
  tags?: string[]
  visibility?: 'private' | 'shared' | 'workspace'
  createdAt?: string
  updatedAt?: string
  pendingApprovals?: number
  sessionCount?: number
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
  async getTasks(instanceId?: string, opts: TaskListFilters = {}): Promise<Task[]> {
    const res = await authFetch(buildTasksUrl(resolveApiBase(), instanceId, opts))
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

  /**
   * 来源 → 任务：把一条笔记 / 邮件 / RSS / 会议转成工作项，带 origin 溯源。
   * 会议会按 action item 展开成多条，重复调用按 (originRef, title) 幂等。
   */
  async createTaskFromSource(input: {
    sourceKind: "note" | "email" | "rss" | "meeting"
    sourceId: string
    type?: string
    title?: string
  }): Promise<{ tasks: Task[]; skipped?: number }> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks/from-source`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    })
    if (!res.ok) throw new Error(`createTaskFromSource failed: ${res.status}`)
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

  // ---- 协作（P3，docs/学习muse/03-架构方案.md §4）----
  // 注意 `/api/tasks/{id}/events` 是 ACC 运行事件，协作活动流叫 `/activity`；
  // `/api/tasks/delegate` 是「经 ACC 建任务」，委派到人走 `{id}/delegate`。

  async getTaskParticipants(taskId: string): Promise<TaskParticipant[]> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks/${taskId}/participants`)
    const data = await res.json()
    return data.participants || []
  },

  async setTaskParticipants(taskId: string, participants: TaskParticipant[]): Promise<TaskParticipant[]> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks/${taskId}/participants`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ participants }),
    })
    const data = await res.json()
    return data.participants || []
  },

  async getTaskActivity(taskId: string, limit = 50): Promise<WorkItemEvent[]> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks/${taskId}/activity?limit=${limit}`)
    const data = await res.json()
    return data.events || []
  },

  async postTaskComment(taskId: string, comment: string, eventId?: string): Promise<WorkItemEvent> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks/${taskId}/activity`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ comment, eventId }),
    })
    const data = await res.json()
    return data.event
  },

  async delegateTask(taskId: string, userId: string, role: TaskParticipantRole = "assignee"): Promise<{ participants: TaskParticipant[]; ownerId: string }> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks/${taskId}/delegate`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ userId, role }),
    })
    return res.json()
  },

  // ---- 目标 → 子任务层级 + 审批读取（P3 剩余）----

  async getTaskChildren(taskId: string): Promise<{ children: Task[]; progress: GoalProgress }> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks/${taskId}/children`)
    return res.json()
  },

  async createSubtask(taskId: string, body: { title: string; type?: string; dueAt?: number; description?: string }): Promise<Task> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks/${taskId}/subtasks`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    })
    if (!res.ok) throw new Error(await res.text())
    return res.json()
  },

  async getTaskApprovals(taskId: string): Promise<{ approvals: TaskApproval[]; pending: number }> {
    const res = await authFetch(`${resolveApiBase()}/api/tasks/${taskId}/approvals`)
    return res.json()
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
