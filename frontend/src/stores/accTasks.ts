/**
 * ACC Tasks Store — 把任务委托给 ACC（Agent Control Center）的客户端状态。
 *
 * 职责：
 *   1. 暴露当前最近一次委托的 ACC 任务、提交中 / 错误状态
 *   2. `createTask` 走 /api/tasks/delegate，成功后写回 `lastTask`
 *   3. `loadAgents` 读 ACC 的智能体清单（委托时选派给谁）
 *
 * 仅服务 TasksView 的「Delegate to ACC」按钮与「停止任务」入口，刻意保持小而轻，
 * 不引入轮询 / 缓存等副作用。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  createAccTask,
  listAccAgents,
  listAccSessions,
  listAccTasks,
  type AccAgent,
  type AccDelegateResult,
  type AccTask,
} from '../api/accTasks'

export const useAccTasksStore = defineStore('accTasks', () => {
  const lastTask = ref<AccTask | null>(null)
  const submitting = ref(false)
  const error = ref<string | null>(null)
  /** ACC 上的智能体清单（打开委托面板时拉一次）。 */
  const agents = ref<AccAgent[]>([])
  const agentsLoading = ref(false)
  const agentsError = ref<string | null>(null)
  /**
   * ACC 生态：ACC 上的 canonical 任务与已上报的会话。
   *
   * 这两个读面此前只有 API、没有调用方（前端门禁 check:dead-api 判为
   * 「新增死能力」⇒ 红的）。它们恰恰是需求里的「获取到当前的会话和任务
   * 等信息」—— 所以按「接上去」修，而不是删导出。
   */
  const accTasks = ref<AccTask[]>([])
  const accSessions = ref<any[]>([])
  const ecosystemLoading = ref(false)
  const ecosystemError = ref<string | null>(null)

  async function loadEcosystem() {
    ecosystemLoading.value = true
    try {
      const [t, s] = await Promise.all([listAccTasks(), listAccSessions()])
      accTasks.value = t
      accSessions.value = s
      ecosystemError.value = null
    } catch (err: any) {
      ecosystemError.value = err?.message || '读取 ACC 生态失败'
    } finally {
      ecosystemLoading.value = false
    }
  }

  /**
   * 委托一个任务给 ACC。
   * 成功 → 写回 `lastTask` 并返回 AccTask；失败 → 设置 `error` 并返回 null。
   */
  async function createTask(input: {
    title: string
    description?: string
    kind?: string
    agentId?: string
  }): Promise<AccTask | null> {
    submitting.value = true
    error.value = null
    try {
      const res: AccDelegateResult = await createAccTask(input)
      // ★ id 取**本地** task_id（后端委托信封的顶层字段），不是 ACC 任务 id：
      //   「停止」调用的是 POST /api/tasks/{本地id}/cancel，用 ACC id 会 404。
      const task: AccTask = {
        id: res?.task_id ?? '',
        title: input.title,
        description: input.description,
        kind: input.kind ?? res?.kind,
        status: res?.status ?? 'active',
        accTaskId: res?.acc_task_id,
        runId: res?.run_id,
        dispatchId: res?.dispatch_id,
        agentId: res?.agent_id,
        createdAt: new Date().toISOString(),
        updatedAt: new Date().toISOString(),
      }
      lastTask.value = task
      return task
    } catch (err: any) {
      error.value = err?.message || '委托任务失败'
      return null
    } finally {
      submitting.value = false
    }
  }

  /** 拉 ACC 的智能体清单。失败**不**阻断委托（后端会自己选一个可派的）。 */
  async function loadAgents(): Promise<void> {
    agentsLoading.value = true
    agentsError.value = null
    try {
      agents.value = await listAccAgents()
    } catch (err: any) {
      agentsError.value = err?.message || '读取 ACC 智能体失败'
    } finally {
      agentsLoading.value = false
    }
  }

  function reset() {
    lastTask.value = null
    submitting.value = false
    error.value = null
  }

  return {
    lastTask,
    submitting,
    error,
    agents,
    agentsLoading,
    agentsError,
    accTasks,
    accSessions,
    ecosystemLoading,
    ecosystemError,
    createTask,
    loadAgents,
    loadEcosystem,
    reset,
  }
})
