export type ScheduleKind = 'cron' | 'interval' | 'at'

export type ScheduledTaskKind =
  | 'redclaw_chat'
  | 'redclaw_knowledge'
  | 'agent_bridge'
  | 'llmbff_summary'
  | 'kxmemory_summary'
  | 'acc_mcp'
  | 'webhook'

export type RunStatus = 'running' | 'success' | 'failed' | 'skipped' | string

export interface ScheduledTask {
  id: string
  workspaceId?: string
  userId?: string
  name: string
  description?: string
  kind: ScheduledTaskKind | string
  scheduleKind: ScheduleKind
  scheduleExpr: string
  timezone: string
  payload: unknown
  enabled: boolean
  nextRunAt: number
  lastRunAt: number
  lastStatus?: RunStatus
  lastError?: string
  runCount: number
  maxRuns: number
  cooldownSec: number
  timeoutSec: number
  createdAt: number
  updatedAt: number
}

export interface ScheduledTaskInput {
  name: string
  description?: string
  kind: ScheduledTaskKind | string
  scheduleKind: ScheduleKind
  scheduleExpr: string
  timezone: string
  payload: unknown
  enabled?: boolean
  maxRuns?: number
  cooldownSec?: number
  timeoutSec?: number
}

export interface ScheduledTaskRun {
  id: string
  taskId: string
  workspaceId?: string
  userId?: string
  status: RunStatus
  startedAt: number
  finishedAt: number
  durationMs: number
  output?: unknown
  error?: string
  referencedTaskId?: string
}

export interface SchedulePreview {
  next: number[]
}

export const SCHEDULE_KINDS: Array<{ value: ScheduleKind; label: string; hint: string }> = [
  { value: 'cron', label: '周期性', hint: '按天、工作日、每周或每月重复' },
  { value: 'interval', label: '每隔一段时间', hint: '按分钟、小时或天重复' },
  { value: 'at', label: '一次性', hint: '到选定的日期时间执行一次' },
]

export const TASK_KINDS: Array<{ value: ScheduledTaskKind; label: string }> = [
  { value: 'redclaw_chat', label: '智能对话' },
  { value: 'redclaw_knowledge', label: '知识库检索' },
  { value: 'agent_bridge', label: '专家助手' },
  { value: 'llmbff_summary', label: 'AI 摘要' },
  { value: 'kxmemory_summary', label: '记忆摘要' },
  { value: 'acc_mcp', label: '系统工具' },
  { value: 'webhook', label: '外部通知' },
]

export function taskKindLabel(kind: string): string {
  return TASK_KINDS.find((item) => item.value === kind)?.label || kind
}

export function scheduleKindLabel(kind: ScheduleKind): string {
  return SCHEDULE_KINDS.find((item) => item.value === kind)?.label || kind
}

export function formatTimestamp(seconds: number): string {
  if (!seconds) return '从未'
  return new Date(seconds * 1000).toLocaleString()
}

export function formatPayload(payload: unknown): string {
  if (typeof payload === 'string') return payload
  try { return JSON.stringify(payload ?? {}, null, 2) } catch { return String(payload) }
}
