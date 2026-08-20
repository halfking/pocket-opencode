/**
 * Approval Store — 移动端 human-in-the-loop 审批状态。
 *
 * 职责：
 *   1. 一次性 bootstrap 拉取 /api/mobile/approvals（覆盖断线窗口）
 *   2. 订阅 idempotentWsBus 的 approval.* 事件，实时增删待审批
 *   3. 暴露待审批的权限请求与问答请求
 *   4. 发送批准/拒绝/回答/跳过，成功后通过 WS 推送自然收敛（必要时再拉一次）
 *
 * 与 session store 解耦：本 store 只关心「审批」，不碰聊天/SSE 流，
 * 因此不会阻塞或破坏既有的会话对话流程。
 *
 * 实时通道说明（PR5 + 优化v4 §2 PR5）：
 *   - 后端 ApprovalBroadcaster 把 {type, request} 包装在 WsEnvelopeV1（cause.approval_id）
 *     中由 idempotentWsBus 归一化、幂等投递。
 *   - 本 store 订阅 APPROVAL_EVENT_TYPES 三种类型，按 instance+session 过滤后
 *     增量更新 permissions / questions。
 *   - 与 usePendingApprovals.ts（远程并行消费者）目前并存，待后续整合。
 */
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import {
  listPendingApprovals,
  replyPermission as apiReplyPermission,
  replyQuestion as apiReplyQuestion,
  rejectQuestion as apiRejectQuestion,
  type PermissionRequest,
  type QuestionRequest,
  type PermissionDecision,
} from '../api/approvals'
import {
  initIdempotentWsBus,
  subscribe,
  type SubscribeHandle,
  type WsEnvelopeV1,
} from '../services/idempotentWsBus'
import {
  APPROVAL_EVENT_TYPES,
  parseApprovalEvent,
} from '../services/approvalEvents'

