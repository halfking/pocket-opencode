/**
 * sessionTakeover.ts — RedClaw 门户「共享会话 / 申请接手」API client。
 *
 * 端点族（platform-go 提供）：
 *   GET  /api/v1/shared-sessions             读「共享给我的」会话列表
 *   POST /api/v1/sessions/:id/takeover        申请接手（产生待审批请求，不直接转移）
 *
 * 与 api/syncPolicy.ts 同源：base 走 **redclawPortalBase**
 * （config/redclaw-portal.ts），不是 pocketd 的 resolveRuntimeApiBase()；
 * 鉴权注入 Bearer token（openpocket 登录态）。
 *
 * 为什么这组端点与 api/http.ts 分开：openpocket 自身的会话是 **OpenCode 实例**
 * 的会话（/api/sessions），而「共享会话」是 **RedClaw 平台**上 dal.sessions 的
 * 另一份数据。两者不是同一个会话空间，也不该用同一个 base —— 混用会让人以为
 * 可以在 OpenCode 的会话列表里接手平台上的会话。
 *
 * 失败（未配置 / 不可达 / 401）由调用方降级：面板不渲染，**不阻塞**会话列表。
 * 门户没部署时，openpocket 依然是一个可用的移动端工作台。
 */

import { resolveRedclawPortalBase } from '../config/redclaw-portal'
import { useAuthStore, captureAuthSession, assertAuthSession } from '../stores/auth'
import { fetchWithTimeout } from './fetch-timeout'

/** 门户请求错误（含状态码；调用方只用于日志/toast，不用于流程分支）。 */
export class SessionTakeoverApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'SessionTakeoverApiError'
    this.status = status
  }
}

/** 一条共享会话（同事共享给当前用户的）。 */
export interface SharedConversation {
  id: string
  title: string
  /** 会话归属人 —— 有权决定是否交出的人。 */
  ownerId: string
  agentId?: string
  /** private | department | project | tenant */
  visibility: string
  sharedWithDepartmentId?: string
  sharedWithProjectId?: string
  startedAt?: string
  lastActive?: string
}

/** 申请接手的返回。 */
export interface TakeoverRequestResult {
  sessionId: string
  approvalId?: string
  pendingApproval?: boolean
}

async function portalFetch(path: string, init: RequestInit = {}): Promise<unknown> {
  const base = resolveRedclawPortalBase()
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(init.headers as Record<string, string> | undefined),
  }
  const auth = useAuthStore()
  const session = captureAuthSession(auth)
  if (auth.token) headers['Authorization'] = `Bearer ${auth.token}`

  const res = await fetchWithTimeout(`${base}${path}`, { ...init, headers })
  assertAuthSession(auth, session)
  if (!res.ok) {
    let message = res.statusText
    try {
      const body = (await res.json()) as { error?: { message?: string } | string }
      const envelope = body?.error
      if (typeof envelope === 'string' && envelope.trim()) message = envelope.trim()
      else if (envelope && typeof envelope === 'object' && envelope.message) message = envelope.message
    } catch {
      /* 非 JSON body：用 statusText */
    }
    assertAuthSession(auth, session)
    throw new SessionTakeoverApiError(res.status, message)
  }
  if (res.status === 204) return undefined
  const body = await res.json()
  assertAuthSession(auth, session)
  return body
}

/** 归一化门户返回的共享会话列表。 */
function normalizeShared(raw: unknown): SharedConversation[] {
  const sessions = (raw as { sessions?: unknown })?.sessions
  if (!Array.isArray(sessions)) return []
  return sessions
    .filter((item): item is Record<string, unknown> => !!item && typeof item === 'object')
    .map(item => ({
      id: String(item.id || ''),
      title: String(item.title || ''),
      ownerId: String(item.ownerId || ''),
      agentId: item.agentId ? String(item.agentId) : undefined,
      visibility: String(item.visibility || 'private'),
      sharedWithDepartmentId: item.sharedWithDepartmentId
        ? String(item.sharedWithDepartmentId)
        : undefined,
      sharedWithProjectId: item.sharedWithProjectId ? String(item.sharedWithProjectId) : undefined,
      startedAt: item.startedAt ? String(item.startedAt) : undefined,
      lastActive: item.lastActive ? String(item.lastActive) : undefined,
    }))
    .filter(item => !!item.id)
}

/** GET /api/v1/shared-sessions */
export async function fetchSharedConversations(limit = 50): Promise<SharedConversation[]> {
  const raw = await portalFetch(`/api/v1/shared-sessions?limit=${limit}`, { method: 'GET' })
  return normalizeShared(raw)
}

/**
 * POST /api/v1/sessions/:id/takeover — 申请接手。
 *
 * 这是一次**申请**，不是接手：会话在有人决定之前仍归原主人。
 */
export async function requestSessionTakeover(
  sessionId: string,
  reason?: string,
): Promise<TakeoverRequestResult> {
  const raw = await portalFetch(
    `/api/v1/sessions/${encodeURIComponent(sessionId)}/takeover`,
    {
      method: 'POST',
      body: JSON.stringify(reason ? { reason } : {}),
    },
  )
  const body = (raw || {}) as Record<string, unknown>
  return {
    sessionId: String(body.sessionId || sessionId),
    approvalId: body.approvalId ? String(body.approvalId) : undefined,
    pendingApproval: body.pendingApproval === true,
  }
}
