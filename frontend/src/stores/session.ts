/**
 * Session Store — 维护单个会话的实时状态。
 *
 * 数据流：
 *   1. open(sessionID, instanceID) 拉取历史消息 + 订阅 SSE
 *   2. SSE 事件驱动 message / toolCalls / reasoning 增量更新
 *   3. sendPrompt(text) POST 发送 prompt + 进入 streaming 状态
 *   4. interrupt() POST 中断 + 退出 streaming 状态
 */

import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { http } from '../api/http'
import { SessionSSEClient, type SessionEvent } from '../api/sse'

export interface TextContent {
  type: 'text'
  text: string
}

export interface ReasoningContent {
  type: 'reasoning'
  text: string
}

export interface ToolContent {
  type: 'tool'
  id: string
  name: string
  state: 'pending' | 'running' | 'completed' | 'error'
  input?: Record<string, any>
  output?: any
  error?: string
  /** Execution duration (ms); may be filled in once the tool finishes. */
  durationMs?: number
}

export type AssistantContent = TextContent | ReasoningContent | ToolContent

export interface Message {
  id: string
  role: 'user' | 'assistant' | 'system'
  /** 文本形式的简化视图（用于气泡快速展示） */
  text: string
  /** assistant 消息的结构化内容 */
  content?: AssistantContent[]
  time: number
  /** 该消息是否正在接收流式增量 */
  streaming?: boolean
}

