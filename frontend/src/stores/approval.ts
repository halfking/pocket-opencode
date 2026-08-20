/**
 * Approval Store — 移动端 human-in-the-loop 审批状态。
 *
 * 职责：
 *   1. 周期性拉取 /api/mobile/approvals（近实时，默认 5s 轮询）
 *   2. 暴露待审批的权限请求与问答请求
 *   3. 发送批准/拒绝/回答/跳过，成功后刷新列表
 *
 * 与 session store 解耦：本 store 只关心「审批」，不碰聊天/SSE 流，
 * 因此不会阻塞或破坏既有的会话对话流程。
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

export const useApprovalStore = defineStore('approval', () => {
  const permissions = ref<PermissionRequest[]>([])
  const questions = ref<QuestionRequest[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  const instanceID = ref<string>('')
  const sessionID = ref<string>('')

  let pollTimer: number | null = null
  let fetching = false

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
      // 轮询失败不阻断聊天；仅记录，保留上一次结果
      error.value = err?.message || '加载审批失败'
    } finally {
      loading.value = false
      fetching = false
    }
  }

  /**
   * 设定作用域（实例 + 会话）并立即拉取一次 + 启动轮询。
   * 在 ApprovalPanel 挂载时调用，或会话切换时（watch）调用。
   */
  async function setScope(iid: string, sid: string) {
    instanceID.value = iid
    sessionID.value = sid
    loading.value = true
    await fetchPending()
    startPolling()
  }

  function startPolling(intervalMs = 5000) {
    stopPolling()
    pollTimer = window.setInterval(() => {
      fetchPending()
    }, intervalMs)
  }

  function stopPolling() {
    if (pollTimer !== null) {
      clearInterval(pollTimer)
      pollTimer = null
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

  /** 离开视图时清理：停止轮询并清空状态 */
  function reset() {
    stopPolling()
    permissions.value = []
    questions.value = []
    instanceID.value = ''
    sessionID.value = ''
    error.value = null
    loading.value = false
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
