/**
 * syncPolicyStore — 「存储与同步」策略的 Pinia store。
 *
 * 数据流（设计 03 §8 / 04 §4）：
 *   load()：远端 GET /api/v1/portal/sync-policy
 *     ├─ 成功 → 写 state + localStorage 缓存（op_sync_policy_v1）
 *     ├─ 失败（未配置/不可达/未登录）→ 读缓存 → 仍没有 → 本地四个门户模块的安全默认 { local }
 *     └─ 任何失败都**不阻塞 UI**：设置页永远可以渲染（未确认策略时暂停上传）。
 *   setModule(module, mode)：乐观更新 + 缓存 → PUT；失败回滚并抛错（组件 toast）。
 *   lastSyncReport：同步引擎按模块上报 {module,status:'synced'|'skipped',count}，
 *     内存（本 store，供设置页渲染）+ localStorage（op_sync_report_v1，冷启动恢复）。
 *
 * ## 与同步引擎的边界（重要）
 *
 * native/mobileSyncRuntime 等同步代码**不得 import Pinia 模块**（其头注释的
 * 既有约定）。因此引擎侧用的是本文件导出的两个免 Pinia 入口：
 *   - readCachedModuleMode(module)：直接读当前策略缓存（缺省 local）；
 *   - recordSyncReportEntry(entry)：localStorage 为 SSOT 写报告；若当前有
 *     活动 Pinia（getActivePinia）则同步更新 store state，设置页实时可见。
 * 两个入口在 Node（node --test）下都可加载：localStorage 访问全部带守卫。
 *
 * ## 测试注入
 *
 * 传输层（transport）默认懒加载 ../api/syncPolicy.ts（运行期才 import，
 * 本文件静态依赖因此只有 pinia）。测试直接改写 syncPolicyTransport 的方法，
 * 并用 installSyncPolicyStorage 注入内存存储 —— 不需要网络，也不需要拉起
 * api 层的 import 链。
 */

import { defineStore, getActivePinia } from 'pinia'
// ⚠ 运行时值只能从 syncPolicyContract（零依赖）取：api/syncPolicy 会拉进
//   stores/auth → api/auth（无扩展名 import），Node 的 ESM 解析器解不开那条链。
import {
  isSyncModule as isKnownSyncModule,
  normalizeSyncPolicy,
  isPortalSyncModule,
  SYNC_MODULES,
  type SyncMode,
  type SyncModule,
  type SyncPolicy,
} from '../api/syncPolicyContract.ts'

// ---------------------------------------------------------------------------
// 常量与类型
// ---------------------------------------------------------------------------

export const SYNC_POLICY_CACHE_KEY = 'op_sync_policy_v1'
export const SYNC_REPORT_CACHE_KEY = 'op_sync_report_v1'

export interface SyncReportEntry {
  module: SyncModule
  status: 'synced' | 'skipped'
  count?: number
  /** 上报时间（Unix ms）。 */
  at: number
}

/** 策略缓存形状（多包一层 lastFetchedAt 供「上次拉取」展示/排障）。 */
export interface SyncPolicyCache {
  policy: SyncPolicy
  lastFetchedAt: number
  scope?: string
}

// ---------------------------------------------------------------------------
// 传输层（可注入）
// ---------------------------------------------------------------------------

export interface SyncPolicyTransport {
  fetchPolicy(): Promise<SyncPolicy>
  putModules(modules: Record<SyncModule, SyncMode>): Promise<SyncPolicy>
}

/** 生产传输层：懒加载 api/syncPolicy（本文件的静态 import 图保持「仅 pinia」）。 */
export const syncPolicyTransport: SyncPolicyTransport = {
  async fetchPolicy(): Promise<SyncPolicy> {
    const api = await import('../api/syncPolicy.ts')
    return api.fetchSyncPolicy()
  },
  async putModules(modules: Record<SyncModule, SyncMode>): Promise<SyncPolicy> {
    const api = await import('../api/syncPolicy.ts')
    return api.updateSyncPolicyModules(modules)
  },
}

