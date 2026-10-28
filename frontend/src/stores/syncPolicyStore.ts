/**
 * Portal policy transport is separate from effective policy and upload admission.
 * Disk cache is only a candidate: the owner and complete LocalDB policy barrier
 * must confirm it before any cloud reader may use it. Local requests revoke
 * admission synchronously; state/cache publish once old DB work has drained.
 * Native and stream consumers use readCachedModuleMode plus the change event.
 */

import { defineStore, getActivePinia } from 'pinia'
import type { PolicySnapshot, PolicySnapshotPublisher } from '../native/local-db-leases'
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
export const SYNC_POLICY_LOCAL_INTENTS_KEY = 'op_sync_policy_local_intents_v1'
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
  readonly length?: number
  key?(index: number): string | null
  keys?(): Iterable<string>
}

let injectedStorage: StorageLike | null = null
let scopeResolver: (() => string) | null = null
// 写入被浏览器拒绝时，过滤器仍须看到本进程已生效的策略。
let volatileCache: SyncPolicyCache | null = null
let nextPolicyGeneration = 0
interface LocalIntent {
  version: 1
  scope: string
  workspace?: string
  module: SyncModule
}
const INTENT_PREFIX = `${SYNC_POLICY_LOCAL_INTENTS_KEY}:`
const MAX_STORAGE_KEYS = 4096
const volatileIntents = new Map<string, LocalIntent>()
let intentSequence = 0

function storageKeys(storage: StorageLike): string[] {
  if (storage.keys) {
    const keys: string[] = []
    for (const key of storage.keys()) {
      if (keys.length >= MAX_STORAGE_KEYS) throw new Error('Policy intent storage limit reached.')
      keys.push(key)
    }
    return keys
  }
  if (typeof storage.length !== 'number' || !storage.key || storage.length > MAX_STORAGE_KEYS) throw new Error('Policy intent storage cannot be inspected safely.')
  return Array.from({ length: storage.length }, (_, index) => storage.key!(index)).filter((key): key is string => key !== null)
}
function readLocalIntents(context: SyncPolicyContext): { modules: SyncModule[]; keys: Map<string, LocalIntent>; error: string; volatile: boolean } {
  const found = new Map<string, LocalIntent>()
  try {
    const storage = policyStorage()
    if (!storage) throw new Error('Local policy storage is unavailable.')
    for (const key of storageKeys(storage)) {
      if (!key.startsWith(INTENT_PREFIX)) continue
      const raw = storage.getItem(key)
      if (raw === null) continue // An exact intent may have been acknowledged in another tab.
      if (raw.length > 8192) throw new Error('Invalid local policy intent.')
      const value = JSON.parse(raw)
      if (value?.version !== 1 || typeof value.scope !== 'string' || !isKnownSyncModule(value.module) ||
        (value.workspace !== undefined && typeof value.workspace !== 'string')) throw new Error('Invalid local policy intent.')
      found.set(key, value)
    }
    for (const [key, value] of volatileIntents) found.set(key, value)
    const scope = context.intentScope ?? context.scope
    for (const [key, value] of found) {
      // Missing stable identity on cold auth restoration can only broaden denial.
      const matches = scope && value.scope ? scope === value.scope
        : context.workspace !== undefined ? context.workspace === value.workspace : scope === value.scope
      if (!matches) found.delete(key)
    }
    return { modules: SYNC_MODULES.filter(module => [...found.values()].some(value => value.module === module)), keys: found, error: '', volatile: [...found.keys()].some(key => volatileIntents.has(key)) }
  } catch (error) {
    return { modules: [...SYNC_MODULES], keys: found, error: error instanceof Error ? error.message : String(error), volatile: true }
  }
}
function addLocalIntent(module: SyncModule, context: SyncPolicyContext): { persisted: boolean; key: string; value: LocalIntent } {
  const value: LocalIntent = { version: 1, scope: context.intentScope ?? context.scope, workspace: context.workspace, module }
  const key = `${INTENT_PREFIX}${encodeURIComponent(value.scope).slice(0, 2048)}:${module}:${Date.now()}:${++intentSequence}:${Math.random().toString(36).slice(2)}`
  volatileIntents.set(key, value)
  try {
    const storage = policyStorage()
    if (!storage || value.scope.length > 2048 || storageKeys(storage).length >= MAX_STORAGE_KEYS) return { persisted: false, key, value }
    storage.setItem(key, JSON.stringify(value))
    volatileIntents.delete(key)
    return { persisted: true, key, value }
  } catch { return { persisted: false, key, value } }
}
function clearLocalIntents(keys: Map<string, LocalIntent>, module: SyncModule, context: SyncPolicyContext, ownedKey?: string): boolean {
  try {
    const storage = policyStorage()
    if (!storage) return false
    for (const [key, value] of keys) {
      const scope = context.intentScope ?? context.scope
      if (value.module !== module || (key !== ownedKey && (!scope || value.scope !== scope))) continue
      // Broad workspace matches are refusal metadata, never authority to clear
      // another principal's persisted choice when stable identity is unknown.
      // Keys are immutable and unique per request: no read/modify/write CAS and
      // no older response can remove a newer tab's local choice.
      storage.removeItem(key)
      volatileIntents.delete(key)
    }
    return true
  } catch { return false }
}
function storageWritable(): boolean {
  try {
    const storage = policyStorage()
    if (!storage) return false
    // A cold start cannot trust a cloud candidate when privacy intent cannot be persisted.
    storage.setItem('op_sync_policy_storage_probe', '1')
    storage.removeItem('op_sync_policy_storage_probe')
    return true
  } catch { return false }
}

