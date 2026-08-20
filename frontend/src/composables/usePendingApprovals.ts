/**
 * usePendingApprovals — 会话页待审批请求的拉取与回复（08 §3.3、§4.5）。
 *
 * 实时来源：WS 审批推送事件（approval.permission.pending /
 * approval.question.pending / approval.resolved，经 idempotentWsBus 幂等投递），
 * 事件命中当前 instance+session 即触发对齐刷新。
 * 轮询兜底：WS 断线时退回 pollMs 轮询；重连后补拉一次，覆盖断线窗口内
 * 错过的事件。进入会话时始终先拉一次（事件只覆盖增量）。
 *
 * 回复分两条路径：
 *   - 在线：直接 POST，服务端确认（confirmed）后才算完成；
 *   - 离线（本地库已解锁）：写本地决定 + 入 outbox 队列，网络恢复后由
 *     MobileSyncRuntime drain 重放并回写终态（sent / expired）。
 * 409 = 请求不再 pending（在别处处理/过期），返回 'conflict' 由 UI 提示。
 */
import { ref, type Ref } from 'vue'
import { ApiError } from '../api/http'
import {
  listPendingApprovals,
  replyPermission,
  type PermissionRequest,
} from '../api/approvals'
import { useAuthStore } from '../stores/auth'
import { useConnectivityStore } from '../stores/connectivity'
import { isLobsterReady } from '../native/lobster-init'
import { localDB, localDbAsSql } from '../native/local-db'
import { SqliteOutboxStore } from '../native/outboxStore'
import { SqliteApprovalStore } from '../native/approvalStore'
import { enqueueApprovalReplyLocally } from '../native/mobileOffline'
import wsClient from '../api/websocket'
import { initIdempotentWsBus, subscribe } from '../services/idempotentWsBus'
import { APPROVAL_EVENT_TYPES, parseApprovalEvent } from '../services/approvalEvents'

export type ReplyStatus = 'confirmed' | 'queued-offline' | 'conflict' | 'failed'

export interface UsePendingApprovalsReturn {
  pendingPermissions: Ref<PermissionRequest[]>
  loadError: Ref<string>
  refresh(): Promise<void>
  reply(requestId: string, decision: 'once' | 'always' | 'reject'): Promise<ReplyStatus>
  startPolling(): void
  stopPolling(): void
}

const DEFAULT_POLL_MS = 10_000

export function usePendingApprovals(args: {
  instanceId: () => string
  sessionId: () => string
  pollMs?: number
}): UsePendingApprovalsReturn {
  const conn = useConnectivityStore()
  const auth = useAuthStore()
  const pendingPermissions = ref<PermissionRequest[]>([])
  const loadError = ref('')
  let timer: ReturnType<typeof setInterval> | null = null
  let refreshing = false
  let eventHandles: Array<ReturnType<typeof subscribe>> = []
  let refreshDebounce: ReturnType<typeof setTimeout> | null = null
  let wasWsConnected = false

  async function refresh(): Promise<void> {
    const instanceId = args.instanceId()
    const sessionId = args.sessionId()
    if (!instanceId || !sessionId || !conn.online) return
    if (refreshing) return
    refreshing = true
    try {
      const result = await listPendingApprovals(instanceId, sessionId)
      pendingPermissions.value = (result.permissions ?? []).filter(
        (p) => typeof p?.id === 'string' && p.id !== '' && p.sessionID === sessionId,
      )
      loadError.value = ''
    } catch (err) {
      // 拉取失败不打断会话：保留上次列表，页内可重试。
      loadError.value = err instanceof Error ? err.message : '审批状态拉取失败'
    } finally {
      refreshing = false
    }
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
        // 从本地待处理列表移除，避免重复弹窗；终态由 drain 回写。
        pendingPermissions.value = pendingPermissions.value.filter((p) => p.id !== requestId)
        return 'queued-offline'
      } catch {
        return 'failed'
      }
    }

    try {
      await replyPermission({ instanceId, sessionId, requestId, decision })
      pendingPermissions.value = pendingPermissions.value.filter((p) => p.id !== requestId)
      return 'confirmed'
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) {
        pendingPermissions.value = pendingPermissions.value.filter((p) => p.id !== requestId)
        return 'conflict'
      }
      return 'failed'
    } finally {
      void refresh()
    }
  }

  /** 事件触发的对齐刷新做 250ms 去抖：一轮连发事件只拉一次。 */
  function scheduleEventRefresh(): void {
    if (refreshDebounce !== null) return
    refreshDebounce = setTimeout(() => {
      refreshDebounce = null
      void refresh()
    }, 250)
  }

  function onApprovalEvent(env: unknown): void {
    const info = parseApprovalEvent(env)
    if (!info) return
    if (info.instanceId !== args.instanceId() || info.sessionId !== args.sessionId()) return
    if ((env as { type?: string }).type === 'approval.resolved') {
      // 服务端确认的终态：立即从列表移除，再对齐拉取其余条目。
      pendingPermissions.value = pendingPermissions.value.filter((p) => p.id !== info.requestId)
    }
    scheduleEventRefresh()
  }

  function startPolling(): void {
    if (timer !== null) return
    initIdempotentWsBus()
    if (eventHandles.length === 0) {
      eventHandles = APPROVAL_EVENT_TYPES.map((t) => subscribe(t, onApprovalEvent))
    }
    void refresh()
    wasWsConnected = wsClient.isConnected()
    timer = setInterval(() => {
      const connected = wsClient.isConnected()
      if (connected && wasWsConnected) return // WS 在线：事件驱动，跳过轮询
      wasWsConnected = connected
      // 断线兜底轮询；断线→重连的跳变补拉一次，覆盖错过的事件。
      void refresh()
    }, args.pollMs ?? DEFAULT_POLL_MS)
  }

  function stopPolling(): void {
    if (timer !== null) {
      clearInterval(timer)
      timer = null
    }
    if (refreshDebounce !== null) {
      clearTimeout(refreshDebounce)
      refreshDebounce = null
    }
    for (const h of eventHandles) h.unsubscribe()
    eventHandles = []
  }

  return { pendingPermissions, loadError, refresh, reply, startPolling, stopPolling }
}
