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
