import { watch } from 'vue'
import { userSettingsApi, type RemoteSetting } from '../../api/user-settings'
import { lobsterReady } from '../lobster-init'
import { localDB } from '../local-db'
import { useAuthStore } from '../../stores/auth'
import { applyRemoteSetting } from './apply'
import { enqueueConfigPush, listQueuedConfigPushes, markConfigPushDone, markConfigPushFailed } from './outbox'
import { configKey, planConfigSync } from './planner'
import { getLocalSetting, listLocalSettings, markSettingClean, writeLocalIfNewer, writeLocalSetting } from './settings-store'

export interface ConfigSyncReport {
  pulled: number
  pushed: number
  queued: number
  online: boolean
  error?: string
}

async function pushOne(namespace: string, id: string): Promise<'ok' | 'queued' | 'conflict'> {
  const local = await getLocalSetting(namespace, id)
  if (!local) return 'ok'
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
  for (const row of queued) {
    try {
      if (row.namespace === 'email_account') {
        const { emailApi } = await import('../../api/email')
        const p = row.payload as { displayName?: string; syncIntervalMin?: number; enabled?: boolean }
        await emailApi.updateAccount(row.entityId, p)
        await markConfigPushDone(row.id)
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
      pushed++
    } catch (err) {
      await markConfigPushFailed(row.id, err instanceof Error ? err.message : String(err))
    }
  }
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
      const won = await writeLocalIfNewer({
        namespace: rec.namespace, id: rec.id, payload: rec.payload, updatedAt: rec.updatedAt,
      })
      if (won) {
        await applyRemoteSetting(rec)
        pulled++
      }
    }
    for (const key of plan.pushKeys) {
      const [namespace, id] = key.split(':')
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
      await writeLocalSetting({ namespace: 'chat_settings', id: 'default', payload: JSON.parse(raw), dirty: 1 })
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
