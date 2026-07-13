/**
 * llm-bff.ts — S0-B 统一 LLM BFF API client.
 *
 * 对接后端：
 *   POST /api/llm/stream   流式 chat（SSE，OpenAI delta shape）
 *   GET  /api/llm/usage    workspace 用量汇总
 *
 * 流式读取：fetch + ReadableStream 手动解析 SSE（EventSource 不支持 POST +
 * Authorization header）。每行 "data: {...}\n\n" 直到 "data: [DONE]"。
 */
import { useAuthStore } from '../stores/auth'

const API_BASE = import.meta.env.VITE_API_BASE || ''

export interface ChatMessage {
  role: 'system' | 'user' | 'assistant'
  content: string
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
