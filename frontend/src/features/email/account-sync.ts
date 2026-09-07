/**
 * account-sync.ts — 邮箱账户配置的 LWW（last-write-wins）双向同步。
 *
 * 协议：
 *   - 服务端 SSOT：email_accounts.updated_at 由任何写路径刷新；
 *   - 客户端镜像：local_email_accounts.updated_at 对齐服务端时间；
 *   - 启动 / 显式 sync 时：
 *       服务端 updated_at > 本地 → 覆盖本地（下行）
 *       本地 updated_at > 服务端且对得上同一账户 → 只上行元数据
 *   - 本地独有账户不上行：镜像库不持有凭证，无法在服务端新建可用账户。
 *
 * 服务端不可达时不抛错，继续用本地列表。
 */
import { watch } from 'vue'
import { emailApi, type EmailAccount as ServerAccount } from '../../api/email'
import { lobsterReady } from '../../native/lobster-init'
import { useAuthStore } from '../../stores/auth'
import { localDB } from '../../native/local-db'
import { deleteAccount, listAccounts, writeAccountIfNewer } from './emails-store'
import { isLocalTestAddress } from './providers'

export interface SyncReport {
  fetched: number
  applied: number
  skipped: number
  pushed: number
  online: boolean
  error?: string
}

export interface AccountStamp {
  id: string
  emailAddress: string
  updatedAt: number
}

export function planAccountSync(local: AccountStamp[], remote: AccountStamp[]): {
  pullIds: string[]
  pushIds: string[]
} {
  const localById = new Map(local.map((a) => [a.id, a]))
  const remoteById = new Map(remote.map((a) => [a.id, a]))
  const remoteByEmail = new Map(remote.map((a) => [a.emailAddress.toLowerCase(), a]))
  const pullIds: string[] = []
  const pushIds: string[] = []
  for (const r of remote) {
    const l = localById.get(r.id)
    if (!l || r.updatedAt > l.updatedAt) pullIds.push(r.id)
  }
  for (const l of local) {
    const r = remoteById.get(l.id) ?? remoteByEmail.get(l.emailAddress.toLowerCase())
    if (r && l.updatedAt > r.updatedAt) pushIds.push(l.id)
  }
  return { pullIds, pushIds }
}

const emptyReport = (): SyncReport => ({
  fetched: 0, applied: 0, skipped: 0, pushed: 0, online: false,
})

/**
 * 拉服务端账户并按 LWW 写入本地；本地更新的已有账户再上行元数据。
 */
export async function syncAccountsFromServer(): Promise<SyncReport> {
  return syncAccountsBidirectional()
}

export async function syncAccountsBidirectional(): Promise<SyncReport> {
  try {
    const res = await emailApi.listAccounts()
    const remote = res.accounts ?? []
    let applied = 0
    let skipped = 0
    let pushed = 0
    // 网页未解锁 / jeep-sqlite 未挂时本地库不可用：只以服务端列表为准，
    // 不把「LocalDB 未初始化」当成同步失败，否则设置页会整页被错误态盖住。
    if (!localDB.isReady()) {
      return { fetched: remote.length, applied, skipped, pushed, online: true }
    }
    try {
      for (const a of remote) {
        if (isLocalTestAddress(a.emailAddress)) {
          skipped++
          continue
        }
        try {
          const updatedAt = a.updatedAt ?? a.createdAt ?? 0
          const won = await writeAccountIfNewer({
            id: a.id,
            displayName: a.displayName,
            emailAddress: a.emailAddress,
            imapHost: a.imapHost,
            imapPort: a.imapPort,
            authType: a.authType,
            syncIntervalMin: a.syncIntervalMin ?? 15,
            enabled: !!a.enabled,
            updatedAt,
          })
          if (won) applied++
          else skipped++
        } catch (e: unknown) {
          skipped++
          console.warn('[email] mirror account write skipped:', a.emailAddress, e)
        }
      }

      for (const a of await listAccounts()) {
        if (isLocalTestAddress(a.emailAddress)) {
          await deleteAccount(a.id)
        }
      }
      const local = await listAccounts()
      const plan = planAccountSync(
        local.map((a) => ({ id: a.id, emailAddress: a.emailAddress, updatedAt: a.updatedAt })),
        remote.map((a) => ({
          id: a.id,
          emailAddress: a.emailAddress,
          updatedAt: a.updatedAt ?? a.createdAt ?? 0,
        })),
      )
      const remoteById = new Map(remote.map((a) => [a.id, a]))
      const localById = new Map(local.map((a) => [a.id, a]))
      for (const id of plan.pushIds) {
        const l = localById.get(id)
        if (!l) continue
        const target = remoteById.get(id)
          ?? remote.find((a) => a.emailAddress.toLowerCase() === l.emailAddress.toLowerCase())
        if (!target) continue
        const ok = await pushAccountToServer({
          ...target,
          displayName: l.displayName,
          syncIntervalMin: l.syncIntervalMin,
          enabled: l.enabled,
        })
        if (ok) pushed++
      }
    } catch (e: unknown) {
      // 本地镜像写入失败不否掉服务端结果；设置页仍以远程列表为准。
      return {
        fetched: remote.length, applied, skipped, pushed, online: true,
        error: e instanceof Error ? e.message : String(e),
      }
    }
    return { fetched: remote.length, applied, skipped, pushed, online: true }
  } catch (e: any) {
    return { ...emptyReport(), error: e?.message }
  }
}

export async function pushAccountToServer(_a: ServerAccount): Promise<boolean> {
  try {
    await emailApi.updateAccount(_a.id, {
      displayName: _a.displayName,
      syncIntervalMin: _a.syncIntervalMin,
      enabled: _a.enabled,
    })
    return true
  } catch {
    void import('../../native/config-sync/outbox').then((m) =>
      m.enqueueConfigPush({
        namespace: 'email_account',
        id: _a.id,
        payload: {
          displayName: _a.displayName,
          syncIntervalMin: _a.syncIntervalMin,
          enabled: _a.enabled,
        },
        updatedAt: _a.updatedAt ?? Math.floor(Date.now() / 1000),
      }),
    )
    return false
  }
}

let watcherStarted = false
let inflight: Promise<SyncReport> | null = null

/** 登录且本地库解锁后自动同步一次；重复调用幂等。 */
export function startEmailConfigSync(): void {
  if (watcherStarted) return
  watcherStarted = true
  const auth = useAuthStore()
  watch(
    [lobsterReady, () => auth.isAuthenticated],
    ([ready, authed]) => {
      if (!ready || !authed) return
      if (inflight) return
      inflight = syncAccountsBidirectional()
        .catch((e: unknown) => ({
          fetched: 0, applied: 0, skipped: 0, pushed: 0, online: false,
          error: e instanceof Error ? e.message : String(e),
        }))
        .finally(() => { inflight = null })
    },
    { immediate: true },
  )
}
