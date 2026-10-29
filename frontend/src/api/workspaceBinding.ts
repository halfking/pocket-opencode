/**
 * workspaceBinding.ts — 工作区成员绑定断言的客户端（2026-10-29）。
 *
 * ⚠ 这层是什么：
 *   「这个用户确实是这个工作区的成员」这件事，必须由**工作区权威签过名**，
 *   而不是客户端自己说了算（客户端自报的 workspaceId 只是句话）。
 *   断言由 pocketd（工作区权威）签发，覆盖用户、租户、工作区、角色、
 *   签发方、受众、有效期；验证方（RedClaw 等服务间调用方）用共享密钥验签。
 *
 * 📌 与 fetchAuthIdentity 的区别（两者都不可省）：
 *   - fetchAuthIdentity：问「服务端认为我属于哪个工作区」（**归属来源**）。
 *   - 本模块：取一份**可携带的签名凭证**，让别的服务能独立验证
 *     「这个人确实在这个工作区有成员资格」（**跨服务授权**）。
 *
 * ⚠ 客户端**不得**用断言里的字段替代自己的归属判断 ——
 *   断言是给**对端服务**验的；本地归属仍然以服务端确认的
 *   (user_id, workspace_id) 为准。
 */

import { resolveRuntimeApiBase } from '../config/api-base'
import { fetchWithTimeout } from './fetch-timeout'
import { useAuthStore } from '../stores/auth'

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
  // ⚠ **不走 http()**：401 在这里只意味着「服务端没有给出绑定证明」，
  //   不等于「会话已死」。http() 的 forceReauth() 会把会话清掉 ⇒
  //   误伤正常的恢复流程。
  const response = await fetchWithTimeout(
    `${resolveRuntimeApiBase()}/api/workspaces/${encodeURIComponent(id)}/binding`,
    { method: 'GET', headers: { Authorization: `Bearer ${useAuthStore().token}` } },
  )
  if (!response.ok) throw new Error('The server did not issue a workspace binding.')
  const body = await response.json() as Partial<WorkspaceBinding> & {
    user_id?: string
    tenant_id?: string
    workspace_id?: string
    role?: string
    issuer?: string
    audience?: string
    issued_at?: number
    expires_at?: number
  }
  const binding = String(body?.binding ?? '')
  const userId = String(body?.user_id ?? '')
  const workspaceIdOut = String(body?.workspace_id ?? '')
  // 三个要素缺一不可：没有签名串就没有可验证的东西；
  // 没有用户/工作区就无法确定这份断言说的是谁、在哪。
  if (!binding || !userId || !workspaceIdOut) {
    throw new Error('The server did not issue a workspace binding.')
  }
  return Object.freeze({
    binding,
    userId,
    tenantId: String(body?.tenant_id ?? ''),
    workspaceId: workspaceIdOut,
    role: String(body?.role ?? ''),
    issuer: String(body?.issuer ?? ''),
    audience: String(body?.audience ?? ''),
    issuedAt: Number(body?.issued_at ?? 0),
    expiresAt: Number(body?.expires_at ?? 0),
  })
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
