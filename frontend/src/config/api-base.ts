/**
 * pocketd API 基址 SSOT。
 *
 * 优先级：localStorage 覆盖 > VITE_API_BASE > 同源（空串，相对 /api）。
 * 设置页「后端服务器」和登录页展示都读这里，不再用 window.location.origin 冒充。
 */

export const API_BASE_STORAGE_KEY = 'pocket_api_base'
export const PRODUCTION_API_BASE = 'https://pocket.itestu.cn'

export type ProbeHealthzResult = { ok: true } | { ok: false; error: string }

function defaultStorage(): Storage | null {
  try {
    return typeof localStorage !== 'undefined' ? localStorage : null
  } catch {
    return null
  }
}

function buildDefaultFromEnv(): string {
  try {
    return String(import.meta.env?.VITE_API_BASE || '')
  } catch {
    return ''
  }
}

function pageOriginFallback(pageOrigin?: string): string {
  if (pageOrigin) return pageOrigin
  return typeof window !== 'undefined' ? window.location.origin : ''
}

/** 去空白、去尾斜杠；同源绝对地址收成 ''；非法 scheme 抛错。 */
export function normalizeApiBase(input: string, pageOrigin?: string): string {
  const raw = input.trim()
  if (!raw) return ''
  let url: URL
  try {
    url = new URL(raw)
  } catch {
    throw new Error('invalid-url')
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    throw new Error('invalid-protocol')
  }
  const origin = pageOriginFallback(pageOrigin)
  const path = url.pathname === '/' ? '' : url.pathname.replace(/\/+$/, '')
  if (origin && url.origin === origin && !path && !url.search && !url.hash) {
    return ''
  }
  return `${url.origin}${path}`
}

export function readApiBaseOverride(storage?: Storage): string | null {
  const store = storage ?? defaultStorage()
  if (!store) return null
  return store.getItem(API_BASE_STORAGE_KEY)
}

export function persistApiBase(value: string | null, storage?: Storage): string {
  const store = storage ?? defaultStorage()
  if (value === null) {
    store?.removeItem(API_BASE_STORAGE_KEY)
    return resolveApiBase({ override: null, storage: store ?? undefined })
  }
  const normalized = normalizeApiBase(value)
  store?.setItem(API_BASE_STORAGE_KEY, normalized)
  return normalized
}

export function resolveApiBase(opts?: {
  override?: string | null
  buildDefault?: string
  pageOrigin?: string
  storage?: Storage
}): string {
  const override = opts && 'override' in opts ? opts.override : readApiBaseOverride(opts?.storage)
  // 空串覆盖会吞掉 VITE_API_BASE，真机 https://localhost 上收信/归类会 Failed to fetch
  if (override !== null && override !== undefined && String(override).trim() !== '') {
    const normalized = normalizeApiBase(override, opts?.pageOrigin)
    if (normalized) return normalized
  }
  const build = opts?.buildDefault ?? buildDefaultFromEnv()
  return build ? normalizeApiBase(build, opts?.pageOrigin) : ''
}

export function displayApiBase(opts?: {
  resolved?: string
  pageOrigin?: string
}): string {
  const resolved = opts?.resolved ?? resolveApiBase()
  if (resolved) return resolved
  return pageOriginFallback(opts?.pageOrigin)
}

export async function probeHealthz(
  base: string,
  fetchImpl: typeof fetch = fetch,
): Promise<ProbeHealthzResult> {
  const prefix = base.replace(/\/+$/, '')
  const url = `${prefix}/healthz`
  try {
    const res = await fetchImpl(url, { method: 'GET' })
    const text = (await res.text()).trim()
    if (res.ok && text === 'ok') return { ok: true }
    if (!res.ok) return { ok: false, error: `HTTP ${res.status}` }
    return { ok: false, error: text || 'unexpected-body' }
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : String(err) }
  }
}
