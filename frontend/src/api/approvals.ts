/**
 * approvals.ts — 移动审批 API 客户端（/api/mobile/approvals）。
 *
 * 与 native/outboxDrain.ts 的发送器走同一路由契约：
 *   GET  /api/mobile/approvals?instance_id=&session_id=
 *   POST /api/mobile/approvals/permission/{request_id}/reply
 * 在线路径直接 POST；离线路径由 usePendingApprovals 走 outbox。
 */
import { http } from './http'

export interface PermissionRequest {
  id: string
  sessionID: string
  action: string
  resources?: string[]
  save?: string[]
  metadata?: Record<string, unknown>
}

export interface QuestionRequest {
  id: string
  sessionID: string
  questions: unknown[]
}

export interface PendingApprovals {
  permissions: PermissionRequest[]
  questions: QuestionRequest[]
}

export function listPendingApprovals(instanceId: string, sessionId?: string): Promise<PendingApprovals> {
  const params = new URLSearchParams({ instance_id: instanceId })
  if (sessionId) params.set('session_id', sessionId)
  return http<PendingApprovals>(`/api/mobile/approvals?${params.toString()}`)
}

export interface PermissionReplyArgs {
  instanceId: string
  sessionId: string
  requestId: string
  decision: 'once' | 'always' | 'reject'
  message?: string
}

export async function replyPermission(args: PermissionReplyArgs): Promise<{ confirmed: boolean }> {
  return http(`/api/mobile/approvals/permission/${encodeURIComponent(args.requestId)}/reply`, {
    method: 'POST',
    headers: {
      'Idempotency-Key': `appr_permission_${args.requestId}`,
    },
    body: JSON.stringify({
      instance_id: args.instanceId,
      session_id: args.sessionId,
      decision: args.decision,
      ...(args.message !== undefined ? { message: args.message } : {}),
    }),
  })
}
