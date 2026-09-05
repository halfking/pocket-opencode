/**
 * Shared HTTP client with auth token injection.
 * New per-feature api modules (notes.ts, email.ts, vault.ts) build on this
 * instead of calling fetch() directly, so auth headers stay consistent.
 */
import { useAuthStore } from '../stores/auth'
import { assertNotHTML } from './jsonGuard'

// 再导出：client.ts 等既有调用方统一从 ./http 取守卫。
export { assertNotHTML }

const API_BASE = import.meta.env.VITE_API_BASE || ''

/** refresh 端点本身 401 时不得再触发续期重放（防自引用循环）。 */
const REFRESH_PATH = '/api/auth/refresh'

export class ApiError extends Error {
  /** 响应 body 解析后的对象（若有）。让调用方能拿到 409 等结构化错误信息。 */
  body?: any
  constructor(public status: number, message: string, body?: any) {
    super(message)
    this.name = 'ApiError'
    this.body = body
  }
}

/** 发请求前临期主动续期（内部仍单飞）；失败不阻塞本次请求（会再走 401 兜底）。 */
async function maybeRefreshBeforeRequest(path: string): Promise<void> {
  if (path === REFRESH_PATH) return
  try {
    const auth = useAuthStore()
    await auth.maybeRefresh()
  } catch {
    // 忽略：maybeRefresh 内部已吞错
  }
}

/** Wrapper around fetch that injects the Bearer token and parses JSON. */
async function httpOnce<T = any>(path: string, opts: RequestInit = {}): Promise<T> {
  const auth = useAuthStore()
  const headers: Record<string, string> = {
    ...(opts.headers as Record<string, string> | undefined),
  }
  if (auth.token) headers['Authorization'] = `Bearer ${auth.token}`
  if (opts.body && !headers['Content-Type']) {
    headers['Content-Type'] = 'application/json'
  }

  const res = await fetch(`${API_BASE}${path}`, { ...opts, headers })
  if (!res.ok) {
    // 尝试解析响应 body，让调用方能拿到结构化错误（如 409 conflict 的 server_version）
    let parsedBody: any
    try {
      const text = await res.text()
      parsedBody = text ? JSON.parse(text) : undefined
    } catch {
      parsedBody = undefined
    }
    throw new ApiError(res.status, `Request failed: ${res.statusText}`, parsedBody)
  }
  // 204 No Content
  if (res.status === 204) return undefined as unknown as T
  return assertNotHTML(res).json() as Promise<T>
}

/**
 * http = httpOnce + JWT 滑动续期（runbook §15.2）：
 *  1. 请求前 token 临期（<5min）主动单飞续期；
 *  2. 收到 401 时单飞 refresh 一次并用新 token 重放；refresh 失败才让
 *     401 透传（调用方维持原有错误处理；登出仍由各视图自行决定）。
 * refresh 端点自身与未登录（无 token）请求不参与续期。
 */
export async function http<T = any>(path: string, opts: RequestInit = {}): Promise<T> {
  await maybeRefreshBeforeRequest(path)
  try {
    return await httpOnce<T>(path, opts)
  } catch (e) {
    if (e instanceof ApiError && e.status === 401 && path !== REFRESH_PATH) {
      const auth = useAuthStore()
      if (auth.token && (await auth.refreshSession())) {
        return httpOnce<T>(path, opts)
      }
    }
    throw e
  }
}

export const apiBase = API_BASE