// ---------------------------------------------------------------------------
// 存储访问（Node 可加载：localStorage 全部带守卫，测试可注入内存实现）
// ---------------------------------------------------------------------------

interface StorageLike {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

let injectedStorage: StorageLike | null = null
let scopeResolver: (() => string) | null = null
// 写入被浏览器拒绝时，过滤器仍须看到本进程已生效的策略。
let volatileCache: SyncPolicyCache | null = null
let nextPolicyGeneration = 0

/** 测试注入内存存储；传 null 恢复默认（真实 localStorage 或不可用）。 */
export function installSyncPolicyStorage(storage: StorageLike | null): void {
  injectedStorage = storage
  volatileCache = null
  scopeResolver = null
}

/** 宿主优先用内存登录态，避免登录态写盘失败时仍读取旧账号策略。 */
export function installSyncPolicyScopeResolver(resolve: () => string): void {
  scopeResolver = resolve
}

function policyStorage(): StorageLike | null {
  if (injectedStorage) return injectedStorage
  try {
    return typeof localStorage !== 'undefined' ? localStorage : null
  } catch {
    return null
  }
}

function readCache(): SyncPolicyCache | null {
  if (volatileCache?.scope === policyScope()) return volatileCache
  const store = policyStorage()
  if (!store) return null
  try {
    const raw = store.getItem(SYNC_POLICY_CACHE_KEY)
    if (!raw) return null
    const parsed = JSON.parse(raw) as Partial<SyncPolicyCache> & { modules?: unknown }
    if (!parsed || typeof parsed !== 'object') return null
    if ((parsed.scope ?? '') !== policyScope()) return null
    // 缓存损坏按「没有缓存」处理（策略可随时重新拉取，不值得修）。
    const policy =
      'policy' in parsed
        ? normalizeSyncPolicy(parsed.policy)
        : normalizeSyncPolicy(parsed) // 旧形状：顶层就是策略
    const lastFetchedAt = Number(parsed.lastFetchedAt) > 0 ? Number(parsed.lastFetchedAt) : 0
    return { policy, lastFetchedAt, scope: parsed.scope }
  } catch {
    return null
  }
}

function writeCache(cache: SyncPolicyCache): void {
  cache = { ...cache, scope: policyScope() }
  volatileCache = cache
  const store = policyStorage()
  if (!store) {
    notifyPolicyChange()
    return
  }
  try {
    store.setItem(SYNC_POLICY_CACHE_KEY, JSON.stringify(cache))
    volatileCache = null
  } catch {
    // 隐私模式等写不进：内存态仍有效，下次 load 走远端/默认链。
  }
  notifyPolicyChange()
}

function notifyPolicyChange(): void {
  if (typeof window !== 'undefined') window.dispatchEvent(new Event('openpocket:sync-policy-changed'))
}

function policyScope(): string {
  try {
    if (scopeResolver) return scopeResolver()
    const storage = policyStorage()
    const user = storage?.getItem('pocket_user') ?? ''
    const workspace = storage?.getItem('pocket_workspace_id') ?? ''
    return user || workspace ? JSON.stringify([user, workspace]) : ''
  } catch { return 'unavailable' }
}

export function readCachedSyncPolicy(): SyncPolicyCache | null {
  return readCache()
}

// ---------------------------------------------------------------------------
// 纯函数
// ---------------------------------------------------------------------------

/**
 * 显式 modules 值优先，缺失回退 system_default；未知状态暂停上传。
 */
export function moduleMode(
  policy: Pick<SyncPolicy, 'system_default'> & { modules?: Partial<Record<SyncModule, SyncMode>> },
  module: SyncModule,
): SyncMode {
  const explicit = policy.modules?.[module]
  if (explicit === 'cloud' || explicit === 'local') return explicit
  return policy.system_default === 'cloud' ? 'cloud' : 'local'
}

/**
 * shouldSync(policy, module) — 该模块的数据是否应上传/同步到云端。
 * 纯函数：同步引擎与 store 共用同一判定，避免两边口径漂移。
 */
export function shouldSync(
  policy: Pick<SyncPolicy, 'system_default'> & { modules?: Partial<Record<SyncModule, SyncMode>> },
  module: SyncModule,
): boolean {
  return moduleMode(policy, module) !== 'local'
}

/**
 * 免 Pinia 的同步引擎入口：读缓存里某模块的当前模式。
 * 门户模块缓存缺失/损坏时为 'local'；未受管模块保留既有同步默认。
 */
export function readCachedModuleMode(module: SyncModule): SyncMode {
  const cached = readCache()
  if (!cached) return isPortalSyncModule(module) ? 'local' : 'cloud'
  return moduleMode(cached.policy, module)
}

// ---------------------------------------------------------------------------
// 同步报告（localStorage 为 SSOT + 活动 Pinia 实时更新）
// ---------------------------------------------------------------------------

function readReport(): SyncReportEntry[] {
  const store = policyStorage()
  if (!store) return []
  try {
    const raw = store.getItem(SYNC_REPORT_CACHE_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw) as unknown
    if (!Array.isArray(parsed)) return []
    const order = new Map(SYNC_MODULES.map((m, i) => [m, i]))
    return parsed
      .filter(
        (e): e is SyncReportEntry =>
          !!e &&
          typeof e === 'object' &&
          isKnownSyncModule((e as SyncReportEntry).module) &&
          ((e as SyncReportEntry).status === 'synced' ||
            (e as SyncReportEntry).status === 'skipped') &&
          Number.isFinite(Number((e as SyncReportEntry).at)),
      )
      .sort((a, b) => (order.get(a.module) ?? 99) - (order.get(b.module) ?? 99))
  } catch {
    return []
  }
}

function writeReport(entries: SyncReportEntry[]): void {
  const store = policyStorage()
  if (!store) return
  try {
    store.setItem(SYNC_REPORT_CACHE_KEY, JSON.stringify(entries))
  } catch {
    // 报告是可再生的展示数据：写不进只影响持久化，不影响本轮同步。
  }
}

/** 供 store 内部 hydrate / 引擎侧 record 使用。 */
export function readCachedSyncReport(): SyncReportEntry[] {
  return readReport()
}

/**
 * 同步引擎上报入口（免 Pinia）：合并进报告（同模块覆盖旧条目）并落 localStorage；
 * 若当前有活动 Pinia 实例则同步更新 store state（设置页「上次同步」实时刷新）。
 */
export function recordSyncReportEntry(
  entry: Omit<SyncReportEntry, 'at'> & { at?: number },
): void {
  const full: SyncReportEntry = { ...entry, at: entry.at ?? Date.now() }
  const next = readReport().filter((e) => e.module !== full.module)
  next.push(full)
  const order = new Map(SYNC_MODULES.map((m, i) => [m, i]))
  next.sort((a, b) => (order.get(a.module) ?? 99) - (order.get(b.module) ?? 99))
  writeReport(next)
  try {
    const pinia = getActivePinia()
    if (pinia) {
      // 动态 require 不可行（ESM）；直接用 store 定义（本文件就是定义处）。
      const store = useSyncPolicyStore(pinia)
      store.lastSyncReport = next
    }
  } catch {
    // 无活动 Pinia（同步引擎在纯 Node 测试里跑）：localStorage 已是 SSOT。
  }
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

export const useSyncPolicyStore = defineStore('syncPolicy', {
  state: () => {
    const cached = readCache()
    const policy = cached?.policy ?? normalizeSyncPolicy(undefined)
    return {
      systemDefault: policy.system_default,
      allowOverride: policy.allow_override,
      modules: { ...policy.modules } as Record<SyncModule, SyncMode>,
      lastFetchedAt: cached?.lastFetchedAt ?? 0,
      /** 远端拉取失败的原因（展示用；空串 = 成功或尚未拉取）。 */
      loadError: '' as string,
      /** load() 是否已跑过（设置页只 load 一次）。 */
      loaded: false as boolean,
      saving: false,
      revision: 0,
      generation: ++nextPolicyGeneration,
      lastSyncReport: readReport(),
    }
  },
  getters: {
    /**
     * 当前策略快照（纯函数 shouldSync 的入参形状）。
     * modules 拷贝一份，避免调用方拿到 state 引用后误改。
     */
    policy(state): SyncPolicy {
      return {
        system_default: state.systemDefault,
        allow_override: state.allowOverride,
        modules: { ...state.modules },
      }
    },
    moduleModes(state): Record<SyncModule, SyncMode> {
      return state.modules
    },
  },
  actions: {
    /**
     * 拉取策略：远端 → 缓存 → 暂停上传。失败不抛（降级语义），
     * 只把原因写进 loadError 供设置页如实提示。
     */
    async load(): Promise<void> {
      if (this.saving) return
      const scope = policyScope()
      const revision = this.revision
      const generation = this.generation
      try {
        const remote = await syncPolicyTransport.fetchPolicy()
        if (scope !== policyScope() || generation !== this.generation || revision !== this.revision) return
        this.applyPolicy(remote, Date.now())
        this.loadError = ''
      } catch (err) {
        if (scope !== policyScope() || generation !== this.generation || revision !== this.revision) return
        // 降级链：缓存已在 state 初始化时 hydrate；这里只确保即便 state 是
        // 默认值也有一份缓存可回读（不写也行——默认值本身会暂停上传）。
        this.loadError = err instanceof Error ? err.message : String(err)
      } finally {
        if (scope === policyScope() && generation === this.generation && revision === this.revision) this.loaded = true
      }
    },

    /** 内部：把一份策略落到 state + 缓存。 */
    applyPolicy(policy: SyncPolicy, fetchedAt: number): void {
      const normalized = normalizeSyncPolicy(policy)
      // 门户未管理的两个模块保留本机既有选择，不因四模块响应被重置。
      normalized.modules['ai-chat'] = this.modules['ai-chat']
      normalized.modules.tasks = this.modules.tasks
      this.systemDefault = normalized.system_default
      this.allowOverride = normalized.allow_override
      this.modules = { ...normalized.modules }
      this.lastFetchedAt = fetchedAt
      writeCache({ policy: normalized, lastFetchedAt: fetchedAt })
    },

    /**
     * 切换单模块模式：乐观更新 + PUT，失败回滚并抛错（组件 toast）。
     * 平台关闭 allow_override 时直接拒绝（策略由管理员统一管控）。
     */
    async setModule(module: SyncModule, mode: SyncMode): Promise<boolean> {
      if (!isPortalSyncModule(module) || !this.allowOverride || this.saving) return false
      const prev = this.modules[module]
      if (prev === mode) return true
      const scope = policyScope()
      const generation = this.generation
      this.saving = true
      this.revision++
      const requested = { ...this.modules, [module]: mode }
      // 关闭上传立即生效；开启上传必须等服务端确认，不能让失败的 PUT 泄露数据。
      if (mode === 'local') this.modules = requested
      // 乐观写缓存：同步引擎立刻按新模式过滤（不等 PUT 往返）。
      writeCache({
        policy: {
          system_default: this.systemDefault,
          allow_override: this.allowOverride,
          modules: { ...this.modules },
        },
        lastFetchedAt: this.lastFetchedAt,
      })
      try {
        const remote = await syncPolicyTransport.putModules(requested)
        if (scope !== policyScope() || generation !== this.generation) return false
        // 服务端是权威：以 PUT 响应为准回写（可能带服务端规整）。
        this.applyPolicy(remote, this.lastFetchedAt || Date.now())
        return true
      } catch (err) {
        if (scope !== policyScope() || generation !== this.generation) return false
        // 回滚 state + 缓存。
        this.modules = { ...this.modules, [module]: prev }
        writeCache({
          policy: {
            system_default: this.systemDefault,
            allow_override: this.allowOverride,
            modules: { ...this.modules },
          },
          lastFetchedAt: this.lastFetchedAt,
        })
        throw err
      } finally {
        if (scope === policyScope() && generation === this.generation) this.saving = false
      }
    },

    /** 报告被外部（同步引擎）写过后手动同步内存态（一般无需调用）。 */
    refreshReport(): void {
      this.lastSyncReport = readReport()
    },
  },
})
