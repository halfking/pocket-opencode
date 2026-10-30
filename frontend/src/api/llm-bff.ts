/**
 * llm-bff.ts — S0-B 统一 LLM BFF API client.
 *
 * 对接后端：
 *   POST /api/llm/stream   流式 chat（SSE，OpenAI delta shape）
 *   GET  /api/llm/usage    workspace 用量汇总
 *   GET  /api/llm/quota    workspace 配额状态（budgets + strategy + enforce_mode）
 *
 * M1（2026-09-09）：流式 chat 改为委托 aiStreamRuntime。
 *   - 流所有权上移到进程级 singleton，组件 unmount 不再 abort。
 *   - 120s 看门狗：隐藏态暂停，唤醒后按剩余预算继续。
 *   - 错误带 reason：UI 层据此决定文案（用户停止 / 网络中断 / 服务端错误）。
 *   - 旧调用方接口（AbortController 返回值）保留，ctrl.abort() 行为不变（用户主动停）。
 *
 * 流式读取：fetch + ReadableStream 手动解析 SSE（EventSource 不支持 POST +
 * Authorization header）。每行 "data: {...}\n\n" 直到 "data: [DONE]"。
 */
import { useAuthStore, captureAuthSession, assertAuthSession } from '../stores/auth'
import { effectScope, watch } from 'vue'
import { assertCloudModule } from './data-policy.ts'
import { assertNotHTML } from './jsonGuard'
import { fetchWithTimeout } from './fetch-timeout.ts'
import { assertDirectPocketRequest } from './pocket-resource'

import { resolveApiBase } from '../config/api-base'
import {
  aiStreamRuntime,
  setStreamDeps,
  type ChatStreamInput,
  type ChatStreamDelta,
  type ChatStreamHandlers,
  type ChatStreamHandle,
  type SpawnFetcher,
} from '../native/aiStreamRuntime.ts'

export interface ChatMessage {
  role: 'system' | 'user' | 'assistant'
  content: string
  /** 多模态：图片附件（https: 外链或 data:image/ 内联），仅 user 消息有意义。 */
  images?: string[]
}

export type {
  ChatStreamInput,
  ChatStreamDelta,
  ChatStreamHandle,
  ChatStreamHandlers,
}

export interface UsageSummary {
  workspace_id: string
  period_start: string
  period_end: string
  total_tokens: number
  prompt_tokens: number
  completion_tokens: number
  total_cost_usd: number
  call_count: number
}

export type QuotaBudgetKind = 'tokens' | 'cost_usd' | 'calls'

export interface QuotaBudget {
  workspace_id: string
  kind: QuotaBudgetKind
  limit: number
  period_start?: string
  period_end?: string
}

export interface QuotaResponse {
  workspace_id: string
  budgets: QuotaBudget[]
  strategy: string
  enforce_mode: boolean
}

/** 注入运行时依赖（main.ts 启动时调用一次）。 */
export function installLlmBffStreamRuntime(): void {
  setStreamDeps({
    fetcher: defaultFetcher,
    resolveBase: () => resolveApiBase(),
    resolveToken: () => useAuthStore().token ?? null,
  })
}

/**
 * 默认 fetcher：负责 401 单飞续期 + HTML 兜底识别。runtime 只关心 SSE 解析。
 * 与原 streamChat 同款的"401 重放一次"语义保留在 fetcher 内部，避免在 runtime
 * 反向依赖 auth store。
 */
const defaultFetcher: SpawnFetcher = async (input, signal, base, _token) => {
  const auth = useAuthStore()
  const session = captureAuthSession(auth)
  assertDirectPocketRequest(auth)
  const { syncModule, ...body } = input
  const controller = new AbortController()
  const scope = effectScope(true)
  let reader: ReadableStreamDefaultReader<Uint8Array> | undefined
  let output: ReadableStreamDefaultController<Uint8Array> | undefined
  let ended = false
  let failure: Error | undefined
  const assertCurrent = () => {
    if (failure) throw failure
    assertAuthSession(auth, session)
    assertCloudModule(syncModule)
    if (signal.aborted) {
      const error = new Error('Stream cancelled')
      error.name = 'AbortError'
      throw error
    }
  }
  const cleanup = () => {
    scope.stop()
    signal.removeEventListener('abort', check)
    if (typeof window !== 'undefined') window.removeEventListener('openpocket:sync-policy-changed', check)
  }
  const stop = (error: unknown) => {
    failure ??= error instanceof Error ? error : new Error(String(error))
    controller.abort()
    if (reader) void reader.cancel(failure).catch(() => {})
    if (!ended && output) { ended = true; output.error(failure) }
    cleanup()
  }
  const check = () => { try { assertCurrent() } catch (error) { stop(error) } }
  assertCurrent()
  scope.run(() => {
    watch(() => [auth.token, auth.user, auth.userId, auth.workspaceId, auth.authMethod], check, { flush: 'sync' })
  })
  signal.addEventListener('abort', check, { once: true })
  if (typeof window !== 'undefined') window.addEventListener('openpocket:sync-policy-changed', check)
  const doFetch = async (): Promise<Response> => {
    assertCurrent()
    const response = await fetch(`${base}/api/llm/stream`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(auth.token ? { Authorization: `Bearer ${auth.token}` } : {}),
      },
      body: JSON.stringify(body),
      signal: controller.signal,
    })
    try { assertCurrent() } catch (error) { void response.body?.cancel().catch(() => {}); throw error }
    return response
  }
  try {
    await auth.maybeRefresh()
    assertCurrent()
    let res = await doFetch()
    if (res.status === 401) {
      void res.body?.cancel().catch(() => {})
      const refreshed = await auth.refreshSession()
      assertCurrent()
      if (refreshed) res = await doFetch()
    }
    const contentType = res.headers.get('content-type') || ''
    if (!res.ok || !res.body || contentType.toLowerCase().includes('text/html')) {
      cleanup()
      return { status: res.status, statusText: res.statusText, contentType, body: res.body, assertCurrent }
    }
    reader = res.body.getReader()
    const guardedBody = new ReadableStream<Uint8Array>({
      start(c) { output = c },
      async pull(c) {
        try {
          assertCurrent()
          const result = await reader!.read()
          assertCurrent()
          if (result.done) { ended = true; cleanup(); c.close() }
          else c.enqueue(result.value)
        } catch (error) { stop(error) }
      },
      async cancel(reason) { ended = true; cleanup(); controller.abort(); await reader!.cancel(reason) },
    })
    return { status: res.status, statusText: res.statusText, contentType, body: guardedBody, assertCurrent }
  } catch (error) {
    stop(error)
    throw failure
  }
}

