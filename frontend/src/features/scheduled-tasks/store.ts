import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { scheduledTasksApi } from './api'
import type { ScheduledTask, ScheduledTaskInput, ScheduledTaskRun } from './types'
import { enqueueConfigPush } from '../../native/config-sync/outbox'
import { nowUnixSec } from '../../native/config-sync/planner'
import { listLocalSettings, writeLocalIfNewer, writeLocalSetting, deleteLocalSetting } from '../../native/config-sync/settings-store'

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
    const applyFilter = (rows: ScheduledTask[]) => enabledOnly ? rows.filter((t) => t.enabled) : rows
    let localSince = 0
    let localRows: ScheduledTask[] = []
    try {
      const local = (await listLocalSettings()).filter((row) => row.namespace === 'scheduled_task')
      localRows = local.map((row) => row.payload as ScheduledTask)
      if (local.length) {
        tasks.value = applyFilter(localRows)
        loading.value = false
      }
      // 增量基线：本地最大 updated_at（秒），只向服务端拉变更行
      localSince = localRows.reduce((m, t) => Math.max(m, t.updatedAt || 0), 0)
    } catch { /* 无本地库时继续拉服务端 */ }
    try {
      const { tasks: changed, deletedIds = [] } = await scheduledTasksApi.list(enabledOnly, localSince || undefined)
      for (const task of changed) {
        await writeLocalIfNewer({
          namespace: 'scheduled_task', id: task.id, payload: task, updatedAt: task.updatedAt || 0,
        })
      }
      for (const id of deletedIds) {
        // 其他端已删除：移除本地镜像行，避免僵尸行在下一次加载复活
        await deleteLocalSetting('scheduled_task', id)
      }
      const tombstoned = new Set(deletedIds)
      // 增量结果合并进本地镜像：changed 行 LWW 覆盖，tombstoned 行剔除
      const merged = new Map(localRows.map((t) => [t.id, t]))
      for (const task of changed) merged.set(task.id, task)
      for (const id of tombstoned) merged.delete(id)
      tasks.value = [...merged.values()]
      error.value = ''
    } catch (e: any) {
      if (!tasks.value.length) {
        error.value = e?.message || '加载自动化失败'
        throw e
      }
    } finally { loading.value = false }
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
    try {
      const task = await scheduledTasksApi.create(input)
      await writeLocalSetting({ namespace: 'scheduled_task', id: task.id, payload: task, dirty: 0, updatedAt: task.updatedAt })
      tasks.value = [task, ...tasks.value]
      return task
    } catch {
      const id = crypto.randomUUID()
      const now = nowUnixSec()
      const draft = { id, ...input, enabled: input.enabled ?? true, updatedAt: now, createdAt: now } as ScheduledTask
      await writeLocalSetting({ namespace: 'scheduled_task', id, payload: draft, dirty: 1, updatedAt: now })
      await enqueueConfigPush({ namespace: 'scheduled_task', id, payload: input, updatedAt: now })
      tasks.value = [draft, ...tasks.value]
      return draft
    }
  }

  async function update(id: string, input: Partial<ScheduledTaskInput>) {
    const now = nowUnixSec()
    try {
      const task = await scheduledTasksApi.update(id, input)
      await writeLocalSetting({ namespace: 'scheduled_task', id: task.id, payload: task, dirty: 0, updatedAt: task.updatedAt })
      const index = tasks.value.findIndex((item) => item.id === id)
      if (index >= 0) tasks.value[index] = task
      if (selected.value?.id === id) selected.value = task
      return task
    } catch {
      const current = tasks.value.find((item) => item.id === id)
      const next = { ...current, ...input, id, updatedAt: now } as ScheduledTask
      await writeLocalSetting({ namespace: 'scheduled_task', id, payload: next, dirty: 1, updatedAt: now })
      await enqueueConfigPush({ namespace: 'scheduled_task', id, payload: input, updatedAt: now })
      const index = tasks.value.findIndex((item) => item.id === id)
      if (index >= 0) tasks.value[index] = next
      if (selected.value?.id === id) selected.value = next
      return next
    }
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
