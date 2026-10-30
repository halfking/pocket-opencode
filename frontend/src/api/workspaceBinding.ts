/**
 * workspaceBinding.ts — 工作区成员绑定断言的客户端（2026-10-29）。
 *
 * ⚠ 这层是什么：
 *   「这个用户确实是这个工作区的成员」这件事，必须由**工作区权威签过名**，
 *   而不是客户端自己说了算（客户端自报的 workspaceId 只是句话）。
 *   断言由 pocketd（工作区权威）签发，覆盖用户、租户、工作区、角色、
 *   签发方、受众、有效期；验证方（RedClaw 等服务间调用方）用共享密钥验签。
 *
 * fetchAuthIdentity 使用服务端验证后返回的 user/workspace 恢复本地归属；
 * 跨服务调用再携带 binding，由接收方独立验签。客户端不解码 JWT
 * 来决定身份，也不把持久化的 workspace 候选当成成员资格。
 */

import { resolveRuntimeApiBase } from '../config/api-base'
import { fetchWithTimeout } from './fetch-timeout'
import { useAuthStore } from '../stores/auth'
import { assertDirectPocketRequest } from './pocket-resource'

/** 一份已签名的成员绑定断言。 */
export interface WorkspaceBinding {
  /** 断言本身（JWT），交给对端服务验证。 */
  readonly binding: string
  readonly userId: string
  readonly tenantId: string
  readonly workspaceId: string
  readonly role: string
  readonly issuer: string
  readonly audience: string
  readonly issuedAt: number
  readonly expiresAt: number
}

/** 对端服务验证一份断言后的结论。 */
export interface WorkspaceBindingVerification {
  readonly valid: boolean
  readonly userId: string
  readonly tenantId: string
  readonly workspaceId: string
  readonly role: string
  readonly issuer: string
  readonly audience: string
  readonly issuedAt: number
  readonly expiresAt: number
}

function bindingFromBody(value: unknown): WorkspaceBinding {
  const body = value as Record<string, unknown> | null
  if (!body || typeof body.binding !== 'string' || !body.binding || typeof body.user_id !== 'string' || !body.user_id ||
    typeof body.workspace_id !== 'string' || !body.workspace_id) {
    throw new Error('The server did not issue a workspace binding.')
  }
  const text = (key: string) => typeof body[key] === 'string' ? body[key] as string : ''
  const seconds = (key: string) => typeof body[key] === 'number' && Number.isSafeInteger(body[key]) ? body[key] as number : 0
  return Object.freeze({ binding: body.binding, userId: body.user_id, workspaceId: body.workspace_id,
    tenantId: text('tenant_id'), role: text('role'), issuer: text('issuer'), audience: text('audience'),
    issuedAt: seconds('issued_at'), expiresAt: seconds('expires_at') })
}

/** Portal calls must use the authenticated RedClaw delegation bridge. The ordinary
 * pocket API has a different token audience; a failed bridge never falls back to it.
 * The caller fixes its bearer, origin and admission before any asynchronous work. */
export async function fetchPortalWorkspaceBinding(
  workspaceId: string,
  request: { token: string; base: string; assertCurrent: () => void },
): Promise<WorkspaceBinding> {
  return fetchCurrentWorkspaceBinding(workspaceId, request, '/api/v1/portal/workspaces')
}

/** Direct pocket logins use the same live membership proof at their authority.
 * Selecting this route is never a fallback after RedClaw rejects a credential. */
export async function fetchPocketWorkspaceBinding(
  workspaceId: string,
  request: { token: string; base: string; assertCurrent: () => void },
): Promise<WorkspaceBinding> {
  return fetchCurrentWorkspaceBinding(workspaceId, request, '/api/workspaces')
}

async function fetchCurrentWorkspaceBinding(
  workspaceId: string,
  request: { token: string; base: string; assertCurrent: () => void },
  prefix: string,
): Promise<WorkspaceBinding> {
  request.assertCurrent()
  if (!workspaceId || workspaceId !== workspaceId.trim()) throw new Error('A workspace id is required to request a binding.')
  if (!request.token) throw new Error('An authenticated portal session is required.')
  const response = await fetchWithTimeout(
    `${request.base}${prefix}/${encodeURIComponent(workspaceId)}/binding`,
    { method: 'GET', headers: { Authorization: `Bearer ${request.token}` }, redirect: 'error', credentials: 'omit' },
  )
  request.assertCurrent()
  if (!response.ok) throw new Error('The server did not issue a workspace binding.')
  const body = await response.json()
  request.assertCurrent()
  const binding = bindingFromBody(body)
  if (binding.workspaceId !== workspaceId || !binding.tenantId || binding.expiresAt * 1000 <= Date.now()) {
    throw new Error('The server did not issue a current workspace binding for this scope.')
  }
  return binding
}

/**
 * 取当前用户的成员绑定断言。
 *
 * ⚠ 失败（未配置、非成员、已过期）一律抛错，**不返回空断言**：
 *   「签不出来」必须与「不在这个工作区」在调用方眼里是同一件事 ——
 *   都不能拿来当授权凭据。
 */
export async function fetchWorkspaceBinding(
  workspaceId: string,
): Promise<WorkspaceBinding> {
  const id = workspaceId.trim()
  if (!id) throw new Error('A workspace id is required to request a binding.')
  assertDirectPocketRequest(useAuthStore())
  // ⚠ **不走 http()**：401 在这里只意味着「服务端没有给出绑定证明」，
  //   不等于「会话已死」。http() 的 forceReauth() 会把会话清掉 ⇒
  //   误伤正常的恢复流程。
  const response = await fetchWithTimeout(
    `${resolveRuntimeApiBase()}/api/workspaces/${encodeURIComponent(id)}/binding`,
    { method: 'GET', headers: { Authorization: `Bearer ${useAuthStore().token}` } },
  )
  if (!response.ok) throw new Error('The server did not issue a workspace binding.')
  return bindingFromBody(await response.json())
}

/**
 * 让服务端验证一份断言（供诊断与自检）。
 *
 * ⚠ 这是**独立裁决点**：调用方不必自己实现验签（各自实现 = 各自可能实现错）。
 */
export async function verifyWorkspaceBinding(
  binding: string,
  userId = '',
  workspaceId = '',
): Promise<WorkspaceBindingVerification> {
  const raw = binding.trim()
  if (!raw) throw new Error('A binding is required to verify.')
  assertDirectPocketRequest(useAuthStore())
  const response = await fetchWithTimeout(
    `${resolveRuntimeApiBase()}/api/auth/workspace-binding`,
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Authorization: `Bearer ${useAuthStore().token}`,
      },
      body: JSON.stringify({ binding: raw, user_id: userId, workspace_id: workspaceId }),
    },
  )
  if (!response.ok) throw new Error('The server rejected the workspace binding.')
  const body = await response.json() as Partial<WorkspaceBindingVerification> & {
    user_id?: string
    tenant_id?: string
    workspace_id?: string
    role?: string
    issuer?: string
    audience?: string
    issued_at?: number
    expires_at?: number
  }
  if (body?.valid !== true) throw new Error('The server rejected the workspace binding.')
  return Object.freeze({
    valid: true,
    userId: String(body?.user_id ?? ''),
    tenantId: String(body?.tenant_id ?? ''),
    workspaceId: String(body?.workspace_id ?? ''),
    role: String(body?.role ?? ''),
    issuer: String(body?.issuer ?? ''),
    audience: String(body?.audience ?? ''),
    issuedAt: Number(body?.issued_at ?? 0),
    expiresAt: Number(body?.expires_at ?? 0),
  })
}
