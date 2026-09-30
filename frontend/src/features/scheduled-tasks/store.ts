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
      // BUG-AP：本地镜像写失败不能连累服务端合并。原先这些写和合并在同一个
      // try 里，本地库一不可用（未解锁/迁移未完成）就整块跳到 catch，
      // tasks.value 停在旧值 —— 表现是「服务端明明有数据，列表却是空的」。
      // 本地镜像是缓存，服务端才是事实来源；缓存写失败只应丢缓存。
      for (const task of changed) {
        try {
          await writeLocalIfNewer({
            namespace: 'scheduled_task', id: task.id, payload: task, updatedAt: task.updatedAt || 0,
          })
        } catch (e) { console.warn('[scheduled-tasks] 本地镜像写失败（不影响列表）:', e) }
      }
      for (const id of deletedIds) {
        // 其他端已删除：移除本地镜像行，避免僵尸行在下一次加载复活
        try { await deleteLocalSetting('scheduled_task', id) }
        catch (e) { console.warn('[scheduled-tasks] 本地镜像删除失败（不影响列表）:', e) }
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
    // BUG-AP：原来服务端调用和本地镜像写挤在同一个 try 里。
    // 本地镜像写失败（例如本地库未就绪）会落进 catch，被当成「服务端创建失败」，
    // 于是**伪造一条 crypto.randomUUID() 的草稿并 enqueueConfigPush 推给服务端**。
    // 后果：服务端 POST 其实已经成功（PG 里确实有行），用户却看到「保存失败」，
    // 列表显示的是那条本地草稿，稍后再同步一次就成了**重复任务**。
    //
    // 现在把两者拆开：只有服务端调用本身失败才走离线草稿兜底。
    let task: ScheduledTask
    try {
      task = await scheduledTasksApi.create(input)
    } catch {
      const id = crypto.randomUUID()
      const now = nowUnixSec()
      const draft = { id, ...input, enabled: input.enabled ?? true, updatedAt: now, createdAt: now } as ScheduledTask
      await writeLocalSetting({ namespace: 'scheduled_task', id, payload: draft, dirty: 1, updatedAt: now })
      await enqueueConfigPush({ namespace: 'scheduled_task', id, payload: input, updatedAt: now })
      tasks.value = [draft, ...tasks.value]
      return draft
    }
    // 服务端已成功 —— 本地镜像只是缓存，写不进去也不能改 id、不能重推、不能报错
    try {
      await writeLocalSetting({ namespace: 'scheduled_task', id: task.id, payload: task, dirty: 0, updatedAt: task.updatedAt })
    } catch (e) { console.warn('[scheduled-tasks] 服务端已创建，但本地镜像写失败:', e) }
    tasks.value = [task, ...tasks.value]
    return task
  }

  async function update(id: string, input: Partial<ScheduledTaskInput>) {
    const now = nowUnixSec()
    // BUG-AP：与 create 同一个毛病——服务端 PATCH 成功但本地镜像写失败时，
    // 原实现会走离线兜底，**用同一个 id 造一条 dirty=1 的本地版本**并排队推送，
    // 等于把一次已成功的修改又推了一遍。服务端成功就不能再推。
    let task: ScheduledTask
    try {
      task = await scheduledTasksApi.update(id, input)
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
    try {
      await writeLocalSetting({ namespace: 'scheduled_task', id: task.id, payload: task, dirty: 0, updatedAt: task.updatedAt })
    } catch (e) { console.warn('[scheduled-tasks] 服务端已更新，但本地镜像写失败:', e) }
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
