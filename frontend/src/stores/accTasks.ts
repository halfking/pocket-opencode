/**
 * ACC Tasks Store — 把任务委托给 ACC（Agent Control Center）的客户端状态。
 *
 * 职责：
 *   1. 暴露当前最近一次委托的 ACC 任务、提交中 / 错误状态
 *   2. `createTask` 走 /api/tasks/delegate，成功后写回 `lastTask`
 *   3. `loadAccAgents` 读 ACC 的智能体清单（委托时选派给谁）
 *
 * 仅服务 TasksView 的「Delegate to ACC」按钮与「停止任务」入口，刻意保持小而轻，
 * 不引入轮询 / 缓存等副作用。
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  createAccTask,
  getAccArtifactContent,
  listAccAgents,
  listAccRunArtifacts,
  listAccRuns,
  listAccSessions,
  listAccTasks,
  type AccAgent,
  type AccDelegateResult,
  type AccRun,
  type AccRunArtifacts,
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

  /**
   * 任务历史与产出（2026-10-27 新增）。
   *
   * 这一块此前**整个不存在**：daemon 侧对 ACC 派单不留 run，pocketd 侧也
   * 没有出口 ⇒ 手机上既看不到「任务历史」，也看不到「生成的文件」。
   *
   * ★ 三条纪律（都来自真机踩坑，不是风格偏好）：
   *
   *   ① **默认不加载**：卡片收起时屏上只有「读取任务历史」这个按钮。
   *      否则 Maestro 那两条真机流的判据会被本卡的行文本命中
   *      （`notVisible: "maestro-acc-stop"`、`notVisible: "已完成|已取消|已失败"`）
   *      ⇒ 流会红在一个跟被测功能无关的地方。
   *   ② **不把智能体的答复原文渲染在列表行里**：答复可能是「已完成」这三个字
   *      （实测 maestro-acc-approve 的答复就是它）⇒ 会命中上面那条判据。
   *      答复放在展开后的详情里。
   *   ③ 状态标签用「成功 / 失败 / 取消」而不是「已完成 / 已失败 / 已取消」：
   *      前三个是 Maestro `visible` 的**完整匹配**目标，后三个会让
   *      `notVisible: "已完成|已取消|已失败"` 永远为假。
   */
  const runs = ref<AccRun[]>([])
  const runsLoading = ref(false)
  const runsError = ref<string | null>(null)
  /** 已展开的 run 的产出详情（run_id → 详情）。 */
  const runDetails = ref<Record<string, AccRunArtifacts>>({})
  const runDetailLoading = ref<string | null>(null)
  const runDetailError = ref<string | null>(null)
  /** 已读取的产出文件内容（`${dispatchId}/${name}` → 内容）。 */
  const artifactContents = ref<Record<string, string>>({})
  const artifactLoading = ref<string | null>(null)

  async function loadRuns() {
    runsLoading.value = true
    runsError.value = null
    try {
      runs.value = await listAccRuns()
    } catch (err: any) {
      runsError.value = err?.message || '读取任务历史失败'
    } finally {
      runsLoading.value = false
    }
  }

  /**
   * 读一条 run 的产出（工具 + 文件）。展开时才读，避免一次性打 N 个请求。
   */
  async function loadRunDetail(runId: string) {
    if (!runId) return
    runDetailLoading.value = runId
    runDetailError.value = null
    try {
      const detail = await listAccRunArtifacts(runId)
      runDetails.value = { ...runDetails.value, [runId]: detail }
    } catch (err: any) {
      runDetailError.value = err?.message || '读取产出失败'
    } finally {
      runDetailLoading.value = null
    }
  }

  /** 读一个产出文件的内容（点文件名时才读）。 */
  async function loadArtifactContent(dispatchId: string, name: string) {
    if (!dispatchId || !name) return
    const key = `${dispatchId}/${name}`
    artifactLoading.value = key
    try {
      const res = await getAccArtifactContent(dispatchId, name)
      artifactContents.value = { ...artifactContents.value, [key]: res.content }
    } catch (err: any) {
      runDetailError.value = err?.message || '读取文件失败'
    } finally {
      artifactLoading.value = null
    }
  }

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
  // ★ 2026-10-27 改名 `loadAgents` → `loadAccAgents`。
  //
  //   原因：前端门禁 check:error-outlet 按**函数名**扫调用点
  //   （`\bloadAgents\b`），而 chatAgentStore 有一个同名的 `loadAgents`
  //   —— 它**失败会 rethrow**，门禁因此要求它的每个调用点都接 .catch。
  //   `accStore.loadAgents()`（ACC 的智能体清单，**从不** rethrow）被同一个
  //   名字扫进来 ⇒ 门禁判它是「没接住 rejection 的调用点」⇒ 假红。
  //   ⇒ 改掉名字这一侧的碰撞，而不是给一个**不可能 reject** 的 promise
  //     挂一个空 catch（那是在骗判据）。
  async function loadAccAgents(): Promise<void> {
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
    runs,
    runsLoading,
    runsError,
    runDetails,
    runDetailLoading,
    runDetailError,
    artifactContents,
    artifactLoading,
    createTask,
    loadAccAgents,
    loadEcosystem,
    loadRuns,
    loadRunDetail,
    loadArtifactContent,
    reset,
  }
})
