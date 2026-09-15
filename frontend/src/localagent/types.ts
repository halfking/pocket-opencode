/**
 * localagent/types.ts — 手机端内置本地智能体的核心类型。
 *
 * 语义移植自 pi(packages/agent/src/types.ts 的 AgentTool / AgentLoopConfig、
 * packages/coding-agent/src/core/skills.ts 的 Skill、subagent 扩展的专家 markdown
 * 定义),针对 WebView 环境裁剪:无 TypeBox(降级为 JSON Schema 对象)、无
 * ExecutionEnv(工具直接闭包注入)、LLM 走提示词驱动 JSON 工具协议(见
 * tool-protocol.ts 头注释)。
 *
 * 本目录全部模块保持「无 Vue / 无 Pinia / 无 Capacitor 顶层 import」,
 * 使 node --test 可直接加载(Node 22 类型剥离);平台能力一律经
 * deps 注入或动态 import。
 */

// ---------------------------------------------------------------------------
// 工具
// ---------------------------------------------------------------------------

/** 工具风险分级:low 自动放行;medium/high 需用户审批(openhands confirmation 风格)。 */
export type ToolRisk = 'low' | 'medium' | 'high'

/** 工具执行上下文:循环注入,工具不得越界访问。 */
export interface ToolContext {
  signal: AbortSignal
  /** 任务级事件出口:工具可发中间进度(目前 task_plan 用)。 */
  emit?: (evt: AgentEvent) => void
}

/**
 * AgentTool — 对齐 pi 的 ToolDefinition 精简版。
 * parameters 用 JSON Schema(仅描述,不做强校验;协议层有轻量 args 校验)。
 */
export interface AgentTool {
  name: string
  label: string
  description: string
  /** system prompt 中的额外使用提示(pi promptSnippet 语义)。 */
  promptSnippet?: string
  parameters: {
    type: 'object'
    properties: Record<string, { type: string; description?: string; enum?: string[] }>
    required?: string[]
  }
  risk: ToolRisk
  execute(args: Record<string, unknown>, ctx: ToolContext): Promise<ToolOutcome>
}

/** 工具执行结果:ok 时 result 为给模型看的文本;error 时 error 为可读原因。 */
export interface ToolOutcome {
  ok: boolean
  /** 给模型回灌的文本(截断由循环层负责)。 */
  result?: string
  error?: string
  /** UI 侧结构化展示数据(可选,如 task_plan 的条目)。 */
  data?: unknown
}

// ---------------------------------------------------------------------------
// 技能 / 专家
// ---------------------------------------------------------------------------

/** Skill — Agent Skills 标准(pi skills.ts 同款字段)。 */
export interface Skill {
  name: string
  description: string
  /** markdown 正文(load_skill 时全文回灌)。 */
  body: string
}

/** Expert — pi subagent 扩展的 markdown 定义(frontmatter + 正文即 system prompt)。 */
export interface Expert {
  name: string
  description: string
  /** 专家专属 system prompt(markdown 正文)。 */
  systemPrompt: string
  /** 工具白名单;缺省 = 全部内置工具。 */
  allowedTools?: string[]
}

// ---------------------------------------------------------------------------
// 事件(时间线数据源,openhands 事件流精简版)
// ---------------------------------------------------------------------------

export type RunStatus =
  | 'idle'
  | 'thinking'
  | 'tool_running'
  | 'waiting_approval'
  | 'done'
  | 'error'
  | 'aborted'

export type AgentEvent =
  | { type: 'run_started'; sessionId: string; expert: string; skills: string[] }
  | { type: 'status'; status: RunStatus }
  | { type: 'text_delta'; text: string }
  | { type: 'assistant'; text: string; interim?: boolean }
  | {
      type: 'tool_call'
      id: string
      name: string
      args: Record<string, unknown>
      state: 'running' | 'completed' | 'error' | 'denied'
      result?: string
      error?: string
      data?: unknown
      durationMs?: number
      risk: ToolRisk
    }
  | { type: 'approval_required'; toolCallId: string; tool: string; args: Record<string, unknown>; risk: ToolRisk }
  | { type: 'plan'; items: PlanItem[] }
  | { type: 'usage'; promptTokens: number; completionTokens: number }
  | { type: 'run_done'; steps: number; reason: 'completed' | 'max_steps' | 'aborted' }
  | { type: 'run_error'; error: string }

export interface PlanItem {
  title: string
  notes?: string
  status: 'todo' | 'in_progress' | 'done'
}

// ---------------------------------------------------------------------------
// 会话消息(发往 LLM 的上下文;工具结果用 <tool_result> 标记包在 user 消息里)
// ---------------------------------------------------------------------------

export interface AgentChatMessage {
  role: 'system' | 'user' | 'assistant'
  content: string
}

// ---------------------------------------------------------------------------
// 循环配置(对齐 pi AgentLoopConfig 精简版)
// ---------------------------------------------------------------------------

/** LLM 流式函数:返回完整回合文本。失败时 reject(循环层转 run_error)。 */
export type StreamFn = (input: {
  messages: AgentChatMessage[]
  signal: AbortSignal
  onDelta?: (text: string) => void
}) => Promise<{ text: string; usage?: { promptTokens: number; completionTokens: number } }>

/** 审批闸门:返回 true 放行。medium/high 工具在执行前被 await。 */
export type ApprovalGate = (req: {
  toolCallId: string
  tool: string
  args: Record<string, unknown>
  risk: ToolRisk
}) => Promise<boolean>

export interface AgentLoopOptions {
  systemPrompt: string
  /** 会话历史(不含本次 prompt;循环不改写传入数组)。 */
  history: AgentChatMessage[]
  prompt: string
  tools: AgentTool[]
  streamFn: StreamFn
  approvalGate?: ApprovalGate
  onEvent: (evt: AgentEvent) => void
  signal: AbortSignal
  maxSteps?: number
  /** 单个工具结果回灌 LLM 的最大字符数。 */
  toolResultLimit?: number
}
