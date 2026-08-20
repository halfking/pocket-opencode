/**
 * Approvals API — 移动端 human-in-the-loop 审批（权限批准/拒绝 + 问答回答）
 *
 * 后端通道（pocketd，已就绪）：/api/mobile/approvals
 *   GET  /api/mobile/approvals?instance_id=&session_id=  → 列出待审批（权限 + 问答）
 *   POST /api/mobile/approvals/permission/{id}/reply      → 批准/拒绝某个权限请求
 *   POST /api/mobile/approvals/question/{id}/reply        → 回答某个问题请求
 *   POST /api/mobile/approvals/question/{id}/reject        → 跳过某个问题请求
 *
 * 鉴权复用 http.ts（自动注入 Bearer token + 统一 JSON 错误处理）。
 *
 * 数据结构与后端 opencode.PermissionManager / QuestionManager 的
 * ListPending 返回保持一致（adapter.PermissionRequest / QuestionRequest）。
 */
import { http } from './http'

/** 权限决策：once=本次批准，always=始终批准，reject=拒绝 */
export type PermissionDecision = 'once' | 'always' | 'reject'

export interface PermissionSource {
  type: string
  messageID?: string
  callID?: string
}

export interface PermissionRequest {
  id: string
  sessionID: string
  action?: string
  resources?: string[]
  save?: string[]
  metadata?: Record<string, any>
  source?: PermissionSource
}

export interface QuestionOption {
  label: string
  description?: string
}

export interface QuestionInfo {
  question: string
  header?: string
  options?: QuestionOption[]
  /** 是否允许多选 */
  multiple?: boolean
  /** 是否允许自定义（自由文本）回答 */
  custom?: boolean
}

export interface QuestionRequest {
  id: string
  sessionID: string
  questions: QuestionInfo[]
}

export interface PendingApprovals {
  permissions: PermissionRequest[]
  questions: QuestionRequest[]
}

export interface ReplyResult {
  request_id: string
  decision: string
  confirmed: boolean
  correlation_id?: string
}

/**
 * 拉取待审批列表。instanceID / sessionID 为可选过滤条件；
 * 不传则按当前 workspace 拉取全部待审批。
 */
export function listPendingApprovals(params: {
  instanceID?: string
  sessionID?: string
} = {}): Promise<PendingApprovals> {
  const qs = new URLSearchParams()
  if (params.instanceID) qs.set('instance_id', params.instanceID)
  if (params.sessionID) qs.set('session_id', params.sessionID)
  const query = qs.toString()
  return http<PendingApprovals>(
    `/api/mobile/approvals${query ? `?${query}` : ''}`,
  )
}

/** 批准 / 拒绝一个权限请求。decision ∈ {once, always, reject} */
export function replyPermission(
  requestID: string,
  body: {
    instanceID: string
    sessionID: string
    decision: PermissionDecision
    message?: string
  },
): Promise<ReplyResult> {
  return http<ReplyResult>(
    `/api/mobile/approvals/permission/${encodeURIComponent(requestID)}/reply`,
    {
      method: 'POST',
      body: JSON.stringify(body),
    },
  )
}

/**
 * 回答一个问题请求。
 * answers 为二维数组：下标对应 request.questions 中的每个子问题，
 * 每个元素是所选选项 label（或自定义文本）的字符串数组。
 */
export function replyQuestion(
  requestID: string,
  body: {
    instanceID: string
    sessionID: string
    answers: string[][]
  },
): Promise<ReplyResult> {
  return http<ReplyResult>(
    `/api/mobile/approvals/question/${encodeURIComponent(requestID)}/reply`,
    {
      method: 'POST',
      body: JSON.stringify(body),
    },
  )
}

/** 跳过（拒绝回答）一个问题请求 */
export function rejectQuestion(
  requestID: string,
  body: { instanceID: string; sessionID: string },
): Promise<ReplyResult> {
  return http<ReplyResult>(
    `/api/mobile/approvals/question/${encodeURIComponent(requestID)}/reject`,
    {
      method: 'POST',
      body: JSON.stringify(body),
    },
  )
}
