import { watch } from 'vue'
import { ApiError } from '../../api/http'
import { isAbortError } from '../../api/abort'
import { userSettingsApi, type RemoteSetting } from '../../api/user-settings'
import { lobsterReady } from '../lobster-init'
import { localDB } from '../local-db'
import { useAuthStore } from '../../stores/auth'
import { applyRemoteSetting } from './apply'
import { enqueueConfigPush, listQueuedConfigPushes, markConfigPushDone, markConfigPushFailed } from './outbox'
import { configKey, planConfigSync } from './planner'
import { preserveCorruptRaw } from '../../utils/corrupt-preserve'
import { getLocalSetting, listLocalSettings, markSettingClean, writeLocalIfNewer, writeLocalSetting, type LocalSetting } from './settings-store'
import { readCachedModuleMode, recordSyncReportEntry } from '../../stores/syncPolicyStore'
import { captureConfigOperation, captureConfigOperationReady, CONFIG_MODULE, type ConfigOperation } from './operation'
import type { SyncModule } from '../../api/syncPolicyContract'
import { PocketResourceUnavailableError } from '../../api/pocket-resource'
import { userConfigSyncStatus } from './status'

function recordCapability(error?: unknown): void {
  userConfigSyncStatus.activationVersion = useAuthStore().activationVersion
  userConfigSyncStatus.capabilityUnavailable = error instanceof PocketResourceUnavailableError
}

export interface ConfigSyncReport {
  pulled: number
  pushed: number
  queued: number
  online: boolean
  error?: string
}

function isCancelled(error: unknown): boolean {
  return isAbortError(error) || (error instanceof ApiError && error.body?.error === 'module_policy_local')
}
function assertRecord(record: RemoteSetting, operation: ConfigOperation, namespace: string, id: string): void {
  operation.assertCurrent()
  if (record.namespace !== namespace || record.id !== id ||
      (record.workspaceId && record.workspaceId !== operation.workspaceId)) {
    throw new Error('Remote setting identity does not match the requested setting.')
  }
}
async function applyRecord(record: RemoteSetting, operation: ConfigOperation): Promise<void> {
  operation.assertCurrent()
  const won = await writeLocalIfNewer({ namespace: record.namespace, id: record.id,
    payload: record.payload, updatedAt: record.updatedAt }, operation.owner, operation.db)
  operation.assertCurrent()
  if (won) {
    const failed = applyRemoteSetting(record, operation)
    if (failed.length > 0) console.warn(`[config-sync] 远端设置 ${record.namespace}/${record.id} 未能完全落地：${failed.join('、')}`)
  }
}

async function pushOne(namespace: string, id: string, operation: ConfigOperation): Promise<'ok' | 'queued' | 'conflict'> {
  const local = await getLocalSetting(namespace, id, operation.owner, operation.db)
  operation.assertCurrent()
  if (!local) return 'ok'
  if (!operation.canSync(namespace)) {
    await enqueueConfigPush({ namespace, id, payload: local.payload, secret: local.secretEncrypted,
      updatedAt: local.updatedAt }, operation.owner, operation.db)
    return 'queued'
  }
  const cloud = operation.forNamespace(namespace)
  try {
    const result = await userSettingsApi.put(namespace, id, { payload: local.payload,
      updatedAt: local.updatedAt, secret: local.secretEncrypted || undefined }, cloud.requestOptions())
    cloud.assertCurrent()
    if (result.conflict && result.record) {
      assertRecord(result.record, cloud, namespace, id)
      await applyRecord(result.record, cloud)
      return 'conflict'
    }
    await markSettingClean(namespace, id, local, cloud.owner, cloud.db)
    return 'ok'
  } catch (error) {
    cloud.assertCurrent()
    if (isCancelled(error)) throw error
    if (error instanceof PocketResourceUnavailableError) recordCapability(error)
    await enqueueConfigPush({ namespace, id, payload: local.payload, secret: local.secretEncrypted,
      updatedAt: local.updatedAt }, cloud.owner, cloud.db)
    return 'queued'
  }
}