export const useApprovalStore = defineStore('approval', () => {
  const permissions = ref<PermissionRequest[]>([])
  const questions = ref<QuestionRequest[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  const instanceID = ref<string>('')
  const sessionID = ref<string>('')

  let fetching = false
  // 订阅 handle 集合；tearDownBus 时统一释放，避免 setScope 切换泄漏。
  let wsHandles: SubscribeHandle[] = []
  let wsWired = false

  const hasPending = computed(
    () => permissions.value.length > 0 || questions.value.length > 0,
  )

  async function fetchPending() {
    if (fetching) return
    fetching = true
    error.value = null
    try {
      const data = await listPendingApprovals({
        instanceID: instanceID.value || undefined,
        sessionID: sessionID.value || undefined,
      })
      permissions.value = data.permissions || []
      questions.value = data.questions || []
    } catch (err: any) {
      // 拉取失败不阻断聊天；仅记录，保留上一次结果
      error.value = err?.message || '加载审批失败'
    } finally {
      loading.value = false
      fetching = false
    }
  }

  /**
   * 设定作用域（实例 + 会话）并立即拉取一次 + 启动 WS 订阅。
   * 在 ApprovalPanel 挂载时调用，或会话切换时（watch）调用。
   */
  async function setScope(iid: string, sid: string) {
    tearDownBus()
    instanceID.value = iid
    sessionID.value = sid
    loading.value = true
    await fetchPending()
    subscribeBus()
  }

  /**
   * 订阅 idempotentWsBus 的 approval.* 事件。当前 store 与
   * usePendingApprovals.ts 并行消费同一组事件（后者离线队列、UI 弹窗路径），
   * 两者通过 idempotentWsBus 的 (type, id) 幂等保护各自拿到一致的事件。
   * 待后续 PR 整合为一个消费者。
   */
  function subscribeBus(): void {
    if (!wsWired) {
      initIdempotentWsBus()
      wsWired = true
    }
    for (const evtType of APPROVAL_EVENT_TYPES) {
      const handle = subscribe(evtType, onApprovalEvent)
      wsHandles.push(handle)
    }
  }

  function tearDownBus(): void {
    for (const h of wsHandles) {
      try {
        h.unsubscribe()
      } catch {
        /* ignore — unsubscribe is idempotent in the bus */
      }
    }
    wsHandles = []
  }

  /**
   * 把审批事件归一化后应用到 store：pending 增量写入并去重；resolved 按 id 移除。
   * 不在当前 instance+session 作用域的事件忽略（与原 GET 的过滤范围一致）。
   */
  function onApprovalEvent(env: WsEnvelopeV1<unknown>): void {
    const info = parseApprovalEvent(env)
    if (!info) return
    if (info.instanceId !== instanceID.value || info.sessionId !== sessionID.value) {
      return
    }
    const outerType = (env as { type?: unknown }).type
    if (outerType === 'approval.resolved') {
      permissions.value = permissions.value.filter((p) => p.id !== info.requestId)
      questions.value = questions.value.filter((q) => q.id !== info.requestId)
      return
    }
    // pending：把内嵌的 request 抽出来写入对应数组
    const inner = (env as { data?: { data?: unknown } }).data
    const payload = (inner as { data?: unknown } | undefined)?.data
    if (!payload || typeof payload !== 'object') return
    const req = (payload as { request?: unknown }).request
    if (!req || typeof req !== 'object') return
    const request = req as PermissionRequest | QuestionRequest
    if (typeof request.id !== 'string' || request.id === '') return
    if (info.kind === 'permission') {
      if (!permissions.value.some((p) => p.id === request.id)) {
        permissions.value = [...permissions.value, request as PermissionRequest]
      }
    } else {
      if (!questions.value.some((q) => q.id === request.id)) {
        questions.value = [...questions.value, request as QuestionRequest]
      }
    }
  }

  async function doReplyPermission(
    requestID: string,
    decision: PermissionDecision,
    message?: string,
  ) {
    await apiReplyPermission(requestID, {
      instanceID: instanceID.value,
      sessionID: sessionID.value,
      decision,
      message,
    })
    await fetchPending()
  }

  async function approvePermission(requestID: string, message?: string) {
    await doReplyPermission(requestID, 'once', message)
  }

  async function alwaysPermission(requestID: string, message?: string) {
    await doReplyPermission(requestID, 'always', message)
  }

  async function denyPermission(requestID: string, message?: string) {
    await doReplyPermission(requestID, 'reject', message)
  }

  /** answers 为二维数组，下标对应每个子问题，值为所选 label / 自定义文本 */
  async function answerQuestion(requestID: string, answers: string[][]) {
    await apiReplyQuestion(requestID, {
      instanceID: instanceID.value,
      sessionID: sessionID.value,
      answers,
    })
    await fetchPending()
  }

  async function skipQuestion(requestID: string) {
    await apiRejectQuestion(requestID, {
      instanceID: instanceID.value,
      sessionID: sessionID.value,
    })
    await fetchPending()
  }

  /** 离开视图时清理：取消 WS 订阅并清空状态 */
  function reset() {
    tearDownBus()
    permissions.value = []
    questions.value = []
    instanceID.value = ''
    sessionID.value = ''
    error.value = null
    loading.value = false
  }

  /**
   * @deprecated 轮询路径已废弃；保留为 tearDownBus 的别名，兼容 ApprovalPanel
   * 等旧调用方。该方法将在下一次消费者整合 PR 中移除。
   */
  function stopPolling(): void {
    tearDownBus()
  }

  /**
   * @deprecated 轮询路径已废弃；保留为 setScope 的别名，行为不变（旧调用方
   * 期望切会话时重订阅）。该方法将在下一次消费者整合 PR 中移除。
   */
  async function startPolling(): Promise<void> {
    await setScope(instanceID.value, sessionID.value)
  }

  return {
    permissions,
    questions,
    loading,
    error,
    instanceID,
    sessionID,
    hasPending,
    setScope,
    fetchPending,
    startPolling,
    stopPolling,
    approvePermission,
    alwaysPermission,
    denyPermission,
    answerQuestion,
    skipQuestion,
    reset,
  }
})
