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
import { onScopeDispose, ref, watch, type Ref } from 'vue'
import { api, type TaskSessionBundleRow, type TaskSessionMessage } from '../../api/client'
import type { SessionMsgKind } from './SessionKindFilter.vue'
import { useApiError } from '../../composables/useApiError'

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
  /** 续写草稿（「继续」预填在这里，用户可改写再发）。 */
  draft: Ref<string>
  /** 投递中。 */
  sending: Ref<boolean>
  /** 投递失败的错误文案。 */
  sendError: Ref<string>
  /** 投递成功的回执提示（含 run id 与 state）。 */
  sentHint: Ref<string>
  reload: () => Promise<void>
  extractTitle: () => Promise<void>
  summarize: () => Promise<void>
  /** 往原生会话投一条提示词；成功返回 true。 */
  send: (prompt?: string) => Promise<boolean>
}

export function useTaskSessionSheet(args: UseTaskSessionSheetArgs): UseTaskSessionSheetReturn {
  const apiError = useApiError()
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
  const draft = ref('')
  const sending = ref(false)
  const sendError = ref('')
  const sentHint = ref('')

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
    // 换会话 ⇒ 换草稿：上一个会话的半截提示词不能流到下一个会话去。
    draft.value = ''
    sendError.value = ''
    sentHint.value = ''
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
      error.value = apiError(e, 'errors.loadSessionsFailed')
      messages.value = []
    } finally {
      loading.value = false
    }
  }

  watch(
    () => [rowRef.value?.id, kinds.value.join(',')],
    () => { void reload() },
  )

  // ★★ 2026-10-27：原生会话的答复是**异步**落库的。
  //
  //   投一条提示词出去 ⇒ agent 要跑一会儿才回话 ⇒ 正文在那之前是**旧**的。
  //   抽屉原来只在「打开时」和「切换 kind 时」重读 ⇒ 用户看到的是
  //   **发出去之前的正文** ⇒ 真机 flow4 实测红在最后一条断言
  //   （`visible: "RESUMEOK"` 不可见）—— 提示词**真的投出去了**（回执
  //   「已发送」是绿的），只是回答没显示出来。
  //
  //   ⇒ 抽屉打开期间**定时重读**（5 秒一次），并在页面隐藏时停掉
  //   （省电，也免得后台空转）。
  const POLL_MS = 5000
  let pollTimer: ReturnType<typeof setInterval> | null = null
  let pollBusy = false

  async function pollOnce(): Promise<void> {
    if (pollBusy) return // 上一轮还没回来就跳过，别叠请求
    pollBusy = true
    try {
      await reload()
    } finally {
      pollBusy = false
    }
  }

  function stopPolling(): void {
    if (pollTimer !== null) {
      clearInterval(pollTimer)
      pollTimer = null
    }
  }

  function startPolling(): void {
    if (pollTimer !== null) return
    pollTimer = setInterval(() => {
      if (typeof document !== 'undefined' && document.hidden) return
      // reload() 自己吞错并写 error ⇒ 这里不会产生未处理的 rejection
      pollOnce()
    }, POLL_MS)
  }

  watch(rowRef, (row) => {
    if (row) startPolling()
    else stopPolling()
  }, { immediate: true })

  onScopeDispose(stopPolling)

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

  /**
   * 往**原生会话**投一条提示词 —— 「继续跟进」与「提示词的修改」的落点。
   *
   * ★ 2026-10-27：这条路径是必需的，不是多出来的：
   *   App 的全局会话视图（stores/session.ts 的 sendPrompt）要求 instance_id，
   *   而 ACC 派单出来的会话是 companion 原生会话（**没有** instance）
   *   ⇒ 那条路根本发不出去 ⇒ 「继续」只能走任务域的 message 端点。
   *
   * 返回 true 表示**已经投出去了**（不是"看着像发出去了"）。
   */
  async function send(prompt?: string): Promise<boolean> {
    const row = rowRef.value
    const sid = sessionIdOf(row)
    const text = (prompt ?? draft.value).trim()
    if (!row || !sid || !taskIdRef.value) return false
    if (!text) {
      sendError.value = '提示词不能为空'
      return false
    }
    sending.value = true
    sendError.value = ''
    sentHint.value = ''
    try {
      const receipt = await api.sendTaskSessionMessage(taskIdRef.value, sid, text)
      draft.value = ''
      sentHint.value = `已发送（run ${receipt.runId || '-'} · ${receipt.state || '-'}）`
      // 新消息是**异步**落库的 ⇒ 立刻重读未必读得到。
      // 但重读一次至少不会把旧正文留在屏幕上、让人以为"没发出去"。
      await reload()
      return true
    } catch (e) {
      // ★ 不能把原始错误直接上屏（check:raw-error 门禁第 26 项）：
      //   原始 message 是英文技术标识，用户看不懂也不知道该做什么。
      //   走 apiError 映射，原始信息仍然留在 console 里。
      console.error('[task-session] send failed:', e)
      sendError.value = apiError(e, '发送失败，请重试')
      return false
    } finally {
      sending.value = false
    }
  }

  return {
    kinds,
    messages,
    summary,
    title,
    loading,
    error,
    draft,
    sending,
    sendError,
    sentHint,
    reload,
    extractTitle,
    summarize,
    send,
  }
}
