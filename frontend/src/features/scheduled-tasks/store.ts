import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { scheduledTasksApi } from './api'
import type { ScheduledTask, ScheduledTaskInput, ScheduledTaskRun } from './types'

export const useScheduledTasksStore = defineStore('scheduledTasks', () => {
  const tasks = ref<ScheduledTask[]>([])
  const selected = ref<ScheduledTask | null>(null)
  const runs = ref<ScheduledTaskRun[]>([])
  const loading = ref(false)
  const detailLoading = ref(false)
  const error = ref('')

  const enabledTasks = computed(() => tasks.value.filter((task) => task.enabled))

  async function load(enabledOnly = false) {
    loading.value = true
    error.value = ''
    try { tasks.value = await scheduledTasksApi.list(enabledOnly) }
    catch (e: any) { error.value = e?.message || '加载自动化失败'; throw e }
    finally { loading.value = false }
  }

  async function loadOne(id: string) {
    detailLoading.value = true
    error.value = ''
    try {
      const [task, taskRuns] = await Promise.all([scheduledTasksApi.get(id), scheduledTasksApi.runs(id)])
      selected.value = task
      runs.value = taskRuns
      return task
    } catch (e: any) { error.value = e?.message || '加载自动化详情失败'; throw e }
    finally { detailLoading.value = false }
  }

  async function create(input: ScheduledTaskInput) {
    const task = await scheduledTasksApi.create(input)
    tasks.value = [task, ...tasks.value]
    return task
  }

  async function update(id: string, input: Partial<ScheduledTaskInput>) {
    const task = await scheduledTasksApi.update(id, input)
    const index = tasks.value.findIndex((item) => item.id === id)
    if (index >= 0) tasks.value[index] = task
    if (selected.value?.id === id) selected.value = task
    return task
  }

  async function run(id: string) {
    return scheduledTasksApi.run(id)
  }

  async function remove(id: string) {
    await scheduledTasksApi.remove(id)
    tasks.value = tasks.value.filter((task) => task.id !== id)
    if (selected.value?.id === id) selected.value = null
  }

  return {
    tasks, selected, runs, loading, detailLoading, error, enabledTasks,
    load, loadOne, create, update, run, remove,
  }
})
