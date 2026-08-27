// ChatAgent 类型定义 — AI 对话智能体角色
export interface ChatAgent {
  id: string                  // 角色唯一标识（如 'engineering-ai-engineer'）
  workspace_id: string        // 所属 workspace；内置角色为空字符串（全局共享）
  name: string                // 角色名称（如 'AI 工程师'）
  description: string         // 角色简介
  department: string          // 所属部门（如 'engineering'）
  emoji?: string              // 角色 emoji（如 '🤖'）
  color?: string              // 角色主题色（如 'purple'）
  system_prompt: string       // 完整角色设定（Markdown 正文）
  is_builtin: boolean         // 是否为内置角色（true=不可修改/删除）
  created_at: number
  updated_at: number
}

export interface ChatAgentListResponse {
  agents: ChatAgent[]
}

export interface ChatAgentCreateRequest {
  id?: string
  name: string
  description: string
  department: string
  emoji?: string
  color?: string
  system_prompt: string
}

export interface ChatAgentUpdateRequest {
  name?: string
  description?: string
  department?: string
  emoji?: string
  color?: string
  system_prompt?: string
}

// ────────────────────────────────────────────────────────────────────────
// Acc 云端同步（PG 启用时）
// ────────────────────────────────────────────────────────────────────────

export interface SyncPayload {
  /** 本地已知的服务端版本号；首次上传或未知时可传 0 */
  version: number
  /** 要上传的自定义角色列表（仅 is_builtin=false） */
  agents: ChatAgent[]
}

export interface SyncResult {
  /** 服务端新版本号（毫秒时间戳） */
  version: number
  /** 本次上传的角色数（已过滤掉内置） */
  uploaded_count: number
  /** 是否发生版本冲突 */
  conflict: boolean
  /** 冲突时返回服务端当前版本（仅 conflict=true 时有效） */
  server_version?: number
  /** 被跳过的角色 id 列表（仅冲突时） */
  skipped_ids?: string[]
}

export interface SyncStatus {
  /** 是否有云端记录 */
  has_remote: boolean
  /** 服务端版本号 */
  server_version: number
  /** 服务端最后更新时间（毫秒时间戳） */
  server_updated_at: number
  /** 服务端角色数量 */
  agent_count: number
}
