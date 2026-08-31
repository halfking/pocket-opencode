/**
 * usePromptOptimizer — 输入框侧「AI 优化」草稿润色。
 *
 * 经统一 llm-bff 通道（/api/llm/stream）发送固定优化 system prompt，
 * 流式回填回调；不自动提交，用户可继续编辑。与 aiChatStore 的
 * "答案侧 optimize"（评审某条回答）不同，这里只润色草稿文本。
 */
import { ref } from 'vue'
import { llmBffApi, type ChatMessage } from '../api/llm-bff'

const OPTIMIZE_SYSTEM_PROMPT =
  '你是一个文本润色助手。用户会给你一段草稿，请在保持原意与语言（中文/英文跟随原文）的前提下，' +
  '优化表达：修正错别字与标点、理顺逻辑、去掉冗余，使它清晰、准确、可直接使用。' +
  '只输出优化后的正文，不要任何解释、前后缀或代码块包裹。'

export interface PromptOptimizerOptions {
  /** 指定模型；缺省走网关智能路由（auto）。 */
  model?: string
}

export function usePromptOptimizer(options: PromptOptimizerOptions = {}) {
  const isOptimizing = ref(false)
  const optimizeError = ref('')
  let controller: AbortController | null = null

  /**
   * 优化草稿。onDelta 流式回传累积文本；结束/出错分别回调。
   * 返回 abort 函数（组件卸载或用户编辑时取消）。
   */
  function optimize(
    draft: string,
    handlers: {
      onDelta: (accumulated: string) => void
      onDone?: (finalText: string) => void
      onError?: (err: Error) => void
    },
  ): void {
    const text = draft.trim()
    if (!text || isOptimizing.value) return
    isOptimizing.value = true
    optimizeError.value = ''
    let accumulated = ''

    const messages: ChatMessage[] = [
      { role: 'system', content: OPTIMIZE_SYSTEM_PROMPT },
      { role: 'user', content: text },
    ]
    controller = llmBffApi.streamChat(
      {
        messages,
        model: options.model || undefined,
        temperature: 0.3,
        kind: 'optimize',
      },
      {
        onDelta: (d) => {
          if (d.content) {
            accumulated += d.content
            handlers.onDelta(accumulated)
          }
        },
        onDone: () => {
          isOptimizing.value = false
          controller = null
          handlers.onDone?.(accumulated)
        },
        onError: (err) => {
          isOptimizing.value = false
          controller = null
          optimizeError.value = err.message || String(err)
          handlers.onError?.(err)
        },
      },
    )
  }

  function abort() {
    controller?.abort()
    controller = null
    isOptimizing.value = false
  }

  return { optimize, abort, isOptimizing, optimizeError }
}