export async function drainConfigOutbox(operation?: ConfigOperation): Promise<number> {
  // ⚠ 归属未确认前捕获的操作会没有归属 ⇒ 它的行永不上传。
  //   ⇒ 同步路径必须等归属就绪（见 captureConfigOperationReady）。
  operation ??= await captureConfigOperationReady()
  const queued = await listQueuedConfigPushes(operation.owner, operation.db)
  operation.assertCurrent()
  let pushed = 0
  const held = new Map<SyncModule, number>(), flowed = new Map<SyncModule, number>()
  const bump = (map: Map<SyncModule, number>, module: SyncModule) => map.set(module, (map.get(module) ?? 0) + 1)
  for (const row of queued) {
    operation.assertCurrent()
    const ownerModule = CONFIG_MODULE[row.namespace]
    if (!operation.canSync(row.namespace)) {
      if (ownerModule) bump(held, ownerModule)
      continue
    }
    const cloud = operation.forNamespace(row.namespace)
    try {
      if (row.namespace === 'email_account') {
        const { emailApi, isStaleWriteError } = await import('../../api/email')
        cloud.assertCurrent()
        try {
          await emailApi.updateAccount(row.entityId, row.payload as never, row.updatedAt, cloud.requestOptions())
        } catch (error) {
          cloud.assertCurrent()
          if (isCancelled(error) || !isStaleWriteError(error)) throw error
          console.warn('[config-sync] email_account push rejected as stale; server copy wins', row.entityId)
        }
      } else if (row.namespace === 'scheduled_task') {
        const { scheduledTasksApi } = await import('../../features/scheduled-tasks/api')
        cloud.assertCurrent()
        try {
          await scheduledTasksApi.update(row.entityId, row.payload as never, cloud.requestOptions())
        } catch (error) {
          cloud.assertCurrent()
          // Only a confirmed missing resource permits creation. Cancellation,
          // validation and transport errors must not duplicate an old entity.
          if (!(error instanceof ApiError) || error.status !== 404) throw error
          await scheduledTasksApi.create(row.payload as never, cloud.requestOptions())
        }
      } else if (row.namespace === 'chat_agent') {
        const { chatAgentApi } = await import('../../api/chatAgent')
        cloud.assertCurrent()
        try {
          await chatAgentApi.update(row.entityId, row.payload as never, cloud.requestOptions())
        } catch (error) {
          cloud.assertCurrent()
          if (!(error instanceof ApiError) || error.status !== 404) throw error
          await chatAgentApi.create({ ...row.payload as object, id: row.entityId } as never, cloud.requestOptions())
        }
      } else {
        const result = await userSettingsApi.put(row.namespace, row.entityId, { payload: row.payload,
          updatedAt: row.updatedAt, secret: row.secret || undefined }, cloud.requestOptions())
        cloud.assertCurrent()
        if (result.conflict && result.record) {
          assertRecord(result.record, cloud, row.namespace, row.entityId)
          await applyRecord(result.record, cloud)
        }
        const expected: LocalSetting = { namespace: row.namespace, id: row.entityId, payload: row.payload,
          updatedAt: row.updatedAt, secretEncrypted: row.secret || '', dirty: 1, owner: cloud.owner }
        await markSettingClean(row.namespace, row.entityId, expected, cloud.owner, cloud.db)
      }
      cloud.assertCurrent()
      await markConfigPushDone(row.id, cloud.db)
      if (ownerModule) bump(flowed, ownerModule)
      pushed++
    } catch (error) {
      cloud.assertCurrent()
      if (isCancelled(error)) throw error
      if (error instanceof PocketResourceUnavailableError) recordCapability(error)
      await markConfigPushFailed(row.id, error instanceof Error ? error.message : String(error), cloud.db)
    }
  }
  operation.assertCurrent()
  for (const [module, count] of held) {
    if (readCachedModuleMode(module) === 'local') recordSyncReportEntry({ module, status: 'skipped', count })
  }
  for (const [module, count] of flowed) {
    const namespace = Object.keys(CONFIG_MODULE).find(key => CONFIG_MODULE[key] === module)!
    operation.forNamespace(namespace).assertCurrent()
    recordSyncReportEntry({ module, status: 'synced', count })
  }
  return pushed
}

export async function syncUserSettings(operation?: ConfigOperation): Promise<ConfigSyncReport> {
  operation ??= await captureConfigOperationReady()
  try {
    const remote = await userSettingsApi.list(operation.requestOptions())
    operation.assertCurrent()
    if (!localDB.isReady()) return { pulled: 0, pushed: 0, queued: 0, online: true }
    const local = await listLocalSettings(operation.owner, operation.db)
    operation.assertCurrent()
    const plan = planConfigSync(local, remote)
    const remoteByKey = new Map(remote.map(record => [configKey(record.namespace, record.id), record]))
    let pulled = 0, pushed = 0, queued = 0
    for (const key of plan.pullKeys) {
      const record = remoteByKey.get(key)
      if (!record || !operation.canSync(record.namespace)) continue
      const cloud = operation.forNamespace(record.namespace)
      assertRecord(record, cloud, record.namespace, record.id)
      const won = await writeLocalIfNewer({ namespace: record.namespace, id: record.id,
        payload: record.payload, updatedAt: record.updatedAt }, cloud.owner, cloud.db)
      cloud.assertCurrent()
      if (won) {
        const failed = applyRemoteSetting(record, cloud)
        if (failed.length > 0) console.warn(`[config-sync] 远端设置 ${record.namespace}/${record.id} 未能完全落地：${failed.join('、')}`)
        pulled++
      }
    }
    for (const key of plan.pushKeys) {
      const record = local.find(item => configKey(item.namespace, item.id) === key)!
      if (!operation.canSync(record.namespace)) continue
      const status = await pushOne(record.namespace, record.id, operation)
      if (status === 'ok') pushed++
      if (status === 'queued') queued++
    }
    queued += await drainConfigOutbox(operation)
    operation.assertCurrent()
    recordCapability()
    return { pulled, pushed, queued, online: true }
  } catch (error) {
    operation.assertCurrent()
    if (isCancelled(error)) throw error
    if (error instanceof PocketResourceUnavailableError) recordCapability(error)
    return { pulled: 0, pushed: 0, queued: 0, online: false,
      error: error instanceof Error ? error.message : String(error) }
  }
}

