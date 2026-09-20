/**
 * useTaskSessionSheet — TaskSessionSheet 的视图模型（2026-09-20 ViewModel 拆分）。
 *
 * 原 `TaskSessionSheet.vue` 直接 `import { api } from '../../api/client'` 并自管三段异步：
 *  - load()               拉取 transcript
 *  - extractTitle()       提取标题
 *  - summarize()          重新生成总结
 *
 * 抽到本 composable 后：
 *  - Vue 文件聚焦模板与 props/emit；
 *  - 业务状态（kinds / messages / summary / title / loading / error）继续保留响应式；
 *  - 监听 row / kinds 变化自动重 fetch 的语义不变；
 *  - 任何上层 View 都可以复用同样的会话抽屉逻辑（TaskDetailView 下一步可接同一 hook）。
 *
 * 关键约束：
 *  - `load()` 失败路径继续保留空 messages + error 文案（与原行为一致）；
 *  - row 为 null 时所有方法快速返回，避免空指针；
 *  - watch 用 { immediate: true, deep: false }，与原行为等价。
 */
import { ref, watch, type Ref } from 'vue'
import { api, type TaskSessionBundleRow, type TaskSessionMessage } from '../../api/client'
import type { SessionMsgKind } from './SessionKindFilter.vue'

export interface UseTaskSessionSheetArgs {
  taskId: Ref<string> | string
  row: Ref<TaskSessionBundleRow | null> | TaskSessionBundleRow | null
}

export interface UseTaskSessionSheetReturn {
  kinds: Ref<SessionMsgKind[]>
  messages: Ref<TaskSessionMessage[]>
  summary: Ref<string>
  title: Ref<string>
  loading: Ref<boolean>
  error: Ref<string>
  reload: () => Promise<void>
  extractTitle: () => Promise<void>
  summarize: () => Promise<void>
}

export function useTaskSessionSheet(args: UseTaskSessionSheetArgs): UseTaskSessionSheetReturn {
  const taskIdRef: Ref<string> = typeof args.taskId === 'string'
    ? ref(args.taskId)
    : args.taskId
  // Row 形态归一为 Ref<... | null>：
  //  - null   → 新建一个 ref(null)
  //  - 非 null 且已是 Ref → 直接采用
  //  - 其它（裸对象）     → 包到新 ref 里
  const rowRef: Ref<TaskSessionBundleRow | null> =
    args.row === null
      ? ref<TaskSessionBundleRow | null>(null)
      : typeof args.row === 'object' && 'value' in args.row
        ? (args.row as Ref<TaskSessionBundleRow | null>)
        : ref<TaskSessionBundleRow | null>(args.row as TaskSessionBundleRow)

  const kinds = ref<SessionMsgKind[]>(['user', 'assistant', 'tool', 'thinking'])
  const messages = ref<TaskSessionMessage[]>([])
  const summary = ref('')
  const title = ref('')
  const loading = ref(false)
  const error = ref('')

  function sessionIdOf(row: TaskSessionBundleRow | null): string | null {
    if (!row) return null
    return row.agentSessionId || row.id || null
  }

  function kindOf(row: TaskSessionBundleRow | null): string {
    if (!row) return ''
    return (row.agentKind || '').replace(/^disk-/, '')
  }

  watch(rowRef, (row) => {
    title.value = row?.title || ''
    summary.value = ''
    messages.value = []
  }, { immediate: true })

  async function reload(): Promise<void> {
    const row = rowRef.value
    const sid = sessionIdOf(row)
    if (!row || !sid || !taskIdRef.value) return
    loading.value = true
    error.value = ''
    try {
      const data = await api.getTaskSessionTranscript(
        taskIdRef.value,
        sid,
        kinds.value.join(','),
        kindOf(row),
      )
      messages.value = data.messages || []
    } catch (e) {
      error.value = e instanceof Error ? e.message : '加载失败'
      messages.value = []
    } finally {
      loading.value = false
    }
  }

  watch(
    () => [rowRef.value?.id, kinds.value.join(',')],
    () => { void reload() },
  )

  async function extractTitle(): Promise<void> {
    const row = rowRef.value
    const sid = sessionIdOf(row)
    if (!row || !sid || !taskIdRef.value) return
    const { title: next } = await api.extractTaskSessionTitle(taskIdRef.value, sid)
    title.value = next
  }

  async function summarize(): Promise<void> {
    const row = rowRef.value
    const sid = sessionIdOf(row)
    if (!row || !sid || !taskIdRef.value) return
    const { summary: text } = await api.summarizeTaskSession(taskIdRef.value, sid)
    summary.value = text
  }

  return {
    kinds,
    messages,
    summary,
    title,
    loading,
    error,
    reload,
    extractTitle,
    summarize,
  }
}
