import { assertAuthSession, captureAuthSession, useAuthStore } from '../../stores/auth'
import { readCachedModuleMode } from '../../stores/syncPolicyStore'
import { localDB } from '../local-db'
import type { HttpOptions } from '../../api/http'
import type { SyncModule } from '../../api/syncPolicyContract'
import type { ConfigOwner } from './ownership'

export const CONFIG_MODULE: Readonly<Record<string, SyncModule>> = Object.freeze({
  email_account: 'email', chat_settings: 'ai-chat', chat_agent: 'ai-chat', scheduled_task: 'tasks',
})

export type ConfigDb = Pick<ReturnType<typeof localDB.bindOperation>, 'run' | 'query' | 'queryOne'>

/**
 * 捕获一个「本地编辑」租约，并从**同一个租约**取出归属。
 *
 * ⚠ 为什么归属取自租约而不是直接读 auth store：租约的 owner 由
 *   `requestOwnerActivation` 在**已验证**登录时写入，切账号会换 epoch。
 *   从租约取归属 ⇒ 归属与「这次写操作属于哪一代会话」**原子一致**，
 *   不会出现「租约是 A 的、归属却是 B 的」这种撕裂。
 *
 * ⚠ 恢复出来的会话没有已验证归属（auth.ts:「Persisted metadata is not a
 *   verified owner assertion」）⇒ 租约的 principal 为 null ⇒ owner 为 null
 *   ⇒ 写下的行没有归属，永不上传。
 */
export function captureLocalConfigLease(): { db: ConfigDb; owner: ConfigOwner | null } {
  const lease = localDB.captureOperation({ kind: 'local-edit' })
  const principal = lease.owner.principal
  return {
    db: localDB.bindOperation(lease),
    owner: principal ? { userId: principal.userId, workspaceId: principal.workspaceId } : null,
  }
}

export interface ConfigOperation {
  readonly db: ConfigDb
  readonly workspaceId: string
  readonly user: string
  /**
   * 本次操作的行归属。
   *
   * ⚠ `null` = **当前会话没有已验证的归属**（例如从 localStorage 恢复的会话
   *   没有 userId —— 见 auth.ts 的「Persisted metadata is not a verified owner
   *   assertion」）。这时写下的行**不带归属** ⇒ 它们永远不会被上传，
   *   也不会被下一个账号捡走。这是有意的保守行为。
   */
  readonly owner: ConfigOwner | null
  assertCurrent(): void
  canSync(namespace: string): boolean
  forNamespace(namespace: string): ConfigOperation
  requestOptions(namespace?: string): HttpOptions & { assertCurrent: () => void }
}

/** Capture all available grants before the first await, never during a retry. */
export function captureConfigOperation(): ConfigOperation {
  const auth = useAuthStore(), session = captureAuthSession(auth)
  const workspaceId = auth.workspaceId, user = auth.user
  // 归属取自**本地编辑租约**的 owner principal：它由已验证登录写入，
  // 切账号即换 epoch ⇒ 归属与本次操作属于同一代会话。
  const localLease = localDB.captureOperation({ kind: 'local-edit' })
  const principal = localLease.owner.principal
  const owner = principal ? { userId: principal.userId, workspaceId: principal.workspaceId } : null
  const local = localDB.bindOperation(localLease)
  const grants = new Map<SyncModule, ReturnType<typeof localDB.bindOperation>>()
  for (const module of new Set(Object.values(CONFIG_MODULE))) {
    if (readCachedModuleMode(module) === 'cloud') {
      grants.set(module, localDB.bindOperation(localDB.captureOperation({ kind: 'cloud-sync', module })))
    }
  }
  const create = (db: ReturnType<typeof localDB.bindOperation>, module?: SyncModule): ConfigOperation => {
    const assertCurrent = () => {
      assertAuthSession(auth, session)
      db.assertCurrent()
      if (module && readCachedModuleMode(module) !== 'cloud') {
        throw Object.assign(new Error('Module synchronization is local.'), { name: 'AbortError' })
      }
      // The admission reader can invoke a host-supplied identity predicate.
      assertAuthSession(auth, session)
      db.assertCurrent()
    }
    return Object.freeze({ db, workspaceId, user, owner, assertCurrent,
      canSync(namespace: string) {
        assertCurrent()
        const target = CONFIG_MODULE[namespace]
        return !target || (grants.has(target) && readCachedModuleMode(target) === 'cloud')
      },
      forNamespace(namespace: string) {
        assertCurrent()
        const target = CONFIG_MODULE[namespace]
        if (!target) return create(local)
        const grant = grants.get(target)
        if (!grant || readCachedModuleMode(target) !== 'cloud') {
          throw Object.assign(new Error('Module synchronization is local.'), { name: 'AbortError' })
        }
        grant.assertCurrent()
        return create(grant, target)
      },
      requestOptions(namespace?: string) {
        assertCurrent()
        const target = namespace ? CONFIG_MODULE[namespace] : module
        return { assertCurrent, ...(target ? { syncModule: target } : {}) }
      },
    })
  }
  return create(local)
}

/**
 * 捕获一次配置操作，**等归属确认完成后再捕获**。
 *
 * ⚠ 为什么需要这个异步版本：恢复出来的会话一开始没有已验证归属（持久化的
 *   workspaceId 是客户端自报的），而服务端确认归属是**异步**的。
 *   若在确认完成前捕获操作，这次操作的归属就会是「未知」⇒ 它的行永不上传。
 *   ⇒ 同步路径必须等归属就绪，否则每次重启应用都会丢掉本该同步的配置。
 *
 * ⚠ 等待期间会话可能已变（切账号/登出）⇒ 确认后重新捕获，归属随之更新。
 */
export async function captureConfigOperationReady(): Promise<ConfigOperation> {
  const auth = useAuthStore()
  await auth.whenOwnerReady()
  return captureConfigOperation()
}
