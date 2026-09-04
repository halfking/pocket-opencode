/**
 * C4 — 邮箱注册 / 验证码登录 / 忘记密码 相关 API。
 *
 * 后端对应：
 *   POST /api/auth/send-code
 *   POST /api/auth/register
 *   POST /api/auth/code-login
 *   POST /api/auth/forgot-password
 *   POST /api/auth/reset-password（requireAuth）
 */
import { http } from './http'

export type CodePurpose = 'register' | 'reset' | 'login'

export interface SendCodeResponse {
  ok: true
  ttl_sec: number
  /** 仅当 POCKET_SMTP_DEBUG_ECHO=true 且 SMTP 未配置时回显 */
  debug_code?: string
}

export interface AuthSuccessResponse {
  token: string
  user: string
  user_id?: string
  workspace_id?: string
}

export interface OkResponse {
  ok: true
}

/** 发送验证码。恒返回 200（即便邮箱未注册），失败时不抛错（仅 status 不对才抛）。 */
export async function sendCode(email: string, purpose: CodePurpose): Promise<SendCodeResponse> {
  return http<SendCodeResponse>('/api/auth/send-code', {
    method: 'POST',
    body: JSON.stringify({ email, purpose }),
  })
}

/** 邮箱注册：返回 JWT + workspace 元数据。 */
export async function registerUser(body: {
  email: string
  code: string
  username: string
  password: string
}): Promise<AuthSuccessResponse> {
  return http<AuthSuccessResponse>('/api/auth/register', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

/** 邮箱验证码登录。 */
export async function codeLogin(email: string, code: string): Promise<AuthSuccessResponse> {
  return http<AuthCodeLoginResponse>('/api/auth/code-login', {
    method: 'POST',
    body: JSON.stringify({ email, code }),
  }) as Promise<AuthCodeLoginResponse>
}

// 旧后端可能直接返回 AuthSuccessResponse；新后端统一格式。
type AuthCodeLoginResponse = AuthSuccessResponse

/** 忘记密码重置。成功后不返回 token，强制重新登录。 */
export async function forgotPassword(email: string, code: string, newPassword: string): Promise<OkResponse> {
  return http<OkResponse>('/api/auth/forgot-password', {
    method: 'POST',
    body: JSON.stringify({ email, code, new_password: newPassword }),
  })
}

/** 已登录改密码。 */
export async function resetPassword(body: {
  email: string
  old_password: string
  new_password: string
}): Promise<OkResponse> {
  return http<OkResponse>('/api/auth/reset-password', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}

// =============================================================================
// Phase 1 RedClaw 切换新增
// =============================================================================

/** 当前用户画像（RedClaw 透传）。 */
export interface EmployeeProfile {
  id: string
  name: string
  role: string
  email: string
  departmentId?: string
  positionId?: string
  agentId?: string
  mustChangePassword?: boolean
}

/** 登出：调后端 /api/auth/logout 让 RedClaw 撤销 session。401 视作幂等成功。 */
export async function logoutRemote(): Promise<OkResponse> {
  return http<OkResponse>('/api/auth/logout', { method: 'POST' })
}

/** 拉取当前 RedClaw employee 画像；用于 token 续期校验与 UI 头像。 */
export async function fetchMe(): Promise<EmployeeProfile> {
  return http<EmployeeProfile>('/api/auth/me', { method: 'GET' })
}

/**
 * SSO 登录入口：拿 RedClaw 跳转 URL，浏览器 location.href 跳过去。
 * CSRF 绑定由后端在响应里落 HttpOnly cookie（pocket_sso_txn），前端不再
 * 生成/比对 state（见 docs/handoff/2026-09-05-sso-state-contract-mismatch.md）。
 */
export async function fetchSsoLoginUrl(redirectUrl?: string): Promise<string> {
  const q = new URLSearchParams()
  if (redirectUrl) q.set('redirect_url', redirectUrl)
  const qs = q.toString()
  const r = await http<{ url: string }>(`/api/auth/sso/login${qs ? `?${qs}` : ''}`, { method: 'GET' })
  return r.url
}

export interface SsoExchangeResponse {
  token: string
  user: string
  user_id: string
  workspace_id: string
}

/**
 * 一次性 code 换登录结果（token 不走 URL，P1-2 修复）。
 * code 由后端 /api/auth/sso/callback 302 注入 SPA 回调页，90s TTL、单次有效。
 */
export async function exchangeSsoCode(code: string): Promise<SsoExchangeResponse> {
  return http<SsoExchangeResponse>('/api/auth/sso/exchange', {
    method: 'POST',
    body: JSON.stringify({ code }),
  })
}
