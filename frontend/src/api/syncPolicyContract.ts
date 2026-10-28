/**
 * syncPolicyContract.ts — 「存储与同步策略」的类型与纯解析（零依赖）。
 *
 * 单独成文件的原因：stores/syncPolicyStore 与同步引擎（native/*，不得依赖
 * Pinia/api 链）需要这些**运行时**值（SYNC_MODULES / normalize …），而
 * syncPolicy.ts 会拉进 stores/auth → api/auth（无扩展名 import，Node 的
 * ESM 解析器解不了）。契约放这里，api/syncPolicy.ts 只做请求层并 re-export。
 */

/** 策略覆盖的六个业务模块（与门户 /api/v1/portal/sync-policy 契约一致）。 */
export type SyncModule = 'ai-chat' | 'email' | 'rss' | 'notes' | 'meetings' | 'tasks'

export const SYNC_MODULES: readonly SyncModule[] = [
  'ai-chat',
  'email',
  'rss',
  'notes',
  'meetings',
  'tasks',
]

export type SyncMode = 'cloud' | 'local'

/** GET /api/v1/portal/sync-policy 的响应形状。 */
export interface SyncPolicy {
  system_default: SyncMode
  allow_override: boolean
  modules: Record<SyncModule, SyncMode>
}

/** PUT body：只提交 modules（system_default / allow_override 由平台管理）。 */
export interface SyncPolicyPutBody {
  modules: Record<SyncModule, SyncMode>
}

export function isSyncMode(v: unknown): v is SyncMode {
  return v === 'cloud' || v === 'local'
}

export function isSyncModule(v: unknown): v is SyncModule {
  return typeof v === 'string' && (SYNC_MODULES as readonly string[]).includes(v)
}

/** 全 cloud 的本地默认策略（门户不可达 / 未配置时的降级值）。 */
export function defaultSyncPolicy(): SyncPolicy {
  return {
    system_default: 'cloud',
    allow_override: true,
    modules: {
      'ai-chat': 'cloud',
      email: 'cloud',
      rss: 'cloud',
      notes: 'cloud',
      meetings: 'cloud',
      tasks: 'cloud',
    },
  }
}

/** 宽容解析：服务端漏字段 / 多余模块键都不至于炸掉整个设置页。 */
export function normalizeSyncPolicy(raw: unknown): SyncPolicy {
  const fallback = defaultSyncPolicy()
  if (!raw || typeof raw !== 'object') return fallback
  const obj = raw as Partial<SyncPolicy> & { modules?: unknown }
  const modules = { ...fallback.modules }
  if (obj.modules && typeof obj.modules === 'object') {
    for (const [k, v] of Object.entries(obj.modules as Record<string, unknown>)) {
      if (isSyncModule(k) && isSyncMode(v)) modules[k] = v
    }
  }
  return {
    system_default: isSyncMode(obj.system_default) ? obj.system_default : fallback.system_default,
    allow_override: typeof obj.allow_override === 'boolean' ? obj.allow_override : true,
    modules,
  }
}
