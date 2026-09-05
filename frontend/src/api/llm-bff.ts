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
import { assertNotHTML } from './jsonGuard'

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
  /** 回退重试进度帧：auto 链切换候选 model 时后端下发（无 content、非终态，流会继续）。 */
  retry?: string
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
  /** 可选：auto 回退重试进度帧（切到 retry 指向的候选 model）。旧调用方不传完全兼容。 */
  onRetry?: (model: string) => void
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
      // 流级看门狗：后端 auto 回退链上游挂死时（观察过 35s+ 无任何字节），
      // 必须保证 onError 最终触发，否则 UI 永远转圈。
      const STREAM_WATCHDOG_MS = 120_000
      const watchdog = setTimeout(() => ctrl.abort(), STREAM_WATCHDOG_MS)
      try {
        // 流式路径不走共享 http()，滑动续期在此补齐：token 临期先单飞续期
        // （失败不阻塞，401 兜底还有一次重放机会）。
        try {
          await auth.maybeRefresh()
        } catch {
          // maybeRefresh 内部已吞错
        }
        const doFetch = () =>
          fetch(`${API_BASE}/api/llm/stream`, {
            method: 'POST',
            headers: {
              'Content-Type': 'application/json',
              ...(auth.token ? { Authorization: `Bearer ${auth.token}` } : {}),
            },
            body: JSON.stringify(input),
            signal: ctrl.signal,
          })
        let res = await doFetch()
        // 401 → 单飞续期一次并用新 token 重放；refresh 失败维持原 401 报错
        // （与 http.ts 的重放语义一致：每请求至多重放一次）。
        if (res.status === 401 && (await auth.refreshSession())) {
          res = await doFetch()
        }
        if (!res.ok || !res.body) {
          throw new Error(`stream failed: ${res.status} ${res.statusText}`)
        }
        // 事故形态兜底：漏注入 API base 时这里拿到的是 HTML（有 body），
        // 流解析不出任何 SSE 帧，用户只会看到"空流"。读流前先识别。
        const ct = (res.headers.get('content-type') || '').toLowerCase()
        if (ct.includes('text/html')) {
          throw new Error(
            '对话流返回了 HTML 页面而非 SSE 流：多为移动端打包漏注入 VITE_API_BASE，请检查打包配置',
          )
        }

        const reader = res.body.getReader()
        const decoder = new TextDecoder()
        let buf = ''
        let finalUsage: ChatStreamDelta['usage']
        let sawDelta = false

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
            let delta: ChatStreamDelta
            try {
              delta = JSON.parse(data) as ChatStreamDelta
            } catch (parseErr) {
              // 单帧解析失败不中断流
              console.warn('[llm-bff] bad SSE frame:', data)
              continue
            }
            // 后端错误帧形如 {"error":"...","delta":{"done":true,...}}。
            // 必须走 onError 让 UI 停止转圈并提示——不能 throw 进上面的
            // 解析 catch（历史上被当坏帧吞掉，用户只看到空气泡）。
            if (delta.error) {
              clearTimeout(watchdog)
              handlers.onError?.(new Error(delta.error))
              return
            }
            // 回退重试进度帧（形如 {"retry":"<model>"}，无 content、非终态）：
            // 交给 onRetry 透传为气泡内提示，不算坏帧也不触发 onError；不计入
            // sawDelta（纯进度不等于有正文，流若就此中断仍走空流错误）。
            // 旧调用方未传 onRetry 时静默跳过，回调契约不变。
            if (delta.retry && !delta.content && !delta.done) {
              handlers.onRetry?.(delta.retry)
              continue
            }
            if (delta.usage) finalUsage = delta.usage
            sawDelta = true
            handlers.onDelta(delta)
          }
        }
        // 流正常关闭但一帧都没有：视为错误而非静默成功（空气泡陷阱）。
        if (!sawDelta && !finalUsage) {
          handlers.onError?.(new Error('模型未返回内容（空流）'))
          return
        }
        handlers.onDone?.(finalUsage)
      } catch (err) {
        if ((err as Error).name === 'AbortError') {
          // 看门狗触发时用户并未手动取消，也要给 UI 一个终态。
          handlers.onError?.(new Error(`响应超时（${STREAM_WATCHDOG_MS / 1000}s 无响应）`))
          return
        }
        handlers.onError?.(err as Error)
      } finally {
        clearTimeout(watchdog)
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
  return assertNotHTML(res).json() as Promise<T>
}