export interface SyncPolicyContext {
  readonly scope: string
  /** Stable identity for refusal metadata only; empty means identity is unavailable. */
  readonly intentScope?: string
  readonly workspace?: string
  readonly isCurrent: () => boolean
}
export interface SyncPolicyActivationHost {
  captureContext(): SyncPolicyContext | null
  activate(modes: PolicySnapshot, publish: PolicySnapshotPublisher): Promise<void>
}
let activationHost: SyncPolicyActivationHost | null = null
let livePolicy: { cache: SyncPolicyCache; context: SyncPolicyContext; persisted: boolean } | null = null
let closedModules = new Set<SyncModule>(SYNC_MODULES)
const publications = new WeakMap<object, object>()
const reads = new WeakMap<object, object>()
const restorations = new WeakMap<object, { generation: number; context: SyncPolicyContext; host: SyncPolicyActivationHost; promise: Promise<boolean> }>()
class PolicyActivationError extends Error {
  readonly cause?: unknown
  constructor(message: string, cause?: unknown) { super(message); this.name = 'PolicyActivationError'; this.cause = cause }
}
function policyAbort(): Error { return Object.assign(new Error('Policy activation was superseded.'), { name: 'AbortError' }) }
function contextCurrent(context: SyncPolicyContext): boolean {
  try { return context.scope === policyScope() && context.isCurrent() } catch { return false }
}
function freezePolicy(policy: SyncPolicy): SyncPolicy { return Object.freeze({ ...policy, modules: Object.freeze({ ...policy.modules }) }) }
function inactivePolicy(): SyncPolicy {
  return freezePolicy({ system_default: 'local', allow_override: false, modules: Object.fromEntries(SYNC_MODULES.map(module => [module, 'local'])) as Record<SyncModule, SyncMode> })
}
export function installSyncPolicyActivationHost(host: SyncPolicyActivationHost | null): void {
  activationHost = host
  livePolicy = null
  closedModules = new Set(SYNC_MODULES)
}

