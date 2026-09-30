/**
 * pocketd API 基址 SSOT。
 *
 * 优先级：localStorage 覆盖 > VITE_API_BASE > 同源（空串，相对 /api）。
 * 设置页「后端服务器」和登录页展示都读这里，不再用 window.location.origin 冒充。
 */

export const API_BASE_STORAGE_KEY = 'pocket_api_base'
export const PRODUCTION_API_BASE = 'https://pocket.itestu.cn'
/**
 * 备用入口：与生产入口互为热备，二者可在系统设置中来回切换。
 * 同一个后端域名 `pocket.kxpms.cn` 在不同 CDN / 边缘节点上对外暴露。
 * 切换会清掉 selected_instance 与已登录 session（强制重登，避免跨节点态错乱）。
 */
export const BACKUP_API_BASE = 'https://pocket.kxpms.cn'

export type ProbeHealthzResult = { ok: true } | { ok: false; error: string }

/**
 * 最小存储契约。真实 localStorage 天然满足它，单测里的内存实现也能满足，
 * 避免为了注入一个假存储而被迫实现 length/clear/key。
 */
export interface StorageLike {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

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

export function readApiBaseOverride(storage?: StorageLike): string | null {
  const store = storage ?? defaultStorage()
  if (!store) return null
  return store.getItem(API_BASE_STORAGE_KEY)
}

export function persistApiBase(value: string | null, storage?: StorageLike): string {
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
  storage?: StorageLike
}): string {
  const override = opts && 'override' in opts ? opts.override : readApiBaseOverride(opts?.storage)
  // A browser can use its same-origin /api proxy. A Capacitor localhost origin
  // is only the app shell, so keep its configured backend fallback instead.
  if (override !== null && override !== undefined) {
    const normalized = normalizeApiBase(override, opts?.pageOrigin)
    if (normalized || !isCapacitorShellOrigin(pageOriginFallback(opts?.pageOrigin))) {
      return normalized
    }
  }
  const build = opts?.buildDefault ?? buildDefaultFromEnv()
  return build ? normalizeApiBase(build, opts?.pageOrigin) : ''
}

/**
 * Capacitor 本地壳的 origin 白名单。
 *
 * 必须是 scheme 无关的：`androidScheme` 是 BUG-F 引入的逃生舱，
 * 联调时可以整成 `http`，此时页面 origin 变成 `http://localhost`（无端口）。
 * 旧实现只硬编码 `https://localhost` / `capacitor://localhost`，
 * 于是 http 壳下守卫失效、base 解析成空串，`/api/*` 全部打到本地 index.html。
 */
const CAPACITOR_SHELL_ORIGIN = /^(https?|capacitor):\/\/localhost$/i

/** 该 origin 是否为 Capacitor WebView 的本地壳（同源 ≠ 后端，必须回退真实 base）。 */
export function isCapacitorShellOrigin(origin: string | undefined | null): boolean {
  return CAPACITOR_SHELL_ORIGIN.test(String(origin ?? '').trim())
}

/** Capacitor WebView origin 是 http(s)://localhost；空 base 时回退生产入口，避免 /api 打到本地壳。 */
export function resolveRuntimeApiBase(opts?: {
  override?: string | null
  buildDefault?: string
  pageOrigin?: string
  storage?: StorageLike
}): string {
  const resolved = resolveApiBase(opts)
  if (resolved) return resolved
  const origin = pageOriginFallback(opts?.pageOrigin)
  if (isCapacitorShellOrigin(origin)) return PRODUCTION_API_BASE
  return ''
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
    if (res.ok && text === 'frontend ok') {
      const backend = await fetchImpl(`${prefix}/api/healthz`, { method: 'GET' })
      const backendText = (await backend.text()).trim()
      if (backend.ok && backendText === 'ok') return { ok: true }
      return {
        ok: false,
        error: backend.ok ? backendText || 'unexpected-body' : `HTTP ${backend.status}`,
      }
    }
    if (!res.ok) return { ok: false, error: `HTTP ${res.status}` }
    return { ok: false, error: text || 'unexpected-body' }
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : String(err) }
  }
}
