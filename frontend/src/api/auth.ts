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
import { ApiError, http } from './http'
import { resolveRuntimeApiBase } from '../config/api-base'
import { fetchWithTimeout } from './fetch-timeout'
import { fetchPortalWorkspaceBinding, fetchPocketWorkspaceBinding } from './workspaceBinding'
import { resolveRedclawPortalBase } from '../config/redclaw-portal'
import { REDCLAW_OIDC_AUTH_METHOD, usesDirectPocketAuthority } from './authentication-mode'
import { readPortalErrorCode } from './portal-error'

export type CodePurpose = 'register' | 'reset' | 'login'

export interface SendCodeResponse {
  ok: true
  ttl_sec: number
  /**
   * 'smtp' = 本部署有邮件通道，验证码会真的发出去；
   * 'none' = 未配置 SMTP，验证码只入库、不会发邮件（此时不要提示用户「已发送」）。
   * 缺失时按老后端处理，视为 'smtp' 以免误伤已部署的实例。
   */
  delivery?: 'smtp' | 'none'
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

/** 已登录改密码（需要旧密码）。 */
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

/** 已登录用户通过邮箱验证码修改密码（无需旧密码）。 */
export async function changePasswordWithCode(email: string, code: string, newPassword: string): Promise<OkResponse> {
  return http<OkResponse>('/api/auth/change-password-with-code', {
    method: 'POST',
    body: JSON.stringify({ email, code, new_password: newPassword }),
  })
}

/** 已登录用户更新邮箱（需要验证码验证新邮箱所有权）。 */
export async function updateEmail(email: string, code: string): Promise<OkResponse> {
  return http<OkResponse>('/api/auth/update-email', {
    method: 'POST',
    body: JSON.stringify({ email, code }),
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
export async function logoutRemote(token?: string): Promise<OkResponse> {
  if (token === undefined) return http<OkResponse>('/api/auth/logout', { method: 'POST' })
  // Revocation uses the captured credential once; it never refreshes/replays as a new login.
  const response = await fetchWithTimeout(`${resolveRuntimeApiBase()}/api/auth/logout`, {
    method: 'POST', headers: { Authorization: `Bearer ${token}` },
  })
  if (!response.ok && response.status !== 401) throw new Error('Remote sign-out failed.')
  return { ok: true }
}

/** 拉取当前 RedClaw employee 画像；用于 token 续期校验与 UI 头像。 */
export async function fetchMe(): Promise<EmployeeProfile> {
  return http<EmployeeProfile>('/api/auth/me', { method: 'GET' })
}

/**
 * 拉取**服务端确认过的归属**（2026-10-29）。
 *
 * ⚠ 为什么需要它：应用重启后从 localStorage 恢复会话时，持久化的
 *   workspaceId 是**客户端自报**的，未经服务端确认。客户端自报的身份
 *   不能当作归属依据 —— 否则跨账号/跨工作区的归属判错无法被发现，
 *   本地数据可能被挂到错误的账号名下。
 *
 * ⇒ 归属只认服务端签发过的东西：恢复会话时调本端点，用服务端确认过的
 *   (user_id, workspace_id) 重新确立归属。
 */
export async function fetchAuthIdentity(
  bearer: string,
  workspaceCandidate: string,
  assertCurrent: () => void,
  loginMethod = 'redclaw',
): Promise<{ userId: string; workspaceId: string }> {
  // The stored workspace is only a candidate. RedClaw authenticates the bearer
  // and delegates to pocketd to check live membership before returning its scope.
  // An absent/invalid candidate stays unowned; there is no default fallback.
  // The mode returned by login selects one authority, without decoding a JWT or
  // trying another authority after failure. Persisted mode/candidate grant no
  // ownership: both routes still require the service's live membership proof.
  const direct = usesDirectPocketAuthority(loginMethod)
  const resolveAuthority = direct ? resolveRuntimeApiBase : resolveRedclawPortalBase
  const base = resolveAuthority()
  const admit = () => {
    assertCurrent()
    if (resolveAuthority() !== base) throw Object.assign(new Error('Workspace authority changed.'), { name: 'AbortError' })
  }
  const fetchBinding = direct ? fetchPocketWorkspaceBinding : fetchPortalWorkspaceBinding
  const binding = await fetchBinding(workspaceCandidate, { token: bearer, base, assertCurrent: admit })
  admit()
  return { userId: binding.userId, workspaceId: binding.workspaceId }
}

/** SSO 启用探测（无副作用）：不铸 nonce、不落 cookie，登录页每次加载调用。 */
export async function fetchSsoStatus(): Promise<boolean> {
  return (await fetchSsoStatusDetailed()) !== null
}

/**
 * SSO 启用探测（带 mode）。mode="oidc" 表示 Casdoor 直连 OIDC（本地凭据面
 * 已退役，登录页只留 SSO 入口）；mode="redclaw" 为 RedClaw 镜像道（双 lane
 * 并存）；null = 未启用 / 网络错误。
 */
export async function fetchSsoStatusDetailed(): Promise<{ enabled: boolean; mode: 'oidc' | 'redclaw'; app_origin?: string } | null> {
  try {
    const r = await http<{ enabled: boolean; mode?: string; app_origin?: string }>('/api/auth/sso/status', { method: 'GET' })
    if (!r.enabled) return null
    if (r.mode === 'oidc') return { enabled: true, mode: 'oidc' }
    if (typeof r.app_origin === 'string' && r.app_origin) return { enabled: true, mode: 'redclaw', app_origin: r.app_origin }
    return { enabled: true, mode: 'redclaw' }
  } catch {
    return null
  }
}

/**
 * SSO 登录入口：拿 RedClaw 跳转 URL，浏览器 location.href 跳过去。
 * CSRF 绑定由后端在响应里落 HttpOnly cookie（pocket_sso_txn），前端不再
 * 生成/比对 state（见 docs/handoff/2026-09-05-sso-state-contract-mismatch.md）。
 */
/**
 * App 端（Capacitor APK）无跳转企业登录：账密直接 POST 给 pocketd，
 * 由 pocketd 服务端代办 Casdoor /api/login → authorize 抓 code → token
 * 兑换 → 签发 pocketd 会话 JWT。响应与 SSO exchange 同形。
 * 背景：APK WebView 里 OIDC 顶层导航会被交给系统浏览器，回调落不到
 * 本地包 —— 直连 Casdoor JS API 又被 CORS/HttpOnly/不透明 302 三堵墙挡死，
 * 故走服务端代办（2026-11-19）。
 */
export async function fetchSsoDirectLogin(username: string, password: string): Promise<SsoExchangeResponse> {
  const result = await http<Omit<SsoExchangeResponse, 'auth_method'> & { auth_method?: string }>('/api/auth/sso/direct-login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  if (result.auth_method !== undefined && result.auth_method !== 'casdoor-oidc') {
    throw new Error('企业登录返回了不匹配的登录权威，请重新登录')
  }
  // The dedicated direct endpoint also identifies older Pocketd responses.
  return { ...result, auth_method: 'casdoor-oidc' }
}

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
  auth_method: 'redclaw' | typeof REDCLAW_OIDC_AUTH_METHOD | 'casdoor-oidc'
}

/**
 * 一次性 code 换登录结果（token 不走 URL，P1-2 修复）。
 * code 由后端 /api/auth/sso/callback 302 注入 SPA 回调页，90s TTL、单次有效。
 */
export async function exchangeSsoCode(
  code: string, app?: { verifier: string; assertCurrent: () => void },
): Promise<SsoExchangeResponse> {
  const result = await http<SsoExchangeResponse>('/api/auth/sso/exchange', {
    method: 'POST',
    body: JSON.stringify({ code, code_verifier: app?.verifier }),
    ...(app && { assertCurrent: app.assertCurrent, credentials: 'omit', redirect: 'error' }),
  })
  if (result.auth_method !== 'redclaw' && result.auth_method !== REDCLAW_OIDC_AUTH_METHOD && result.auth_method !== 'casdoor-oidc') {
    throw new Error('登录服务未确认会话来源，请重新发起企业登录')
  }
  return result
}

/** Sliding renewal for credentials accepted by the ordinary Pocket path. */
export async function refreshAuth(): Promise<AuthSuccessResponse> {
  return http<AuthSuccessResponse>('/api/auth/refresh', { method: 'POST' })
}

/** The dedicated relay authenticates this captured bearer and the selected workspace. */
export async function refreshRedClawSSO(
  bearer: string, workspace: string, assertCurrent: () => void,
): Promise<SsoExchangeResponse> {
  const base = resolveRuntimeApiBase()
  const checkCurrent = () => {
    assertCurrent()
    if (resolveRuntimeApiBase() !== base) {
      throw Object.assign(new Error('The session renewal endpoint changed.'), { name: 'AbortError' })
    }
  }
  checkCurrent()
  const response = await fetchWithTimeout(`${base}/api/auth/sso/refresh`, {
    method: 'POST', redirect: 'error', credentials: 'omit',
    headers: { Authorization: `Bearer ${bearer}`, 'Content-Type': 'application/json' },
    body: JSON.stringify({ workspace_id: workspace }),
  })
  if (!response.ok) {
    const code = await readPortalErrorCode(response)
    checkCurrent()
    throw new ApiError(response.status, 'SSO session renewal failed.', { code })
  }
  const result = await response.json() as SsoExchangeResponse
  checkCurrent()
  return result
}
