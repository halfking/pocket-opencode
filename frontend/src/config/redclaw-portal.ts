/**
 * RedClaw 门户（platform-go / portal API）基址配置。
 *
 * 背景（设计 03 §8 / 04 §4，storage-sync 策略）：openpocket 的 RSS / 邮件 /
 * 笔记 / 会议记录等模块的「存储与同步策略」统一存 RedClaw 后台，端点族为
 * /api/v1/portal/sync-policy 等。RedClaw 平台 API 与 openpocket 自身的
 * pocketd 后端**可能不是同一台**，所以这里单独给一个可配置项，而不是复用
 * api/http.ts 里写死的 resolveRuntimeApiBase()。
 *
 * 解析优先级（高 → 低）：
 *   1. 构建期环境变量 VITE_REDCLAW_PORTAL_BASE（部署时烘进 bundle）；
 *   2. localStorage 覆盖（op_redclaw_portal_base，预留运行期切换入口）；
 *   3. 现有网关/平台地址（resolveRuntimeApiBase() 的解析结果）；
 *   4. 都没有 ⇒ 空串（相对路径 = 同源；请求失败由调用方降级为本地默认策略，
 *      不阻塞 UI —— 见 stores/syncPolicyStore.ts 的 load() 回退链）。
 *
 * 空串/不可达**不是错误**：sync-policy 是增强能力，门户未部署时策略一律
 * 先暂停上传，直到取得有效的服务端策略。
 */

import { resolveRuntimeApiBase, type StorageLike } from './api-base.ts'

export const REDCLAW_PORTAL_BASE_STORAGE_KEY = 'op_redclaw_portal_base'

function defaultStorage(): StorageLike | null {
  try {
    return typeof localStorage !== 'undefined' ? localStorage : null
  } catch {
    return null
  }
}

function readEnvOverride(): string {
  try {
    return String(import.meta.env?.VITE_REDCLAW_PORTAL_BASE || '').trim()
  } catch {
    return ''
  }
}

function readStoredOverride(storage?: StorageLike): string {
  const store = storage ?? defaultStorage()
  if (!store) return ''
  try {
    return String(store.getItem(REDCLAW_PORTAL_BASE_STORAGE_KEY) || '').trim()
  } catch {
    return ''
  }
}

/** 去尾斜杠；非法 URL 原样丢弃（返回空串 = 回退默认链）。 */
function normalizeBase(raw: string): string {
  const trimmed = raw.trim().replace(/\/+$/, '')
  if (!trimmed) return ''
  try {
    const url = new URL(trimmed)
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return ''
    return `${url.origin}${url.pathname === '/' ? '' : url.pathname.replace(/\/+$/, '')}`
  } catch {
    return ''
  }
}

/**
 * RedClaw 门户 API 基址（不带尾斜杠）。
 * 返回空串 = 未配置（调用方走同源相对路径，失败则降级本地默认策略）。
 */
export function resolveRedclawPortalBase(opts?: { storage?: StorageLike }): string {
  const fromEnv = normalizeBase(readEnvOverride())
  if (fromEnv) return fromEnv
  const stored = normalizeBase(readStoredOverride(opts?.storage))
  if (stored) return stored
  // 现有网关/平台地址；没有 ⇒ 空串。
  return resolveRuntimeApiBase()
}

/** 运行期覆盖门户基址（写 localStorage；空串/null 清除覆盖回到默认链）。 */
export function persistRedclawPortalBase(value: string | null): string {
  const store = defaultStorage()
  if (!store) return resolveRedclawPortalBase()
  if (value === null) {
    try {
      store.removeItem(REDCLAW_PORTAL_BASE_STORAGE_KEY)
    } catch {
      /* 隐私模式等：忽略 */
    }
  } else {
    const normalized = normalizeBase(value)
    if (!normalized) return resolveRedclawPortalBase()
    try {
      store.setItem(REDCLAW_PORTAL_BASE_STORAGE_KEY, normalized)
    } catch {
      /* 写不进就当没配过 */
    }
  }
  return resolveRedclawPortalBase()
}
