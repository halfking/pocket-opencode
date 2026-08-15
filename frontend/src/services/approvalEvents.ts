/**
 * approvalEvents.ts — 后端审批推送事件的解析（纯函数，Node 可测）。
 *
 * 服务端（backend/internal/opencode/approval_broadcaster.go）把 WsEnvelopeV1
 * 包在 WS hub 的 {type, payload} 通用信封里下发；idempotentWsBus 归一化后，
 * 订阅回调拿到的 env.data 即后端 WsEnvelopeV1（v:1，带 cause.approval_id 供幂等）。
 *
 * 本模块不 import 任何运行时依赖，保持可在 node --test 下直接测试。
 */

export type ApprovalEventKind = 'permission' | 'question'

export interface ApprovalEventInfo {
  /** 幂等键：cause.approval_id（缺省回退 request id）。 */
  approvalId: string
  instanceId: string
  sessionId: string
  /** pending 事件取 request.id；resolved 事件取 request_id。 */
  requestId: string
  kind: ApprovalEventKind
  /** 仅 resolved 事件：approved | rejected | answered | expired | resolved。 */
  resolution?: string
}

/** 事件的三种类型（与后端 ApprovalEvent* 常量一一对应）。 */
export const APPROVAL_EVENT_TYPES = [
  'approval.permission.pending',
  'approval.question.pending',
  'approval.resolved',
] as const

/**
 * 从 idempotentWsBus 的订阅回调入参解析审批事件；结构不符返回 null。
 * env 形如 { v:0, type:'approval.permission.pending', data: <WsEnvelopeV1> }。
 */
export function parseApprovalEvent(env: unknown): ApprovalEventInfo | null {
  if (!env || typeof env !== 'object') return null
  const outer = env as { type?: unknown; data?: unknown }
  if (typeof outer.type !== 'string') return null

  const inner = outer.data
  if (!inner || typeof inner !== 'object') return null
  const e = inner as {
    type?: unknown
    cause?: { approval_id?: unknown }
    data?: unknown
  }
  const data = e.data
  if (!data || typeof data !== 'object') return null
  const d = data as Record<string, unknown>

  const instanceId = typeof d.instance_id === 'string' ? d.instance_id : ''
  const sessionId = typeof d.session_id === 'string' ? d.session_id : ''
  let requestId = typeof d.request_id === 'string' ? d.request_id : ''
  if (!requestId && d.request && typeof d.request === 'object') {
    const id = (d.request as { id?: unknown }).id
    if (typeof id === 'string') requestId = id
  }
  if (!instanceId || !sessionId || !requestId) return null

  const kind: ApprovalEventKind =
    outer.type === 'approval.question.pending' || d.kind === 'question' ? 'question' : 'permission'
  const resolution = typeof d.resolution === 'string' ? d.resolution : undefined
  const causeId = e.cause?.approval_id
  const approvalId = typeof causeId === 'string' && causeId !== '' ? causeId : requestId

  return { approvalId, instanceId, sessionId, requestId, kind, resolution }
}
