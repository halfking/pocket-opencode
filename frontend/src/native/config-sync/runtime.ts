import { watch } from 'vue'
import { userSettingsApi, type RemoteSetting } from '../../api/user-settings'
import { lobsterReady } from '../lobster-init'
import { localDB } from '../local-db'
import { useAuthStore } from '../../stores/auth'
import { applyRemoteSetting } from './apply'
import { enqueueConfigPush, listQueuedConfigPushes, markConfigPushDone, markConfigPushFailed } from './outbox'
import { configKey, planConfigSync } from './planner'
import { preserveCorruptRaw } from '../../utils/corrupt-preserve'
import { getLocalSetting, listLocalSettings, markSettingClean, writeLocalIfNewer, writeLocalSetting } from './settings-store'
// 存储与同步策略（storage-sync）：按 namespace 过滤模块owned配置的上行。
import {
  readCachedModuleMode,
  recordSyncReportEntry,
} from '../../stores/syncPolicyStore'
import type { SyncModule } from '../../api/syncPolicyContract.ts'

export interface ConfigSyncReport {
  pulled: number
  pushed: number
  queued: number
  online: boolean
  error?: string
}

/**
 * storage-sync：settings namespace → 业务模块的归属映射。
 * 只映射**整块属于某个策略模块**的 namespace；app_prefs / connection 是
 * 应用级连接配置（切错了会把设备变成孤岛），不参与按模块过滤。
 */
const NAMESPACE_MODULE: Record<string, SyncModule> = {
  email_account: 'email',
  chat_settings: 'ai-chat',
  chat_agent: 'ai-chat',
  scheduled_task: 'tasks',
}

/** 该 namespace 是否被策略拦下（对应模块已切「仅本地」）。 */
function namespaceHeldLocal(namespace: string): boolean {
  const mod = NAMESPACE_MODULE[namespace]
  if (!mod) return false
  return readCachedModuleMode(mod) === 'local'
}

/**
 * storage-sync：把本轮各模块的拦截/放行结果记入 lastSyncReport。
 * held>0 ⇒ skipped；flowed>0 ⇒ synced（count=条数）。0 条不动旧报告
 * （配置同步很多轮都是空转，空转不该刷新「上次同步」时间戳）。
 */
function reportModuleConfigFlow(held: Map<SyncModule, number>, flowed: Map<SyncModule, number>): void {
  for (const [module, count] of held) {
    if (count > 0) recordSyncReportEntry({ module, status: 'skipped', count })
  }
  for (const [module, count] of flowed) {
    if (count > 0) recordSyncReportEntry({ module, status: 'synced', count })
  }
}

async function pushOne(namespace: string, id: string): Promise<'ok' | 'queued' | 'conflict'> {
  const local = await getLocalSetting(namespace, id)
  if (!local) return 'ok'
  // storage-sync 挂点：在线直存路径同样受策略约束 —— 模块切「仅本地」后，
  // 配置改动静默入本地 outbox（与离线路径同款），不直发云端。
  if (namespaceHeldLocal(namespace)) {
    await enqueueConfigPush({
      namespace, id, payload: local.payload,
      secret: local.secretEncrypted, updatedAt: local.updatedAt,
    })
    return 'queued'
  }
  try {
    const result = await userSettingsApi.put(namespace, id, {
      payload: local.payload,
      updatedAt: local.updatedAt,
      secret: local.secretEncrypted || undefined,
    })
    if (result.conflict && result.record) {
      await writeLocalIfNewer({
        namespace: result.record.namespace,
        id: result.record.id,
        payload: result.record.payload,
        updatedAt: result.record.updatedAt,
      })
      await applyRemoteSetting(result.record)
      return 'conflict'
    }
    await markSettingClean(namespace, id)
    return 'ok'
  } catch (err) {
    await enqueueConfigPush({
      namespace, id, payload: local.payload,
      secret: local.secretEncrypted, updatedAt: local.updatedAt,
    })
    return 'queued'
  }
}