export const useSessionStore = defineStore('session', () => {
  const sessionID = ref<string | null>(null)
  const instanceID = ref<string | null>(null)
  const title = ref<string>('')
  const status = ref<'idle' | 'streaming' | 'completed' | 'error'>('idle')
  const messages = ref<Message[]>([])
  const sseClient = ref<SessionSSEClient | null>(null)

  // 当前正在累积的 assistant 消息 ID（首次见到 text.delta 时分配）
  const currentAssistantId = ref<string | null>(null)
  const errorMessage = ref<string | null>(null)

  const isStreaming = computed(() => status.value === 'streaming')
  const lastMessage = computed(() =>
    messages.value.length ? messages.value[messages.value.length - 1] : null,
  )

  async function open(sid: string, iid: string, initialTitle?: string) {
    // M2（2026-09-09）：流所有权上移 — 同 sid+iid 已有活跃 SSE 时跳过 close()
    // 复用现有 SSE 连接；切回同一会话不会丢事件、不会重连风暴。
    const sameSession = sessionID.value === sid && instanceID.value === iid && sseClient.value
    if (!sameSession) {
      close() // 清理上一个会话（不同 sid+iid）
    } else {
      // 同会话：只清 UI 瞬态；messages / status 保持原样，reattach 会重算
      // currentAssistantId，让用户在切回时立刻看到流式进展（不闪空、不打断后台 SSE）。
      currentAssistantId.value = null
      errorMessage.value = null
    }
    sessionID.value = sid
    instanceID.value = iid
    if (initialTitle) title.value = initialTitle

    // 仅在切换到不同会话时才重置历史；同会话重入保留 messages/status，
    // 避免切回瞬间把 SSE 在后台累积的消息清掉（设计 D3：切走 ≠ 关）。
    if (!sameSession) {
      status.value = 'idle'
      messages.value = []
      errorMessage.value = null

      // 1. 拉取历史消息
      try {
        const qs = new URLSearchParams({ instance_id: iid, limit: '100' })
        const data = await http<{ messages: any[] }>(
          `/api/mobile/sessions/${encodeURIComponent(sid)}/messages?${qs}`,
        )
        // 转换消息格式
        for (const m of data.messages || []) {
          const msg = normalizeMessage(m)
          if (msg) messages.value.push(msg)
        }
      } catch (err: any) {
        errorMessage.value = `加载历史失败: ${err?.message || err}`
      }
    }

    // 2. 订阅 SSE（同 sid+iid 时跳过：sseClient 已在跑）
    if (sameSession) return

    const token = localStorage.getItem('pocket_token')
    sseClient.value = new SessionSSEClient(sid, iid, () => token, {
      onOpen: () => {
        errorMessage.value = null
      },
      onMessage: (evt) => handleEvent(evt),
      onError: (e) => {
        errorMessage.value = 'SSE 连接错误，自动重连中…'
      },
    })
    sseClient.value.open()
  }

  function normalizeMessage(raw: any): Message | null {
    if (!raw || !raw.id) return null
    const role = raw.role || (raw.type === 'user' ? 'user' : raw.type === 'assistant' ? 'assistant' : 'system')
    return {
      id: raw.id,
      role: role as Message['role'],
      text: raw.text || (raw.data?.text) || '',
      content: raw.content,
      time: raw.time?.created ? raw.time.created : Date.now(),
    }
  }

  function handleEvent(evt: SessionEvent) {
    const type = evt.type
    // pocketd 转发的是 OpenCode 信封 {id,type,location,data:{...}}；
    // 旧格式直接是扁平 payload。优先解包 data，兜底扁平。
    const data = evt.data?.data ?? evt.data

    switch (type) {
      case 'server.connected':
        // 握手
        break

      case 'session.next.prompted':
      case 'session.next.prompt.admitted':
        // 用户 prompt 被接收
        status.value = 'streaming'
        break

      case 'session.next.text.started':
      case 'session.next.reasoning.started':
        // 助手开始输出
        status.value = 'streaming'
        ensureCurrentAssistant()
        break

      case 'session.next.text.delta': {
        ensureCurrentAssistant()
        const msg = currentMessage()
        if (msg) {
          const delta = data?.delta || data?.textDelta || data?.text || ''
          if (delta) msg.text += delta
          msg.streaming = true
        }
        break
      }

      case 'session.next.text.ended':
      case 'session.next.reasoning.ended':
      case 'session.next.step.ended':
        finalizeCurrentAssistant()
        break

      case 'session.next.reasoning.delta': {
        // reasoning 暂合并到 message.text + 前缀 [思考]
        ensureCurrentAssistant()
        const msg = currentMessage()
        if (msg) {
          const delta = data?.delta || data?.textDelta || data?.text || ''
          if (delta) msg.text = (msg.text || '') + delta
          msg.streaming = true
        }
        break
      }

      case 'session.next.tool.called':
      case 'session.next.tool.progress':
      case 'session.next.tool.success':
      case 'session.next.tool.failed': {
        ensureCurrentAssistant()
        const msg = currentMessage()
        if (msg) {
          if (!msg.content) msg.content = []
          const toolName = data?.tool || data?.name || 'tool'
          const toolId = data?.id || data?.callID || toolName + '-' + Date.now()
          let existing = msg.content.find(
            (c) => c.type === 'tool' && c.id === toolId,
          ) as ToolContent | undefined
          if (!existing) {
            existing = { type: 'tool', id: toolId, name: toolName, state: 'pending' }
            msg.content.push(existing)
          }
          existing.state =
            type === 'session.next.tool.success' ? 'completed'
            : type === 'session.next.tool.failed' ? 'error'
            : 'running'
          if (data?.input) existing.input = data.input
          if (data?.output) existing.output = data.output
          if (data?.error) existing.error = data.error
        }
        break
      }

      case 'session.next.step.failed':
        status.value = 'error'
        errorMessage.value = data?.error?.message || '步骤失败'
        finalizeCurrentAssistant()
        break

      case 'session.next.context.updated':
      case 'session.next.compaction.started':
      case 'session.next.compaction.delta':
      case 'session.next.compaction.ended':
        // 暂忽略
        break

      case 'upstream.closed':
        status.value = 'idle'
        finalizeCurrentAssistant()
        break

      case 'error':
        status.value = 'error'
        errorMessage.value = data?.error || '未知错误'
        finalizeCurrentAssistant()
        break

      default:
        // 忽略未知事件
        break
    }
  }

  function ensureCurrentAssistant() {
    if (currentAssistantId.value) return
    const id = `assistant-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
    currentAssistantId.value = id
    messages.value.push({
      id,
      role: 'assistant',
      text: '',
      time: Date.now(),
      streaming: true,
    })
  }

  function currentMessage(): Message | null {
    if (!currentAssistantId.value) return null
    return messages.value.find((m) => m.id === currentAssistantId.value) || null
  }

  function finalizeCurrentAssistant() {
    const m = currentMessage()
    if (m) m.streaming = false
    currentAssistantId.value = null
    if (status.value === 'streaming') status.value = 'idle'
  }

  async function sendPrompt(text: string, agent?: string, model?: any) {
    if (!sessionID.value || !instanceID.value) return
    // 立即显示用户消息
    messages.value.push({
      id: `user-${Date.now()}`,
      role: 'user',
      text,
      time: Date.now(),
    })
    status.value = 'streaming'
    try {
      const qs = new URLSearchParams({ instance_id: instanceID.value })
      const resp = await http<{ messageID: string; sessionID: string }>(
        `/api/mobile/sessions/${encodeURIComponent(sessionID.value)}/prompt?${qs}`,
        {
          method: 'POST',
          body: JSON.stringify({ text, agent, model }),
        },
      )
      // SSE 会驱动后续 assistant 消息
      return resp
    } catch (err: any) {
      status.value = 'error'
      errorMessage.value = `发送失败: ${err?.message || err}`
      throw err
    }
  }

  async function interrupt() {
    if (!sessionID.value || !instanceID.value) return
    try {
      const qs = new URLSearchParams({ instance_id: instanceID.value })
      await http<void>(
        `/api/mobile/sessions/${encodeURIComponent(sessionID.value)}/interrupt?${qs}`,
        { method: 'POST' },
      )
      finalizeCurrentAssistant()
      status.value = 'idle'
    } catch (err: any) {
      errorMessage.value = `中断失败: ${err?.message || err}`
    }
  }

  /**
   * M2（2026-09-09）：切走当前会话页时调用，**不**关 SSE（流继续在后台跑）。
   *
   * 与 close() 的区别：
   *   - detach()：UI 瞬态清理 + 重置 SSE 句柄引用但保留 SSE 实例在内存中继续推消息。
   *     数据（sessionID / messages / status）保留；用户切回同一会话时 reattach()。
   *     实际上当前 SessionStore 是 Pinia singleton，组件 unmount 时数据本身不会被
   *     Vue 回收；detach 的语义主要为"标记 UI 不再活跃"，**不**做实质操作。
   *     SSE 实例继续推送 → messages.value 持续累积。
   *   - close()：真正关 SSE。仅在切换不同 sid+iid / 删除会话 / 登出时调用。
   *
   * 配合 useSessionEvents.stopLive / usePendingApprovals.stopPolling：
   *   - 那些钩子也应在 detach 时不再清理订阅（详见 SessionConversationView 改造）。
   *   - 但因 M1 之后 approvalsRuntime 已进程级；本页"stopPolling"现只是退订本视图的
   *     pendingPermissions 引用，SSE 仍持续被 approvalsRuntime 转发给其他订阅方。
   */
  function detach(): void {
    // intentionally no-op for M2：UI 瞬态（dismissedApprovalIds、composerInitialText
    // 等）是组件本地 ref，在 SessionConversationView.onBeforeUnmount 自己清。
    // 这里保留方法名作为契约位，便于 M3+ 真后台场景下补"通知/快照"逻辑。
  }

  /**
   * 用户切回当前会话页时调用（与 detach 配对）。
   * 重算 currentAssistantId（最后一条 streaming=true 的消息）；status 由消息状态决定。
   */
  function reattach(): void {
    if (!sessionID.value) return
    const streaming = [...messages.value].reverse().find((m) => m.streaming)
    currentAssistantId.value = streaming?.id ?? null
    if (status.value !== 'streaming' && streaming) status.value = 'streaming'
  }

  function close() {
    if (sseClient.value) {
      sseClient.value.close()
      sseClient.value = null
    }
    sessionID.value = null
    instanceID.value = null
    status.value = 'idle'
    currentAssistantId.value = null
  }

  return {
    sessionID,
    instanceID,
    title,
    status,
    messages,
    errorMessage,
    isStreaming,
    lastMessage,
    open,
    detach,
    reattach,
    close,
    sendPrompt,
    interrupt,
  }
})