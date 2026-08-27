/**
 * llm-bff.ts — S0-B 统一 LLM BFF API client.
 *
 * 对接后端：
 *   POST /api/llm/stream   流式 chat（SSE，OpenAI delta shape）
 *   GET  /api/llm/usage    workspace 用量汇总
 *   GET  /api/llm/quota    workspace 配额状态（budgets + strategy + enforce_mode）
 *
 * 流式读取：fetch + ReadableStream 手动解析 SSE（EventSource 不支持 POST +
 * Authorization header）。每行 "data: {...}\n\n" 直到 "data: [DONE]"。
 */
import { useAuthStore } from '../stores/auth'

const API_BASE = import.meta.env.VITE_API_BASE || ''

export interface ChatMessage {
  role: 'system' | 'user' | 'assistant'
  content: string
  /** 多模态：图片附件（https: 外链或 data:image/ 内联），仅 user 消息有意义。 */
  images?: string[]
}

export interface ChatStreamDelta {
  content?: string
  done: boolean
  finish_reason?: string
  usage?: {
    prompt_tokens: number
    completion_tokens: number
    total_tokens: number
  }
  error?: string
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

export interface StreamHandlers {
  onDelta: (delta: ChatStreamDelta) => void
  onError?: (err: Error) => void
  onDone?: (finalUsage?: ChatStreamDelta['usage']) => void
}

export const llmBffApi = {
  /**
   * 流式 chat。返回一个 abort controller，调用方可取消。
   *
   * 用法：
   *   const ctrl = llmBffApi.streamChat({ messages, model }, { onDelta: d => append(d.content) })
   *   // 取消：
   *   ctrl.abort()
   */
  streamChat(
    input: {
      messages: ChatMessage[]
      model?: string
      temperature?: number
      max_tokens?: number
      kind?: string
    },
    handlers: StreamHandlers,
  ): AbortController {
    const ctrl = new AbortController()
    const auth = useAuthStore()

   ;(async () => {
      try {
        const res = await fetch(`${API_BASE}/api/llm/stream`, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            ...(auth.token ? { Authorization: `Bearer ${auth.token}` } : {}),
          },
          body: JSON.stringify(input),
          signal: ctrl.signal,
        })
        if (!res.ok || !res.body) {
          throw new Error(`stream failed: ${res.status} ${res.statusText}`)
        }

        const reader = res.body.getReader()
        const decoder = new TextDecoder()
        let buf = ''
        let finalUsage: ChatStreamDelta['usage']

        while (true) {
          const { done, value } = await reader.read()
          if (done) break
          buf += decoder.decode(value, { stream: true })

          // 按行处理 SSE：行间以 "\n\n" 分隔
          let nl: number
          while ((nl = buf.indexOf('\n\n')) >= 0) {
            const chunk = buf.slice(0, nl)
            buf = buf.slice(nl + 2)
            if (!chunk.startsWith('data: ')) continue
            const data = chunk.slice(6)
            if (data === '[DONE]') {
              handlers.onDone?.(finalUsage)
              return
            }
            try {
              const delta = JSON.parse(data) as ChatStreamDelta
              if (delta.error) {
                throw new Error(delta.error)
              }
              if (delta.usage) finalUsage = delta.usage
              handlers.onDelta(delta)
            } catch (parseErr) {
              // 单帧解析失败不中断流
              console.warn('[llm-bff] bad SSE frame:', data)
            }
          }
        }
        handlers.onDone?.(finalUsage)
      } catch (err) {
        if ((err as Error).name === 'AbortError') return
        handlers.onError?.(err as Error)
      }
    })()

    return ctrl
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
  const res = await fetch(`${API_BASE}${path}`, {
    headers: auth.token ? { Authorization: `Bearer ${auth.token}` } : {},
  })
  if (!res.ok) throw new Error(`usage failed: ${res.status}`)
  return res.json() as Promise<T>
}