export async function saveSettingLocalFirst(namespace: string, id: string, payload: unknown, secret?: string): Promise<void> {
  if (!localDB.isReady()) return
  const operation = captureConfigOperation()
  const row = await writeLocalSetting({ namespace, id, payload, secretEncrypted: secret, dirty: 1 }, operation.owner, operation.db)
  operation.assertCurrent()
  applyRemoteSetting({ userId: '', workspaceId: operation.workspaceId, namespace, id, payload,
    updatedAt: row.updatedAt } as RemoteSetting, operation)
  if (typeof navigator !== 'undefined' && !navigator.onLine) {
    await enqueueConfigPush({ namespace, id, payload, secret, updatedAt: row.updatedAt }, operation.owner, operation.db)
    return
  }
  await pushOne(namespace, id, operation)
}

let watcherStarted = false
let inflight: Promise<ConfigSyncReport> | null = null

async function importLegacyPrefsIfMissing(operation?: ConfigOperation): Promise<void> {
  operation ??= await captureConfigOperationReady()
  const { currentAppPrefs } = await import('./prefs')
  operation.assertCurrent()
  const { readSelectedInstance } = await import('../../config/selected-instance')
  operation.assertCurrent()
  if (!(await getLocalSetting('app_prefs', 'default', operation.owner, operation.db))) {
    await writeLocalSetting({ namespace: 'app_prefs', id: 'default', payload: currentAppPrefs(), dirty: 1 }, operation.owner, operation.db)
  }
  if (!(await getLocalSetting('connection', 'default', operation.owner, operation.db))) {
    const inst = readSelectedInstance()
    if (inst) await writeLocalSetting({ namespace: 'connection', id: 'default', payload: inst, dirty: 1 }, operation.owner, operation.db)
  }
  try {
    operation.assertCurrent()
    const workspace = operation.workspaceId
    const user = operation.user
    const scope = encodeURIComponent(workspace || user || 'local')
    const raw = localStorage.getItem(`pocket:ai-chat:settings:v2:${scope}`)
    if (raw && !(await getLocalSetting('chat_settings', 'default', operation.owner, operation.db))) {
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
      await writeLocalSetting({ namespace: 'chat_settings', id: 'default', payload, dirty: 1 }, operation.owner, operation.db)
    }
  } catch (error) {
    operation.assertCurrent()
    if (isCancelled(error)) throw error
    console.warn('[config-sync] 旧偏好迁移失败：', error)
  }
}

export function startUserConfigSync(): void {
  if (watcherStarted) return
  watcherStarted = true
  const auth = useAuthStore()
  const kick = () => {
    if (!lobsterReady.value || !auth.isAuthenticated || inflight) return
    const version = auth.activationVersion
    let operation: ConfigOperation
    try { operation = captureConfigOperation() } catch { return }
    inflight = importLegacyPrefsIfMissing(operation)
      .then(() => {
        if (!auth.isAuthenticated || auth.activationVersion !== version) return { pulled: 0, pushed: 0, queued: 0, online: false }
        return syncUserSettings(operation)
      })
      .catch((error: unknown) => {
        if (!(error instanceof Error) || error.name !== 'AbortError') console.warn('[config-sync] 启动同步失败：', error)
        return { pulled: 0, pushed: 0, queued: 0, online: false, error: error instanceof Error ? error.message : String(error) }
      })
      .finally(() => {
        inflight = null
        if (auth.activationVersion !== version) kick()
      })
  }
  watch([lobsterReady, () => auth.activationStatus, () => auth.activationVersion], kick,
    { immediate: true, flush: 'sync' })
  if (typeof window !== 'undefined') {
    window.addEventListener('online', () => {
      if (!auth.isAuthenticated || !lobsterReady.value) return
      void syncUserSettings().catch(error => {
        if (!isCancelled(error)) console.warn('[config-sync] 联网同步失败：', error)
      })
    })
  }
}
