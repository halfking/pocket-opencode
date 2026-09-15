/**
 * localagent/llm-stream.ts — StreamFn 的 LLM 传输适配。
 *
 * 复用 aiStreamRuntime(POST /api/llm/stream),因此 120s 看门狗、隐藏暂停、
 * 401 单飞续期、Android 前台服务保活全部继承(2026-09-09 三件套),不重复造。
 * 测试可注入 fakeSpawner,不需要真实网络。
 */

import { aiStreamRuntime, type ChatStreamHandle, type ChatStreamInput } from '../native/aiStreamRuntime.ts'
import type { StreamFn } from './types.ts'

export type ChatSpawner = (
  id: string,
  input: ChatStreamInput,
  handlers: {
    onDelta?: (delta: { content?: string; done: boolean; usage?: { prompt_tokens: number; completion_tokens: number; total_tokens: number } }) => void
    onDone?: (finalUsage?: { prompt_tokens: number; completion_tokens: number; total_tokens: number }) => void
    onError?: (err: Error, reason: string) => void
  },
) => ChatStreamHandle

export interface LlmStreamOptions {
  /** 会话 id(用于流命名);缺省匿名。 */
  sessionId?: string
  /** 指定模型;缺省由网关/preferred 解析。 */
  model?: string
  temperature?: number
  /** 替换流发起器(测试)。 */
  spawner?: ChatSpawner
  /** 流 id 前缀计数器来源(测试隔离用);缺省自增。 */
  turnCounter?: { next(): number }
}

export function createLlmStreamFn(opts: LlmStreamOptions = {}): StreamFn {
  const spawner = opts.spawner ?? ((id, input, handlers) => aiStreamRuntime.spawnChat(id, input, handlers))
  const counter = opts.turnCounter ?? { next: () => ++llmTurnSeq }

  return async function llmStream({ messages, signal, onDelta }) {
    const turn = counter.next()
    const input: ChatStreamInput = {
      messages: messages.map((m) => ({ role: m.role, content: m.content })),
      model: opts.model,
      temperature: opts.temperature,
      kind: 'agent',
    }
    const streamId = `agent-${opts.sessionId ?? 'anon'}-${turn}`

    return await new Promise<{ text: string; usage?: { promptTokens: number; completionTokens: number } }>((resolve, reject) => {
      let text = ''
      let settled = false
      let usage: { promptTokens: number; completionTokens: number } | undefined
      // 底层流句柄:外部 abort 时必须同步掐断,否则 fetch 续跑白烧 token。
      let streamHandle: ChatStreamHandle | null = null

      const onAbort = () => {
        // 外部 signal 取消:掐断底层流并 reject,免悬挂。
        streamHandle?.abort()
        if (!settled) {
          settled = true
          reject(new DOMException('Aborted', 'AbortError'))
        }
      }
      if (signal.aborted) {
        reject(new DOMException('Aborted', 'AbortError'))
        return
      }
      signal.addEventListener('abort', onAbort, { once: true })

      const settle = (fn: () => void) => {
        if (settled) return
        settled = true
        signal.removeEventListener('abort', onAbort)
        fn()
      }

      streamHandle = spawner(streamId, input, {
        onDelta: (delta) => {
          if (settled) return
          if (delta.content) {
            text += delta.content
            try {
              onDelta?.(delta.content)
            } catch {
              // 订阅方异常不拖垮收流。
            }
          }
          if (delta.usage && delta.usage.total_tokens > 0) {
            usage = { promptTokens: delta.usage.prompt_tokens, completionTokens: delta.usage.completion_tokens }
          }
        },
        onDone: (finalUsage) => {
          if (finalUsage && finalUsage.total_tokens > 0) {
            usage = { promptTokens: finalUsage.prompt_tokens, completionTokens: finalUsage.completion_tokens }
          }
          settle(() => resolve({ text, usage }))
        },
        onError: (err, reason) => {
          settle(() => {
            if (signal.aborted || reason === 'user') {
              reject(new DOMException('Aborted', 'AbortError'))
            } else {
              reject(new Error(err?.message || `LLM 流失败(${reason})`))
            }
          })
        },
      })
    })
  }
}

let llmTurnSeq = 0
