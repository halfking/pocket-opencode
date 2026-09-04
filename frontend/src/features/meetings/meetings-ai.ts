/**
 * meetings-ai.ts — S2.2 会议纪要 AI 编排。
 *
 * 会议正文只通过 S0-B LLM BFF 处理，不落服务端；纪要结果写回本地
 * local_meetings.summary。v1 采用非流式 UI 收集流式 delta。
 */
import { llmBffApi, type ChatStreamDelta } from '../../api/llm-bff'

export async function summarizeMeeting(
  transcript: string,
  handlers: { onRetry?: (model: string) => void } = {},
): Promise<string> {
  if (!transcript.trim()) throw new Error('会议转写为空，无法生成纪要')

  const chunks: string[] = []
  // 非流式 UI 没有气泡灰字渲染位：onRetry 只记录回退轨迹，正文到达即静默，
  // 与 ai-chat 的 retry 提示语义对齐（runbook §16.6-2：全调用方接入 retry 帧）。
  const retriedModels = new Set<string>()
  await new Promise<void>((resolve, reject) => {
    llmBffApi.streamChat(
      {
        kind: 'meeting_summary',
        messages: [
          {
            role: 'system',
            content:
              '你是会议记录助手。请用中文输出结构化会议纪要，包含：会议摘要、关键决策、行动项（负责人/截止时间若能识别）、待确认问题。不要编造转写中不存在的信息。',
          },
          { role: 'user', content: transcript.slice(0, 60000) },
        ],
      },
      {
        onDelta(delta: ChatStreamDelta) {
          if (delta.content) chunks.push(delta.content)
        },
        onRetry(model: string) {
          retriedModels.add(model)
          handlers.onRetry?.(model)
        },
        onError: reject,
        onDone: () => resolve(),
      },
    )
  })
  if (chunks.length === 0 && retriedModels.size > 0) {
    throw new Error(`上游模型不可用（已重试 ${[...retriedModels].join('、')}），未返回纪要内容`)
  }
  return chunks.join('')
}
