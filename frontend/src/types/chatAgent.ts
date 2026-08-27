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
