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
import { emailApi, type AuthType, type EmailAccount as ServerAccount } from '../../api/email'
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

// LWW 判定与 AccountStamp 类型已抽到无依赖的 account-lww.ts（见那里的说明：
// 此前判定只在本文件内、测试却复制了一份，是假测试）。这里既 import 供本
// 文件使用、又 export 保持既有 import 路径可用；测试则直接测 account-lww.ts。
// 注意必须用 `import` + 单独 `export`，`export { x } from` 不会把名字引入
// 本模块作用域（下面 syncAccountsBidirectional 还要用它）。
import { planAccountSync, type AccountStamp } from './account-lww'
export { planAccountSync }
export type { AccountStamp }

const emptyReport = (): SyncReport => ({
  fetched: 0, applied: 0, skipped: 0, pushed: 0, online: false,
})

/**
 * 下行：把服务端账户按 LWW 写入本地镜像。返回 {applied, skipped}。
 * 单独抽出来是为了 409（服务端更新胜出）时只重跑下行，不重入整个双向同步。
 */
async function pullAccountsToLocal(remote: ServerAccount[]): Promise<{ applied: number; skipped: number }> {
  let applied = 0
  let skipped = 0
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
  return { applied, skipped }
}

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
      ;({ applied, skipped } = await pullAccountsToLocal(remote))

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
          // 这三个也必须取**本地**值。上面 spread 的 target 是服务端对象，
          // 直接用它等于把服务端的旧值原样发回去 —— 字段虽然带上了，
          // 但上行没有任何效果，等于没修。
          imapHost: l.imapHost,
          imapPort: l.imapPort,
          authType: narrowAuthType(l.authType),
          // 基准版本取**本地**那一份：它才是我们这次改动的出发点。
          // 用 target（服务端）的 updatedAt 会让守卫永远放行，等于没有守卫。
          updatedAt: l.updatedAt,
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

/**
 * 把本地镜像读回来的 auth_type（SQLite TEXT，故为 string）收窄成 AuthType。
 *
 * 本地库是服务端数据的镜像，正常情况下值一定在枚举内；但镜像是可被
 * 手工改坏的旧数据，非法值上行会让服务端 400，整条 LWW 同步卡住。
 * 未知值一律回落到 'password'（唯一被广泛支持的方式），宁可同步成
 * 可用状态也不要让整轮同步失败。
 */
function narrowAuthType(raw: string | undefined | null): AuthType {
  return raw === 'oauth2' ? 'oauth2' : 'password'
}

export async function pushAccountToServer(_a: ServerAccount): Promise<boolean> {
  // 带上本地基准版本：服务端据此做 LWW 守卫。若它手里的副本更新，会回 409
  // 而不是被我们的旧值静默覆盖（需求 8「以最后修改时间为准」）。
  //
  // 必须把 imapHost / imapPort / authType 一并上行。
  // 服务端的 updateEmailAccount **接受**这三个字段（body 里有对应指针），
  // 而下行 buildMirrorAccountWrite 会用服务端值**覆盖**本地同名列。
  // 早先这里只推 displayName / syncIntervalMin / enabled，于是：
  //   用户在设置页改了 IMAP 主机 → 本地 updatedAt 变大 → LWW 判「本地更新」
  //   → 触发上行 → 但 payload 里没有 imapHost，服务端原样不动
  //   → 下一轮下行又把服务端的旧 imapHost 覆盖回来
  // 用户改动被静默丢弃，且没有任何报错。必须与下行覆盖的字段集保持对称。
  //
  // updatedAt **不在**这个字段集里：它是 LWW 的基准版本，不是账户数据，
  // 由 updateAccount 的必填第三参单独传（早先混在 patch 里，于是「传没传」
  // 全看调用点心情：outbox 路径带了，交互式 UI 的几处没带，守卫静默失效）。
  const patch = {
    displayName: _a.displayName,
    syncIntervalMin: _a.syncIntervalMin,
    enabled: _a.enabled,
    imapHost: _a.imapHost,
    imapPort: _a.imapPort,
    authType: narrowAuthType(_a.authType),
  }
  try {
    await emailApi.updateAccount(_a.id, patch, _a.updatedAt ?? 0)
    return true
  } catch (e: unknown) {
    // 409 = 服务端更新，本地下行覆盖即可（不是「推送失败」，别进 outbox 干等）。
    const status = (e as { status?: number } | null)?.status
    if (status === 409) {
      console.warn('[email] account push rejected as stale; server copy wins', _a.id)
      const res = await emailApi.listAccounts().catch(() => null)
      if (res?.accounts) await pullAccountsToLocal(res.accounts).catch(() => undefined)
      return true
    }
    void import('../../native/config-sync/outbox').then((m) =>
      m.enqueueConfigPush({
        namespace: 'email_account',
        id: _a.id,
        payload: patch,
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
