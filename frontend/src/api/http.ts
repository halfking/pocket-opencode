/**
 * Shared HTTP client with auth token injection.
 * New per-feature api modules (notes.ts, email.ts, vault.ts) build on this
 * instead of calling fetch() directly, so auth headers stay consistent.
 */
import { useAuthStore } from '../stores/auth'

const API_BASE = import.meta.env.VITE_API_BASE || ''

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
    this.name = 'ApiError'
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
    throw new ApiError(res.status, `Request failed: ${res.statusText}`)
  }
  // 204 No Content
  if (res.status === 204) return undefined as unknown as T
  return res.json() as Promise<T>
}

export const apiBase = API_BASE
