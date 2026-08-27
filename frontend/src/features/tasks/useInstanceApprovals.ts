/**
 * useInstanceApprovals — 指挥中心（/ai）的实例级待审批视图（设计方案 v2 §4.2-3）。
 *
 * 与 usePendingApprovals（instance+session 双过滤、会话页专用）不同，本模块
 * 拉取**整个实例**的待审批（permission + question），供 L0 分诊条 / L1 需介入
 * 列表聚合 needs-input 信号，并提供内联回复：
 *   - 权限：复用 usePendingApprovals().reply() —— 在线走服务端确认、离线走
 *     outbox 队列（P1 离线能力），409 冲突语义一致；
 *   - 问答：replyQuestion / rejectQuestion（在线路径；离线时提示进入会话处理）。
 *
 * 实时刷新：idempotentWsBus 的 approval.* 事件触发 250ms 去抖对齐拉取（与
 * usePendingApprovals 同款节拍）；断线时由调用方的下拉刷新兜底。
 *
 * P0 近似：审批等待时长用客户端首见时间（请求对象无服务端时间戳）；
 * `session.activity` 事件上线后（§5.1）由后端时间戳替换。
 */
import { ref, type Ref } from 'vue'
import { ApiError } from '../../api/http'
import {
  listPendingApprovals,
  replyQuestion,
  rejectQuestion,
  type QuestionInfo,
} from '../../api/approvals'
import { useConnectivityStore } from '../../stores/connectivity'
import { usePendingApprovals, type ReplyStatus } from '../../composables/usePendingApprovals'
import { initIdempotentWsBus, subscribe } from '../../services/idempotentWsBus'
import { APPROVAL_EVENT_TYPES } from '../../services/approvalEvents'

/** L1 需介入列表里的一个待办（权限或问答）。 */
export interface PendingItem {
  kind: 'permission' | 'question'
  requestId: string
  sessionId: string
  /** 权限请求的动作（bash/edit/…） */
  action?: string
  resources?: string[]
  /** 问答请求的第一个子问题（候选 chips 来源） */
  question?: QuestionInfo
  /** 客户端首见时间（ms），跨刷新保持以近似等待时长 */
  firstSeenAt: number
}

export interface UseInstanceApprovalsReturn {
  pending: Ref<PendingItem[]>
  loadError: Ref<string>
  refresh: () => Promise<void>
  startLive: () => void
  stopLive: () => void
  replyPermission: (
    item: PendingItem,
    decision: 'once' | 'always' | 'reject',
  ) => Promise<ReplyStatus>
  answerQuestion: (item: PendingItem, optionLabel: string) => Promise<ReplyStatus>
  skipQuestion: (item: PendingItem) => Promise<ReplyStatus>
}

export function useInstanceApprovals(instanceId: () => string): UseInstanceApprovalsReturn {
  const conn = useConnectivityStore()
  const pending = ref<PendingItem[]>([])
  const loadError = ref('')
  const firstSeen = new Map<string, number>()

  let refreshing = false
  let eventHandles: Array<ReturnType<typeof subscribe>> = []
  let refreshDebounce: ReturnType<typeof setTimeout> | null = null

  /** 回复通道按 (instance,session) 缓存，复用 usePendingApprovals.reply 的
   * 全部语义（在线确认 / 离线 outbox / 409 冲突）。 */
  const replyChannels = new Map<string, ReturnType<typeof usePendingApprovals>>()
  function channelFor(sessionId: string) {
    const key = `${instanceId()}::${sessionId}`
    let ch = replyChannels.get(key)
    if (!ch) {
      ch = usePendingApprovals({ instanceId, sessionId: () => sessionId })
      replyChannels.set(key, ch)
    }
    return ch
  }

  async function refresh(): Promise<void> {
    const iid = instanceId()
    if (!iid || !conn.online) return
    if (refreshing) return
    refreshing = true
    try {
      const result = await listPendingApprovals({ instanceID: iid })
      const now = Date.now()
      const seen = new Set<string>()
      const items: PendingItem[] = []
      for (const p of result.permissions ?? []) {
        if (!p?.id || seen.has(p.id)) continue
        seen.add(p.id)
        if (!firstSeen.has(p.id)) firstSeen.set(p.id, now)
        items.push({
          kind: 'permission',
          requestId: p.id,
          sessionId: p.sessionID,
          action: p.action,
          resources: p.resources,
          firstSeenAt: firstSeen.get(p.id)!,
        })
      }
      for (const q of result.questions ?? []) {
        if (!q?.id || seen.has(q.id)) continue
        seen.add(q.id)
        if (!firstSeen.has(q.id)) firstSeen.set(q.id, now)
        items.push({
          kind: 'question',
          requestId: q.id,
          sessionId: q.sessionID,
          question: q.questions?.[0],
          firstSeenAt: firstSeen.get(q.id)!,
        })
      }
      // 已消失的请求清理首见记录，避免 Map 无界增长。
      for (const id of firstSeen.keys()) if (!seen.has(id)) firstSeen.delete(id)
      pending.value = items.sort((a, b) => a.firstSeenAt - b.firstSeenAt)
      loadError.value = ''
    } catch (err) {
      // 拉取失败保留上次列表，分诊条退化为无审批信号。
      loadError.value = err instanceof Error ? err.message : '待审批拉取失败'
    } finally {
      refreshing = false
    }
  }

  function removeLocal(requestId: string): void {
    pending.value = pending.value.filter((p) => p.requestId !== requestId)
  }

  async function replyPermission(
    item: PendingItem,
    decision: 'once' | 'always' | 'reject',
  ): Promise<ReplyStatus> {
    // 离线入队由 reply() 内部处理（queued-offline）；conflict 时本地同步移除。
    const status = await channelFor(item.sessionId).reply(item.requestId, decision)
    if (status === 'confirmed' || status === 'queued-offline' || status === 'conflict') {
      removeLocal(item.requestId)
    }
    return status
  }

  async function answerQuestion(item: PendingItem, optionLabel: string): Promise<ReplyStatus> {
    try {
      await replyQuestion(item.requestId, {
        instanceID: instanceId(),
        sessionID: item.sessionId,
        answers: [[optionLabel]],
      })
      removeLocal(item.requestId)
      return 'confirmed'
    } catch (err) {
      // 409 视为已在别处处理；其余失败保留条目可重试。
      if (err instanceof ApiError && err.status === 409) {
        removeLocal(item.requestId)
        return 'conflict'
      }
      return 'failed'
    }
  }

  async function skipQuestion(item: PendingItem): Promise<ReplyStatus> {
    try {
      await rejectQuestion(item.requestId, {
        instanceID: instanceId(),
        sessionID: item.sessionId,
      })
      removeLocal(item.requestId)
      return 'confirmed'
    } catch {
      return 'failed'
    }
  }

  function startLive(): void {
    initIdempotentWsBus()
    if (eventHandles.length === 0) {
      eventHandles = APPROVAL_EVENT_TYPES.map((t) =>
        subscribe(t, () => {
          if (refreshDebounce !== null) return
          refreshDebounce = setTimeout(() => {
            refreshDebounce = null
            void refresh()
          }, 250)
        }),
      )
    }
    void refresh()
  }

  function stopLive(): void {
    if (refreshDebounce !== null) {
      clearTimeout(refreshDebounce)
      refreshDebounce = null
    }
    for (const h of eventHandles) h.unsubscribe()
    eventHandles = []
  }

  return {
    pending,
    loadError,
    refresh,
    startLive,
    stopLive,
    replyPermission,
    answerQuestion,
    skipQuestion,
  }
}
