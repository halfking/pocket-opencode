/**
 * Shared HTTP client with auth token injection.
 * New per-feature api modules (notes.ts, email.ts, vault.ts) build on this
 * instead of calling fetch() directly, so auth headers stay consistent.
 */
import { useAuthStore } from '../stores/auth'

const API_BASE = import.meta.env.VITE_API_BASE || ''

export class ApiError extends Error {
  /** 响应 body 解析后的对象（若有）。让调用方能拿到 409 等结构化错误信息。 */
  body?: any
  constructor(public status: number, message: string, body?: any) {
    super(message)
    this.name = 'ApiError'
    this.body = body
  }
}

/** Wrapper around fetch that injects the Bearer token and parses JSON. */
export async function http<T = any>(
  path: string,
  opts: RequestInit = {},
): Promise<T> {
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
  return res.json() as Promise<T>
}

export const apiBase = API_BASE