/** 测试注入内存存储；传 null 恢复默认（真实 localStorage 或不可用）。 */
export function installSyncPolicyStorage(storage: StorageLike | null): void {
  injectedStorage = storage
  volatileCache = null
  volatileIntents.clear()
  scopeResolver = null
  installSyncPolicyActivationHost(null)
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

function writeCache(cache: SyncPolicyCache, scope: string): boolean {
  cache = { ...cache, scope }
  volatileCache = cache
  const store = policyStorage()
  if (!store) return false
  try {
    store.setItem(SYNC_POLICY_CACHE_KEY, JSON.stringify(cache))
    volatileCache = null
    return true
  } catch {
    return false
  }
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
  return livePolicy && contextCurrent(livePolicy.context) ? livePolicy.cache : readCache()
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
 * 免 Pinia 的同步准入入口。未激活、已撤销或身份过期时一律 local；
 * 不将磁盘上的 cloud 候选误当成当前权限。
 */
export function readCachedModuleMode(module: SyncModule): SyncMode {
  if (!livePolicy || !contextCurrent(livePolicy.context) || closedModules.has(module) || readLocalIntents(livePolicy.context).modules.includes(module)) return 'local'
  return moduleMode(livePolicy.cache.policy, module)
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
  state: () => ({
    effective: { policy: inactivePolicy(), lastFetchedAt: 0 },
    loadError: '', loaded: false, saving: false, saveError: '', identityRequired: false, intentReadError: false, persistenceWarning: false,
    pendingLocal: [] as SyncModule[],
    activation: { active: false, ready: false, error: '', pending: null as Record<SyncModule, SyncMode> | null },
    revision: 0, generation: ++nextPolicyGeneration,
    lastSyncReport: readReport(),
  }),
  getters: {
    activating: state => state.activation.active,
    activationReady: state => state.activation.ready,
    activationError: state => state.activation.error,
    pendingModules: state => state.activation.pending,
    systemDefault: state => state.effective.policy.system_default,
    allowOverride: state => state.effective.policy.allow_override,
    modules: state => state.effective.policy.modules,
    lastFetchedAt: state => state.effective.lastFetchedAt,
    policy: state => ({ ...state.effective.policy, modules: { ...state.effective.policy.modules } }),
    moduleModes: state => ({ ...state.effective.policy.modules }),
  },
  actions: {
    /** Revoke immediately. Owner-pending callers must not enqueue a policy activation. */
    suspend(): void {
      publications.delete(this); reads.delete(this); restorations.delete(this)
      livePolicy = null; closedModules = new Set(SYNC_MODULES)
      const generation = ++nextPolicyGeneration
      this.generation = generation
      if (this.generation !== generation) return
      this.activation = { active: false, ready: false, error: '', pending: null }
      if (this.generation !== generation) return
      this.saving = false
      if (this.generation !== generation) return
      this.pendingLocal = []
      if (this.generation !== generation) return
      this.saveError = ''
      if (this.generation !== generation) return
      this.identityRequired = false
      if (this.generation !== generation) return
      this.intentReadError = false
      if (this.generation !== generation) return
      this.persistenceWarning = false
      if (this.generation !== generation) return
      this.loaded = false
      if (this.generation !== generation) return
      this.loadError = ''
      notifyPolicyChange()
    },
    async $reset(): Promise<boolean> {
      this.suspend()
      try { return await this.restoreFromCache() } catch { return false }
    },
    async restoreFromCache(): Promise<boolean> {
      const host = activationHost, context = host?.captureContext(), generation = this.generation
      if (!host || !context) return false
      const existing = restorations.get(this)
      if (existing?.generation === generation && existing.host === host && contextCurrent(existing.context)) return existing.promise
      let resolve!: (value: boolean) => void, reject!: (error: unknown) => void
      const promise = new Promise<boolean>((done, fail) => { resolve = done; reject = fail })
      const ticket = { generation, context, host, promise }
      // Coalesce host bootstrap and a settings mount before any reactive callback
      // can request another restoration and steal the host's activation ticket.
      restorations.set(this, ticket)
      const restore = async () => {
        const current = () => this.generation === generation && activationHost === host && contextCurrent(context)
        const cached = readCache(), intents = readLocalIntents(context), writable = storageWritable()
        if (!current()) return false
        this.pendingLocal = intents.modules
        if (!current()) return false
        this.identityRequired = context.intentScope === '' && intents.keys.size > 0
        if (!current()) return false
        this.intentReadError = Boolean(intents.error)
        if (!current()) return false
        this.persistenceWarning = !writable || intents.volatile
        if (!current()) return false
        this.saveError = intents.error || (!writable ? 'Local policy storage is unavailable.' : intents.modules.length ? 'Local choice is waiting to be saved.' : '')
        if (!current()) return false
        const candidate = normalizeSyncPolicy(cached?.policy)
        if (!writable) candidate.modules = Object.fromEntries(SYNC_MODULES.map(module => [module, 'local'])) as Record<SyncModule, SyncMode>
        return this.applyPolicy(candidate, cached?.lastFetchedAt ?? 0, false)
      }
      void restore().then(value => {
        if (restorations.get(this) === ticket) restorations.delete(this)
        resolve(value)
      }, error => {
        if (restorations.get(this) === ticket) restorations.delete(this)
        reject(error)
      })
      return promise
    },
    async load(): Promise<void> {
      if (this.saving) return
      if (!this.activationReady) {
        try { if (!await this.restoreFromCache()) return } catch { return }
      }
      if (this.saving) return
      const context = activationHost?.captureContext()
      if (!context) return
      const generation = this.generation, revision = this.revision, ticket = {}
      reads.set(this, ticket)
      const current = () => reads.get(this) === ticket && this.generation === generation && this.revision === revision && contextCurrent(context)
      try {
        const remote = await syncPolicyTransport.fetchPolicy()
        if (!current()) return
        if (await this.applyPolicy(remote, Date.now()) && current()) this.loadError = ''
      } catch (error) {
        if (current() && !(error instanceof PolicyActivationError)) this.loadError = error instanceof Error ? error.message : String(error)
      } finally {
        if (current()) this.loaded = true
      }
    },
    /** Only the synchronous publisher changes the effective snapshot/cache. */
    async applyPolicy(policy: SyncPolicy, fetchedAt: number, preserveUnmanaged = true): Promise<boolean> {
      const host = activationHost, context = host?.captureContext()
      if (!host || !context) throw new PolicyActivationError('Policy activation is not ready.')
      const candidate = normalizeSyncPolicy(policy)
      if (preserveUnmanaged) {
        candidate.modules['ai-chat'] = this.modules['ai-chat']
        candidate.modules.tasks = this.modules.tasks
      }
      const intents = readLocalIntents(context)
      for (const module of intents.modules) candidate.modules[module] = 'local'
      const generation = this.generation, revision = this.revision, ticket = {}
      publications.set(this, ticket)
      const current = () => publications.get(this) === ticket && this.generation === generation && this.revision === revision && activationHost === host && contextCurrent(context)
      const previous = livePolicy && contextCurrent(livePolicy.context) ? livePolicy.cache.policy : null
      closedModules = new Set([...closedModules, ...SYNC_MODULES.filter(module => !previous || candidate.modules[module] === 'local' || previous.modules[module] !== candidate.modules[module])])
      let published = false, pending: Promise<void>
      try {
        pending = host.activate(candidate.modules, modes => {
          if (!current()) throw policyAbort()
          const latestIntents = readLocalIntents(context)
          if (latestIntents.modules.some(module => modes[module] === 'cloud')) throw policyAbort()
          const complete = freezePolicy({ ...candidate, modules: { ...modes } })
          const cache = { policy: complete, lastFetchedAt: fetchedAt, scope: context.scope }
          const persisted = writeCache(cache, context.scope)
          if (!current()) throw policyAbort()
          if (!persisted && SYNC_MODULES.some(module => complete.modules[module] === 'cloud' && previous?.modules[module] !== 'cloud')) {
            throw new Error('Cannot persist cloud policy safely.')
          }
          this.effective = { policy: complete, lastFetchedAt: fetchedAt }
          if (!current()) throw policyAbort()
          livePolicy = { cache, context, persisted }
          published = true
          return undefined
        })
      } catch (error) { pending = Promise.reject(error) }
      if (current()) {
        this.activation = { ...this.activation, active: true, pending: { ...candidate.modules } }
      }
      // Readers see denial now; old effective state/cache stay visible until the DB drains.
      notifyPolicyChange()
      try {
        await pending
        if (!current()) return false
        if (!published) throw new Error('Policy publisher did not run.')
        // Never open cloud admission before the kernel has confirmed the publisher.
        closedModules = new Set()
        this.activation = { active: false, ready: true, error: '', pending: null }
        if (!current()) return false
        notifyPolicyChange()
        return current()
      } catch (error) {
        if (!current()) return false
        const message = error instanceof Error ? error.message : String(error)
        closedModules = new Set(SYNC_MODULES)
        this.activation = { ...this.activation, active: false, ready: false, error: message }
        if (!current()) return false
        notifyPolicyChange()
        // Retain denial after DB/publisher failures; only a new explicit activation may release it.
        throw new PolicyActivationError(message, error)
      }
    },
    async setModule(module: SyncModule, mode: SyncMode): Promise<boolean> {
      if (!isPortalSyncModule(module) || !this.allowOverride || !this.activationReady || this.saving || this.activating) return false
      if (this.modules[module] === mode && !closedModules.has(module) && !this.pendingLocal.includes(module)) return true
      const context = activationHost?.captureContext()
      if (!context) return false
      const capturedIntents = readLocalIntents(context)
      let ownedIntentKey: string | undefined
      const previous = this.policy, fetchedAt = this.lastFetchedAt, generation = this.generation, revision = this.revision + 1
      const requested = { ...previous.modules, [module]: mode }
      const current = () => this.generation === generation && this.revision === revision && contextCurrent(context)
      this.revision = revision
      if (!current()) return false
      this.saving = true
      if (!current()) return false
      const local = mode === 'local' ? this.applyPolicy({ ...previous, modules: requested }, fetchedAt, false) : Promise.resolve(true)
      if (!current()) return false
      if (mode === 'local') {
        const intent = addLocalIntent(module, context)
        // Persist a new immutable token before retiring captured older tokens.
        // Retries stay bounded while an older cloud response cannot erase them.
        if (intent.persisted && !capturedIntents.error) clearLocalIntents(capturedIntents.keys, module, context)
        capturedIntents.keys.set(intent.key, intent.value)
        ownedIntentKey = intent.key
        if (!current()) return false
        this.pendingLocal = readLocalIntents(context).modules
        if (!current()) return false
        this.persistenceWarning = !intent.persisted
        if (!current()) return false
        this.saveError = intent.persisted ? '' : 'Local choice could not be persisted. Retry before closing the app.'
      } else this.saveError = ''
      if (!current()) return false
      this.activation = { ...this.activation, pending: { ...requested } }
      const response = (async () => {
        if (!current()) throw policyAbort()
        return syncPolicyTransport.putModules(requested)
      })().then(value => ({ ok: true as const, value }), error => ({ ok: false as const, error }))
      try {
        if (!await local || !current()) return false
        const result = await response
        if (!current()) return false
        if (!result.ok) {
          this.saveError = result.error instanceof Error ? result.error.message : String(result.error)
          // Keep local effective and pending: a GET or restart must not silently
          // undo the user's privacy choice after an offline/ambiguous PUT.
          throw result.error
        }
        const remote = normalizeSyncPolicy(result.value)
        if (mode === 'cloud' && remote.modules[module] === 'cloud' && !capturedIntents.error && !clearLocalIntents(capturedIntents.keys, module, context, ownedIntentKey)) {
          this.persistenceWarning = true
          if (!current()) return false
          this.saveError = 'Local choice could not be persisted. Retry before closing the app.'
          return false
        }
        if (!current()) return false
        if (!await this.applyPolicy(result.value, fetchedAt || Date.now()) || !current()) return false
        if (remote.modules[module] !== mode) {
          this.saveError = 'The server did not confirm the requested policy.'
          return false
        }
        if (!livePolicy?.persisted || capturedIntents.error || !clearLocalIntents(capturedIntents.keys, module, context, ownedIntentKey)) {
          this.persistenceWarning = true
          if (!current()) return false
          this.saveError = 'Local choice could not be persisted. Retry before closing the app.'
          return false
        }
        if (!current()) return false
        this.pendingLocal = readLocalIntents(context).modules
        if (!current()) return false
        this.persistenceWarning = false
        if (!current()) return false
        this.saveError = this.pendingLocal.length ? 'Local choice is waiting to be saved.' : ''
        return this.modules[module] === mode && !this.pendingLocal.includes(module)
      } finally {
        if (current()) {
          this.saving = false
          if (current() && !this.activating) this.activation = { ...this.activation, pending: null }
        }
      }
    },
    async refreshLocalIntents(): Promise<boolean> {
      const context = activationHost?.captureContext(), generation = this.generation
      if (!context) return false
      const intents = readLocalIntents(context)
      // prepare the complete snapshot before any reactive status/event callback.
      const activation = this.applyPolicy(this.policy, this.lastFetchedAt, false)
      if (this.generation !== generation || !contextCurrent(context)) return false
      this.pendingLocal = intents.modules
      if (this.generation !== generation || !contextCurrent(context)) return false
      this.identityRequired = context.intentScope === '' && intents.keys.size > 0
      if (this.generation !== generation || !contextCurrent(context)) return false
      this.intentReadError = Boolean(intents.error)
      if (this.generation !== generation || !contextCurrent(context)) return false
      this.persistenceWarning = intents.volatile
      if (this.generation !== generation || !contextCurrent(context)) return false
      this.saveError = intents.error || (intents.modules.length ? 'Local choice is waiting to be saved.' : '')
      return activation
    },
    async retrySave(): Promise<boolean> {
      const module = this.pendingLocal.find(isPortalSyncModule)
      return module ? this.setModule(module, 'local') : false
    },
    refreshReport(): void { this.lastSyncReport = readReport() },
  },
})
