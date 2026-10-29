/**
 * syncPolicy.ts — RedClaw 门户「存储与同步策略」API client。
 *
 * 端点族（platform-go 提供，见设计 03 §8 / 04 §4）：
 *   GET  /api/v1/portal/sync-policy   读策略
 *   PUT  /api/v1/portal/sync-policy   只改 modules（body: { modules: {...} }）
 *   POST /api/v1/portal/sync/v2/purge 清除可信工作区副本（持久 reset_id + epoch 栅栏）
 *
 * 与 api/http.ts 的差别：门户 API 的 base 是 **redclawPortalBase**
 * （config/redclaw-portal.ts），不是 pocketd 的 resolveRuntimeApiBase()。
 * 鉴权沿用同一机制：注入 Bearer token（openpocket 登录态，token 由 RedClaw
 * Admin 签发、pocketd 透传 —— 见 stores/auth.ts 头注释）。
 *
 * 请求失败（未配置 / 不可达 / 401）由调用方（stores/syncPolicyStore.load）
 * 使用缓存；没有有效缓存时暂停上传，不阻塞 UI。
 *
 * 类型与纯解析在 ./syncPolicyContract.ts（零依赖，供 store / 同步引擎使用），
 * 本文件 re-export 以维持「契约从 api/syncPolicy 导入」的单一入口。
 */

import { resolveRedclawPortalBase } from '../config/redclaw-portal.ts'
import { useAuthStore, captureAuthSession, assertAuthSession } from '../stores/auth'
import { fetchWithTimeout } from './fetch-timeout.ts'
import { capturePortalPurgeOperation } from '../native/portal-sync/purge-operation'
import {
  isPortalSyncModule,
  normalizeSyncPolicy,
  portalPolicyModules,
  type SyncMode,
  type PortalSyncModule,
  type SyncModule,
  type SyncPolicy,
  type SyncPolicyPutBody,
} from './syncPolicyContract.ts'

export {
  SYNC_MODULES,
  defaultSyncPolicy,
  isSyncMode,
  isSyncModule,
  normalizeSyncPolicy,
} from './syncPolicyContract.ts'
export type {
  SyncMode,
  SyncModule,
  SyncPolicy,
  SyncPolicyPutBody,
} from './syncPolicyContract.ts'

/** 门户请求错误（含状态码；调用方只用于日志/toast，不用于流程分支）。 */
export class SyncPolicyApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.name = 'SyncPolicyApiError'
    this.status = status
  }
}

async function portalFetch(path: string, init: RequestInit = {}): Promise<unknown> {
  const base = resolveRedclawPortalBase()
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(init.headers as Record<string, string> | undefined),
  }
  const auth = useAuthStore()
  const session = captureAuthSession(auth)
  if (auth.token) headers['Authorization'] = `Bearer ${auth.token}`

  // 30s 上界：策略读取挂死不得拖住设置页（与 api/http.ts 同口径）。
  const res = await fetchWithTimeout(`${base}${path}`, { ...init, headers })
  assertAuthSession(auth, session)
  if (!res.ok) {
    let message = res.statusText
    try {
      const body = (await res.json()) as { error?: unknown }
      if (typeof body.error === 'string' && body.error.trim()) message = body.error.trim()
    } catch {
      /* 非 JSON body：用 statusText */
    }
    assertAuthSession(auth, session)
    throw new SyncPolicyApiError(res.status, message)
  }
  if (res.status === 204) return undefined
  const body = await res.json()
  assertAuthSession(auth, session)
  return body
}

/** GET /api/v1/portal/sync-policy */
export async function fetchSyncPolicy(): Promise<SyncPolicy> {
  const raw = await portalFetch('/api/v1/portal/sync-policy', { method: 'GET' })
  return normalizeSyncPolicy(raw)
}

/** PUT /api/v1/portal/sync-policy（body 只带 modules） */
export async function updateSyncPolicyModules(
  modules: Record<SyncModule, SyncMode>,
): Promise<SyncPolicy> {
  const body: SyncPolicyPutBody = { modules: portalPolicyModules(modules) }
  const raw = await portalFetch('/api/v1/portal/sync-policy', {
    method: 'PUT',
    body: JSON.stringify(body),
  })
  return normalizeSyncPolicy(raw)
}

/** Trusted v2 purge; uncertain retries retain their persisted reset_id. */
export async function purgeCloudCopy(
  modules: SyncModule[],
): Promise<{ purged: Partial<Record<PortalSyncModule, number>> }> {
  if (!modules.length || !modules.every(isPortalSyncModule)) {
    throw new SyncPolicyApiError(400, 'purge requires a supported portal module')
  }
  const operation = capturePortalPurgeOperation(modules as PortalSyncModule[])
  try {
    const { purgePortalCloudCopy } = await import('../native/portal-sync/purge.ts')
    operation.assertCurrent()
    return await purgePortalCloudCopy(operation)
  } finally { operation.dispose() }
}
