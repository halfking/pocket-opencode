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
  { value: 'cron', label: 'Cron', hint: '5 字段，例如 0 9 * * 1-5' },
  { value: 'interval', label: '间隔', hint: 'Go duration，例如 30m、6h' },
  { value: 'at', label: '一次性', hint: 'RFC3339，例如 2026-09-01T09:00:00Z' },
]

export const TASK_KINDS: Array<{ value: ScheduledTaskKind; label: string }> = [
  { value: 'redclaw_chat', label: 'RedClaw 对话' },
  { value: 'redclaw_knowledge', label: 'RedClaw 知识库' },
  { value: 'agent_bridge', label: 'Agent Bridge' },
  { value: 'llmbff_summary', label: 'LLM 摘要' },
  { value: 'kxmemory_summary', label: 'KXMemory 摘要' },
  { value: 'acc_mcp', label: 'ACC MCP' },
  { value: 'webhook', label: 'Webhook' },
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