export async function drainConfigOutbox(): Promise<number> {
  const queued = await listQueuedConfigPushes()
  let pushed = 0
  // storage-sync：按模块统计本轮被拦下 / 正常放行的配置行数。
  const held = new Map<SyncModule, number>()
  const flowed = new Map<SyncModule, number>()
  const bump = (m: Map<SyncModule, number>, k: SyncModule) => m.set(k, (m.get(k) ?? 0) + 1)
  for (const row of queued) {
    // storage-sync 挂点：模块切「仅本地」后其配置行留在本地 outbox（不发送、
    // 不计失败），切回「云端」下一轮 drain 自会重放 —— 与会话 outbox 的
    // skipActions 同语义。
    if (namespaceHeldLocal(row.namespace)) {
      const mod = NAMESPACE_MODULE[row.namespace]!
      bump(held, mod)
      continue
    }
    const ownerModule = NAMESPACE_MODULE[row.namespace] ?? null
    try {
      if (row.namespace === 'email_account') {
        const { emailApi, isStaleWriteError } = await import('../../api/email')
        // payload 是 enqueueConfigPush 存下来的 patch，字段集见
        // account-sync.ts 的 pushAccountToServer（displayName /
        // syncIntervalMin / enabled / imapHost / imapPort / authType）。
        // 下面这个类型只声明了其中三个字段，属于**收窄**：其余字段运行时照样
        // 带着走，只是类型上看不见。若日后要按字段分支，先把它对齐。
        const p = row.payload as { displayName?: string; syncIntervalMin?: number; enabled?: boolean }
        // 基准取行上的 updatedAt：它就是入队那一刻客户端据以编辑的那一版
        // （enqueueConfigPush 收到的 _a.updatedAt 原样落在这里）。
        // 传 0 会让服务端按 store.go 的 baseUpdatedAt<=0 分支退化成无条件
        // 覆盖，守卫等于不存在 —— 所以这里**不能**用 `?? 0` 兜底。
        try {
          await emailApi.updateAccount(row.entityId, p, row.updatedAt)
        } catch (err) {
          // 409 = 服务端那份更新，按需求「新的胜出」就该让服务端赢，不重试。
          // 必须在分支内消化：外层 catch 只会 markConfigPushFailed，而它只加
          // attempts、**不改 state**，这一行仍是 'queued'，于是每轮 online 事件
          // 和每轮 syncUserSettings 都会重放同一行，永远推不进去。
          // 本地值随后由下行 syncUserSettings 的 writeLocalIfNewer 覆盖。
          if (!isStaleWriteError(err)) throw err
          console.warn('[config-sync] email_account push rejected as stale; server copy wins', row.entityId)
        }
        await markConfigPushDone(row.id)
        if (ownerModule) bump(flowed, ownerModule)
        pushed++
        continue
      }
      if (row.namespace === 'scheduled_task') {
        const { scheduledTasksApi } = await import('../../features/scheduled-tasks/api')
        const p = row.payload as Record<string, unknown>
        try {
          await scheduledTasksApi.update(row.entityId, p as never)
        } catch {
          await scheduledTasksApi.create(p as never)
        }
        await markConfigPushDone(row.id)
        if (ownerModule) bump(flowed, ownerModule)
        pushed++
        continue
      }
      if (row.namespace === 'chat_agent') {
        const { chatAgentApi } = await import('../../api/chatAgent')
        const p = row.payload as Record<string, unknown>
        try {
          await chatAgentApi.update(row.entityId, p as never)
        } catch {
          await chatAgentApi.create({ id: row.entityId, ...p } as never)
        }
        await markConfigPushDone(row.id)
        if (ownerModule) bump(flowed, ownerModule)
        pushed++
        continue
      }
      const result = await userSettingsApi.put(row.namespace, row.entityId, {
        payload: row.payload, updatedAt: row.updatedAt, secret: row.secret || undefined,
      })
      if (result.conflict && result.record) {
        await writeLocalIfNewer({
          namespace: result.record.namespace,
          id: result.record.id,
          payload: result.record.payload,
          updatedAt: result.record.updatedAt,
        })
        await applyRemoteSetting(result.record)
      }
      await markConfigPushDone(row.id)
      await markSettingClean(row.namespace, row.entityId)
      if (ownerModule) bump(flowed, ownerModule)
      pushed++
    } catch (err) {
      await markConfigPushFailed(row.id, err instanceof Error ? err.message : String(err))
    }
  }
  reportModuleConfigFlow(held, flowed)
  return pushed
}

export async function syncUserSettings(): Promise<ConfigSyncReport> {
  try {
    const remote = await userSettingsApi.list()
    if (!localDB.isReady()) {
      return { pulled: 0, pushed: 0, queued: 0, online: true }
    }
    const local = await listLocalSettings()
    const plan = planConfigSync(
      local.map((s) => ({ namespace: s.namespace, id: s.id, updatedAt: s.updatedAt })),
      remote.map((s) => ({ namespace: s.namespace, id: s.id, updatedAt: s.updatedAt })),
    )
    const remoteByKey = new Map(remote.map((s) => [configKey(s.namespace, s.id), s]))
    let pulled = 0
    let pushed = 0
    let queued = 0
    for (const key of plan.pullKeys) {
      const rec = remoteByKey.get(key)
      if (!rec) continue
      // storage-sync 挂点：模块切「仅本地」后**双向**都跳过——pull 会用远端
      // 副本覆盖本地（writeLocalIfNewer），对 local 模块等于悄悄改写本机数据。
      if (namespaceHeldLocal(rec.namespace)) continue
      const won = await writeLocalIfNewer({
        namespace: rec.namespace, id: rec.id, payload: rec.payload, updatedAt: rec.updatedAt,
      })
      if (won) {
        const failed = await applyRemoteSetting(rec)
        // ⚠⚠§869/D37/D38：writeLocalIfNewer 已把这条设置在 **DB** 里标记为
        // 「已是最新」（dirty = 0），而 localStorage 投影可能刚写失败
        // ⇒ 两者会永久分叉且**不会重试**。这里至少留一条可检索的日志。
        if (failed.length > 0) {
          console.warn(`[config-sync] 远端设置 ${rec.namespace}/${rec.id} 未能完全落地：${failed.join('、')}`)
        }
        pulled++
      }
    }
    for (const key of plan.pushKeys) {
      const [namespace, id] = key.split(':')
      // storage-sync 挂点：同上，local 模块的配置不上行（留给本机）。
      if (namespaceHeldLocal(namespace)) continue
      const status = await pushOne(namespace, id)
      if (status === 'ok') pushed++
      if (status === 'queued') queued++
    }
    queued += await drainConfigOutbox()
    return { pulled, pushed, queued, online: true }
  } catch (err) {
    return {
      pulled: 0, pushed: 0, queued: 0, online: false,
      error: err instanceof Error ? err.message : String(err),
    }
  }
}

