/**
 * usePendingApprovals — 会话页待审批请求的拉取与回复（M2 薄包装）。
 *
 * M2（2026-09-09）改造：原 composable自己拥有 timer + WS 订阅，
 * 组件 unmount 时被 stopPolling 整体清掉。改为薄包装：所有"轮询 + WS 订阅"
 * 都委托给 `approvalsRuntime`（进程级 singleton），组件只持有
 * pendingPermissions 的本地 ref 并订阅该 (instanceId, sessionId)。
 *
 * 调用契约不变：
 *   startPolling() → 订阅 runtime（路由进入/审批 sheet 唤起时调）
 *   stopPolling()  → 退订（组件 unmount 时调）
 *
 * 实时来源 + 兜底逻辑（与原版一致）全部下沉到 approvalsRuntime：
 *   - WS 审批推送事件 → 250ms 去抖对齐刷新
 *   - WS 断线时回到 10s 轮询
 *   - 切换会话 / 离开页面 → runtime 继续跑；用户回来时直接拿到最新列表
 */
import { ref, type Ref } from 'vue'
import { ApiError } from '../api/http'
import {
  replyPermissionFlat,
  type PermissionRequest,
} from '../api/approvals.ts'
import { useAuthStore } from '../stores/auth'
import { useConnectivityStore } from '../stores/connectivity'
import { isLobsterReady } from '../native/lobster-init.ts'
import { localDB, localDbAsSql } from '../native/local-db.ts'
import { SqliteOutboxStore } from '../native/outboxStore.ts'
import { SqliteApprovalStore } from '../native/approvalStore.ts'
import { enqueueApprovalReplyLocally } from '../native/mobileOffline.ts'
import { getApprovalsRuntime } from '../native/approvalsRuntime.ts'

export type ReplyStatus = 'confirmed' | 'queued-offline' | 'conflict' | 'failed'

export interface UsePendingApprovalsReturn {
  pendingPermissions: Ref<PermissionRequest[]>
  loadError: Ref<string>
  refresh(): Promise<void>
  reply(requestId: string, decision: 'once' | 'always' | 'reject'): Promise<ReplyStatus>
  /** 订阅当前 (instanceId, sessionId)；M2 下沉到 runtime，幂等。 */
  startPolling(): void
  /** 退订；M2 下沉到 runtime，幂等。 */
  stopPolling(): void
}

export function usePendingApprovals(args: {
  instanceId: () => string
  sessionId: () => string
}): UsePendingApprovalsReturn {
  const conn = useConnectivityStore()
  const auth = useAuthStore()
  const pendingPermissions = ref<PermissionRequest[]>([])
  const loadError = ref('')
  let unsubscribe: (() => void) | null = null

  // runtime 回调：每次 list 变化或拉取失败都同步到本地 ref
  function onChange(list: PermissionRequest[], err: string): void {
    pendingPermissions.value = list
    loadError.value = err
  }

  async function refresh(): Promise<void> {
    const instanceId = args.instanceId()
    const sessionId = args.sessionId()
    if (!instanceId || !sessionId || !conn.online) return
    // 走 runtime.refresh()（依赖注入下可单独调用），无需重新订阅
    await getApprovalsRuntime()?.refresh()
  }

  async function reply(requestId: string, decision: 'once' | 'always' | 'reject'): Promise<ReplyStatus> {
    const instanceId = args.instanceId()
    const sessionId = args.sessionId()

    // 离线 + 本地库已解锁 → 走 outbox（P1 离线队列）。
    if (!conn.online && isLobsterReady()) {
      try {
        const db = localDbAsSql(localDB)
        await enqueueApprovalReplyLocally({
          outbox: new SqliteOutboxStore(db),
          workspaceId: auth.workspaceId || 'default',
          approvalStore: new SqliteApprovalStore(db),
          reply: {
            kind: 'permission',
            requestId,
            instanceId,
            sessionId,
            decision,
          },
        })
        await conn.refreshCounts()
        pendingPermissions.value = pendingPermissions.value.filter((p) => p.id !== requestId)
        return 'queued-offline'
      } catch {
        return 'failed'
      }
    }

    try {
      await replyPermissionFlat({ instanceId, sessionId, requestId, decision })
      pendingPermissions.value = pendingPermissions.value.filter((p) => p.id !== requestId)
      return 'confirmed'
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        pendingPermissions.value = pendingPermissions.value.filter((p) => p.id !== requestId)
        return 'conflict'
      }
      return 'failed'
    } finally {
      // 触发一次对齐刷新
      void getApprovalsRuntime()?.refresh()
    }
  }

  function startPolling(): void {
    const instanceId = args.instanceId()
    const sessionId = args.sessionId()
    if (!instanceId || !sessionId) return
    if (unsubscribe) return // 幂等：已订阅不重复
    unsubscribe = getApprovalsRuntime()?.subscribe(instanceId, sessionId, onChange) ?? null
    // 进入页面立即拉一次（与原 usePendingApprovals 行为一致）
    void refresh()
  }

  function stopPolling(): void {
    if (unsubscribe) {
      unsubscribe()
      unsubscribe = null
    }
  }

  return { pendingPermissions, loadError, refresh, reply, startPolling, stopPolling }
}
