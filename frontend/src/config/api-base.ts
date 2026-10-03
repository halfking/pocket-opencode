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

/**
 * 解析结果的来源，决定后续能不能做「不可达就丢弃」的判断。
 * - `override`：用户在「后端服务器」页显式选过（含空串＝显式同源）
 * - `build`：构建期烘进来的 VITE_API_BASE
 * - `origin`：同源（相对 /api）
 */
export type ApiBaseSource = 'override' | 'build' | 'origin'

export interface ResolvedApiBase {
  base: string
  source: ApiBaseSource
  /** 构建默认值因「设备上不可达」被丢弃并改用生产入口；供 UI 如实提示。 */
  loopbackBuildRejected?: boolean
}

/**
 * 该 base 的主机是不是 loopback（设备/本机的自己）。
 *
 * 在 Capacitor 壳里页面 origin 是 localhost，所以「localhost:18099」这种
 * 构建默认值打的是**手机自己**，不是后端：只有 `adb reverse` 这类开发拐杖
 * 才能把它打通，真机上不存在。实测（模拟器，已拔掉 adb reverse）：
 *   device -> localhost:18099  Connection refused
 *   device -> 10.0.2.2:18099    HTTP/1.0 200 OK
 * 同理 0.0.0.0 与 ::1 作为目标地址也都是本机。
 */
export function isLoopbackApiBase(base: string | null | undefined): boolean {
  const raw = String(base ?? '').trim()
  if (!raw) return false
  let url: URL
  try {
    url = new URL(raw)
  } catch {
    return false
  }
  const host = url.hostname.toLowerCase()
  // URL 的 hostname 对 IPv6 保留方括号（`[::1]`），两种写法都收。
  if (host === 'localhost' || host === '0.0.0.0' || host === '::1' || host === '[::1]') return true
  // 整个 127.0.0.0/8 都是 loopback。
  return /^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(host)
}

/**
 * 解析生效 base，并带上来源。
 *
 * 规则（顺序即优先级）：
 * 1. 有 override：归一化后非空就用它（`source=override`）；归一成空串＝显式同源，
 *    Web 下就此打住，Capacitor 壳下继续往下走（壳的 origin 只是壳，不是后端）。
 * 2. 构建默认值：Capacitor 壳上**且**是 loopback 时丢弃——真机不可达，
 *    宁可回落到生产入口也不要静默打向设备自己。用户显式填的地址不受影响，
 *    因为 `adb reverse` 开发流确实需要用户主动指定 localhost。
 * 3. 都没有：同源（`source=origin`）。
 */
export function resolveApiBaseWithSource(opts?: {
  override?: string | null
  buildDefault?: string
  pageOrigin?: string
  storage?: StorageLike
}): ResolvedApiBase {
  const origin = pageOriginFallback(opts?.pageOrigin)
  const onShell = isCapacitorShellOrigin(origin)
  const override = opts && 'override' in opts ? opts.override : readApiBaseOverride(opts?.storage)
  // A browser can use its same-origin /api proxy. A Capacitor localhost origin
  // is only the app shell, so keep its configured backend fallback instead.
  if (override !== null && override !== undefined) {
    const normalized = normalizeApiBase(override, opts?.pageOrigin)
    if (normalized || !onShell) {
      return { base: normalized, source: normalized ? 'override' : 'origin' }
    }
  }
  const build = opts?.buildDefault ?? buildDefaultFromEnv()
  if (build) {
    const normalized = normalizeApiBase(build, opts?.pageOrigin)
    if (normalized) {
      if (onShell && isLoopbackApiBase(normalized)) {
        return { base: PRODUCTION_API_BASE, source: 'build', loopbackBuildRejected: true }
      }
      return { base: normalized, source: 'build' }
    }
  }
  return { base: '', source: 'origin' }
}

export function resolveApiBase(opts?: {
  override?: string | null
  buildDefault?: string
  pageOrigin?: string
  storage?: StorageLike
}): string {
  return resolveApiBaseWithSource(opts).base
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

/**
 * 登录页/设置页展示用的「当前实际打向哪里」。
 *
 * 必须走 resolveRuntimeApiBase 而不是 resolveApiBase：设备上两者可能不同
 * （构建默认值里的 localhost 会被换成生产入口），展示若用旧口径，用户看到的
 * 地址就不是真正在用的地址。
 */
export function displayApiBase(opts?: {
  resolved?: string
  pageOrigin?: string
}): string {
  const resolved = opts?.resolved ?? resolveRuntimeApiBase()
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
    // 'frontend ok' 是 nginx 纯前端部署的哨兵（deploy/本地方案/nginx.conf:
    //   location = /healthz { return 200 "frontend ok\n"; }），与该文件保持
    //   字面一致。它只证明前端容器活着，必须再穿透 /api/healthz 确认后端。
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