export interface StreamHandlers {
  onDelta: (delta: ChatStreamDelta) => void
  onError?: (err: Error) => void
  onDone?: (finalUsage?: ChatStreamDelta['usage']) => void
  /** 可选：auto 回退重试进度帧（切到 retry 指向的候选 model）。旧调用方不传完全兼容。 */
  onRetry?: (model: string) => void
}

export const llmBffApi = {
  /**
   * 流式 chat。返回一个 handle，调用方可 `handle.abort()` 取消。
   *
   * 用法：
   *   const h = llmBffApi.streamChat({ messages, model }, { onDelta: d => append(d.content) }, 'chat:c-1')
   *   // 取消：
   *   h.abort()
   *
   * 流所有权归 runtime：本函数立刻返回；流在后台仍跑（切标签/切路由不中断）。
   *
   * streamId 可选：传入时同 id 的多次调用幂等（runtime 复用已有流，新调用方作为
   * 订阅方加入），用于"同一会话的流持有者想随时取消"。不传则每次新生成唯一 id，
   * 适合一次性 composable（usePromptOptimizer / note-search 等）。
   */
  streamChat(
    input: ChatStreamInput,
    handlers: StreamHandlers,
    streamId?: string,
  ): ChatStreamHandle {
    const auth = useAuthStore()
    const session = captureAuthSession(auth)
    let settled = false
    const fail = (error: Error) => {
      if (settled) return
      settled = true
      handlers.onError?.(error)
    }
    const forward = (callback: () => void) => {
      if (settled) return
      try { assertAuthSession(auth, session); assertCloudModule(input.syncModule) }
      catch (error) { fail(error instanceof Error ? error : new Error(String(error))); return }
      callback()
    }
    const id = streamId ?? `oneshot-${typeof crypto !== 'undefined' && 'randomUUID' in crypto ? crypto.randomUUID() : Math.random().toString(36).slice(2)}`
    return aiStreamRuntime.spawnChat(id, input, {
      onDelta: delta => forward(() => handlers.onDelta(delta)),
      onDone: usage => forward(() => { settled = true; handlers.onDone?.(usage) }),
      onError: fail,
      onRetry: model => forward(() => handlers.onRetry?.(model)),
    }, session.generation)
  },

  getUsage: (days = 7) =>
    http<UsageSummary>(`/api/llm/usage?days=${days}`),

  getQuota: () => http<QuotaResponse>('/api/llm/quota'),

  /**
   * 拉取当前网关下可用模型列表（GET /api/llm/models，由网关实时返回）。
   * 用于前端模型选择器动态填充，无需硬编码。
   */
  listModels: async (): Promise<string[]> => {
    const res = await http<{
      models: string[]
      source: string
      base_url: string
      preferred?: string[]
    }>('/api/llm/models')
    // 常用模型（设置页勾选）非空时只展示勾选集；勾选里已下线的模型保留
    // 展示（避免目录刷新后选择器突然清空），实际不存在时由网关报错。
    const preferred = res.preferred ?? []
    if (preferred.length > 0) {
      const set = new Set(preferred)
      const filtered = (res.models ?? []).filter((m) => set.has(m))
      return [...new Set([...preferred, ...filtered])]
    }
    return res.models ?? []
  },
}

// 局部 http 引用，避免循环依赖（与 ./http.ts 同款）。
async function http<T>(path: string): Promise<T> {
  const auth = useAuthStore()
  assertDirectPocketRequest(auth)
  // ⚠§951：原先是裸 fetch ⇒ 无上界。
  // ⚠ 这个局部 http 仍然保留（它是为了避开 import 环），
  //   但**超时原语**可以安全 import：./fetch-timeout 是零依赖的。
  const res = await fetchWithTimeout(`${resolveApiBase()}${path}`, {
    headers: auth.token ? { Authorization: `Bearer ${auth.token}` } : {},
  })
  if (!res.ok) throw new Error(`usage failed: ${res.status}`)
  return assertNotHTML(res).json() as Promise<T>
}
