/**
 * sessionEvents.ts — P1 会话/任务事件解析（纯函数，Node 可测）。
 *
 * 冻结契约见 docs/2026-08-27-p1-contracts-frozen.md §2；本文件是 TS 侧唯一
 * 类型定义，Go 侧镜像在 backend/internal/opencode/session_event_broadcaster.go
 * （注释互指，两侧 fixture 测试锁定序列化形状，参照 approvalEvents.ts 模式）。
 *
 * 服务端把 WsEnvelopeV1 包在 WS hub 的 {type, payload} 通用信封里下发；
 * idempotentWsBus 归一化后订阅回调拿到 env，其中 env.data 即 WsEnvelopeV1，
 * env.data.data 才是业务 payload（与 parseApprovalEvent 同构，双层解包）。
 *
 * 本模块不 import 任何运行时依赖，保持可在 node --test 下直接测试。
 */

/** session.activity 的阶段（§4.3 状态条 / §5.1 表）。 */
export type SessionPhase = 'thinking' | 'tool' | 'file_write' | 'pty' | 'idle'

/** round.completed 的结果状态。 */
export type RoundStatus = 'completed' | 'error' | 'cancelled'

/** task.health 五态，与 frontend/src/features/tasks/health.ts 完全一致。 */
export type TaskHealthValue = 'needs-input' | 'stalled' | 'error' | 'running' | 'idle'

export interface SessionActivityData {
  instance_id: string
  session_id: string
  phase: SessionPhase
  /** 最近一次上游事件时间，epoch ms。 */
  last_event_at: number
  /** 1-based，按用户 prompt 序数递增（与前端按用户消息分组的轮次编号同规则）。 */
  round_index: number
}

export interface RoundChangeStats {
  added: number
  removed: number
  files: number
}

export interface RoundCompletedData {
  instance_id: string
  session_id: string
  round_index: number
  /** 一句话结论，发送侧截断至 ~200 字符。 */
  summary: string
  changes: RoundChangeStats
  status: RoundStatus
  completed_at: number
}

export interface TaskHealthData {
  task_id: string
  instance_id?: string
  health: TaskHealthValue
  /** 待审批 + 待提问合计计数。 */
  pending_count: number
  computed_at: number
}

/** 三个事件类型（与后端 session_event_broadcaster.go 常量一一对应）。 */
export const SESSION_EVENT_TYPES = [
  'session.activity',
  'round.completed',
  'task.health',
] as const

export type SessionEventType = (typeof SESSION_EVENT_TYPES)[number]

export interface SessionActivityEvent {
  /** WsEnvelopeV1.id（事件幂等键，日志/排查用）。 */
  eventId: string
  data: SessionActivityData
}

export interface RoundCompletedEvent {
  eventId: string
  data: RoundCompletedData
}

export interface TaskHealthEvent {
  eventId: string
  data: TaskHealthData
}

/** GET /api/mobile/events/snapshot 的响应（§5.2.2 重连追赶，快照而非回放）。 */
export interface EventsSnapshotSession {
  instance_id: string
  session_id: string
  health: TaskHealthValue | null
  phase: SessionPhase | null
  round_index: number
  last_event_at: number | null
  latest_round: RoundCompletedData | null
}

export interface EventsSnapshot {
  sessions: EventsSnapshotSession[]
  generated_at: number
}

function unwrapEnvelope(env: unknown): { inner: unknown; type: string } | null {
  if (!env || typeof env !== 'object') return null
  const outer = env as { type?: unknown; data?: unknown }
  if (typeof outer.type !== 'string') return null
  return { inner: outer.data, type: outer.type }
}

function readNumber(v: unknown): number {
  return typeof v === 'number' && Number.isFinite(v) ? v : 0
}

const PHASES: SessionPhase[] = ['thinking', 'tool', 'file_write', 'pty', 'idle']
const ROUND_STATUSES: RoundStatus[] = ['completed', 'error', 'cancelled']
const HEALTHS: TaskHealthValue[] = ['needs-input', 'stalled', 'error', 'running', 'idle']

/** 从 idempotentWsBus 订阅回调入参解析 session.activity；结构不符返回 null。 */
export function parseSessionActivity(env: unknown): SessionActivityEvent | null {
  const unwrapped = unwrapEnvelope(env)
  if (!unwrapped || unwrapped.type !== 'session.activity') return null
  const e = unwrapped.inner as { id?: unknown; data?: unknown } | null
  const d = e?.data
  if (!d || typeof d !== 'object') return null
  const r = d as Record<string, unknown>
  const sessionId = typeof r.session_id === 'string' ? r.session_id : ''
  const instanceId = typeof r.instance_id === 'string' ? r.instance_id : ''
  const phase = PHASES.includes(r.phase as SessionPhase) ? (r.phase as SessionPhase) : null
  if (!sessionId || !instanceId || !phase) return null
  return {
    eventId: typeof e?.id === 'string' ? e.id : '',
    data: {
      instance_id: instanceId,
      session_id: sessionId,
      phase,
      last_event_at: readNumber(r.last_event_at),
      round_index: readNumber(r.round_index),
    },
  }
}

/** 从 idempotentWsBus 订阅回调入参解析 round.completed；结构不符返回 null。 */
export function parseRoundCompleted(env: unknown): RoundCompletedEvent | null {
  const unwrapped = unwrapEnvelope(env)
  if (!unwrapped || unwrapped.type !== 'round.completed') return null
  const e = unwrapped.inner as { id?: unknown; data?: unknown } | null
  const d = e?.data
  if (!d || typeof d !== 'object') return null
  const r = d as Record<string, unknown>
  const sessionId = typeof r.session_id === 'string' ? r.session_id : ''
  const instanceId = typeof r.instance_id === 'string' ? r.instance_id : ''
  const status = ROUND_STATUSES.includes(r.status as RoundStatus) ? (r.status as RoundStatus) : null
  const summary = typeof r.summary === 'string' ? r.summary : ''
  if (!sessionId || !instanceId || !status) return null
  const c = (r.changes ?? {}) as Record<string, unknown>
  return {
    eventId: typeof e?.id === 'string' ? e.id : '',
    data: {
      instance_id: instanceId,
      session_id: sessionId,
      round_index: readNumber(r.round_index),
      summary,
      changes: {
        added: readNumber(c.added),
        removed: readNumber(c.removed),
        files: readNumber(c.files),
      },
      status,
      completed_at: readNumber(r.completed_at),
    },
  }
}

/** 从 idempotentWsBus 订阅回调入参解析 task.health；结构不符返回 null。 */
export function parseTaskHealth(env: unknown): TaskHealthEvent | null {
  const unwrapped = unwrapEnvelope(env)
  if (!unwrapped || unwrapped.type !== 'task.health') return null
  const e = unwrapped.inner as { id?: unknown; data?: unknown } | null
  const d = e?.data
  if (!d || typeof d !== 'object') return null
  const r = d as Record<string, unknown>
  const taskId = typeof r.task_id === 'string' ? r.task_id : ''
  const health = HEALTHS.includes(r.health as TaskHealthValue) ? (r.health as TaskHealthValue) : null
  if (!taskId || !health) return null
  const instanceId = typeof r.instance_id === 'string' ? r.instance_id : undefined
  return {
    eventId: typeof e?.id === 'string' ? e.id : '',
    data: {
      task_id: taskId,
      ...(instanceId ? { instance_id: instanceId } : {}),
      health,
      pending_count: readNumber(r.pending_count),
      computed_at: readNumber(r.computed_at),
    },
  }
}