export async function saveSettingLocalFirst(
  namespace: string,
  id: string,
  payload: unknown,
  secret?: string,
): Promise<void> {
  if (!localDB.isReady()) return
  const row = await writeLocalSetting({
    namespace, id, payload, secretEncrypted: secret, dirty: 1,
  })
  await applyRemoteSetting({
    userId: '', workspaceId: '', namespace, id, payload, updatedAt: row.updatedAt,
  } as RemoteSetting)
  if (typeof navigator !== 'undefined' && !navigator.onLine) {
    await enqueueConfigPush({
      namespace, id, payload, secret, updatedAt: row.updatedAt,
    })
    return
  }
  const status = await pushOne(namespace, id)
  if (status === 'queued') return
}

let watcherStarted = false
let inflight: Promise<ConfigSyncReport> | null = null

async function importLegacyPrefsIfMissing(): Promise<void> {
  const { currentAppPrefs } = await import('./prefs')
  const { readSelectedInstance } = await import('../../config/selected-instance')
  if (!(await getLocalSetting('app_prefs', 'default'))) {
    await writeLocalSetting({ namespace: 'app_prefs', id: 'default', payload: currentAppPrefs(), dirty: 1 })
  }
  if (!(await getLocalSetting('connection', 'default'))) {
    const inst = readSelectedInstance()
    if (inst) await writeLocalSetting({ namespace: 'connection', id: 'default', payload: inst, dirty: 1 })
  }
  try {
    const workspace = localStorage.getItem('pocket_workspace_id') || ''
    const user = localStorage.getItem('pocket_user') || ''
    const scope = encodeURIComponent(workspace || user || 'local')
    const raw = localStorage.getItem(`pocket:ai-chat:settings:v2:${scope}`)
    if (raw && !(await getLocalSetting('chat_settings', 'default'))) {
      let payload: unknown
      try {
        payload = JSON.parse(raw)
      } catch {
        // ⚠⚠§859/D25：这是同一个 key（pocket:ai-chat:settings:v2:<scope>）的
        // **第三个**消费者，而前两个都被硬化过了：
        //   ① 读侧 loadSettings（§858/D21）—— 留证 + 置位 + 界面如实告知；
        //   ② 写侧 applyChatSettings（§859/D24）—— 留证 + 以 {} 为基底继续应用；
        //   ③ **这里（一次性旧偏好迁移）此前只有一个 `catch { // ignore }`**。
        // ⇒ 后果：本地 blob 损坏时这次迁移**静默跳过**，
        //   用户的 AI 聊天设置从此**只留在这一台设备上、永远同步不到别的设备**，
        //   而界面上零提示（§858 那条提示覆盖不到这条路径）。
        // ⇒ 与前两处同构：先留证，再跳过；并留一条 warn 以便排查。
        //   ⚠ 迁移路径没有界面出口，warn 是这里唯一说得出口的地方。
        preserveCorruptRaw('pocket:ai-chat:corrupt', 'settings', raw)
        console.warn('[config-sync] 旧 AI 聊天设置损坏，已留证并跳过迁移')
        return
      }
      await writeLocalSetting({ namespace: 'chat_settings', id: 'default', payload, dirty: 1 })
    }
  } catch {
    // ignore
  }
}

export function startUserConfigSync(): void {
  if (watcherStarted) return
  watcherStarted = true
  const auth = useAuthStore()
  watch(
    [lobsterReady, () => auth.isAuthenticated],
    ([ready, authed]) => {
      if (!ready || !authed) return
      if (inflight) return
      inflight = importLegacyPrefsIfMissing()
        .then(() => syncUserSettings())
        .finally(() => { inflight = null })
    },
    { immediate: true },
  )
  if (typeof window !== 'undefined') {
    window.addEventListener('online', () => {
      if (!auth.isAuthenticated || !lobsterReady.value) return
      void drainConfigOutbox()
      void syncUserSettings()
    })
  }
}
