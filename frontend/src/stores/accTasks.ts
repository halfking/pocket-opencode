/**
 * ACC Tasks Store — 把任务委托给 ACC（Agent Control Center）的客户端状态。
 *
 * 职责：
 *   1. 暴露当前最近一次委托的 ACC 任务、提交中 / 错误状态
 *   2. `createTask` 走 /api/tasks/delegate，成功后写回 `lastTask`
 *
 * 仅服务 TasksView 的「Delegate to ACC」按钮，刻意保持小而轻，
 * 不引入轮询 / 缓存等副作用。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { createAccTask, type AccTask } from '../api/accTasks'

export const useAccTasksStore = defineStore('accTasks', () => {
  const lastTask = ref<AccTask | null>(null)
  const submitting = ref(false)
  const error = ref<string | null>(null)

  /**
   * 委托一个任务给 ACC。
   * 成功 → 写回 `lastTask` 并返回 AccTask；失败 → 设置 `error` 并返回 null。
   */
  async function createTask(input: {
    title: string
    description?: string
    kind?: string
  }): Promise<AccTask | null> {
    submitting.value = true
    error.value = null
    try {
      const { raw } = await createAccTask(input)
      // 后端响应形状我们不强约束；从 raw 里兜底挑出常见字段
      const task: AccTask = {
        id: raw?.id ?? raw?.task_id ?? raw?.taskId ?? '',
        title: input.title,
        description: input.description,
        kind: input.kind ?? raw?.kind,
        status: raw?.status ?? 'pending',
        createdAt: raw?.createdAt ?? raw?.created_at ?? new Date().toISOString(),
        updatedAt: raw?.updatedAt ?? raw?.updated_at ?? new Date().toISOString(),
        ...raw,
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

  function reset() {
    lastTask.value = null
    submitting.value = false
    error.value = null
  }

  return {
    lastTask,
    submitting,
    error,
    createTask,
    reset,
  }
})