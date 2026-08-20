/**
 * meetings-ai.ts — S2.2 会议纪要 AI 编排。
 *
 * 会议正文只通过 S0-B LLM BFF 处理，不落服务端；纪要结果写回本地
 * local_meetings.summary。v1 采用非流式 UI 收集流式 delta。
 */
import { llmBffApi, type ChatStreamDelta } from '../../api/llm-bff'

export async function summarizeMeeting(transcript: string): Promise<string> {
  if (!transcript.trim()) throw new Error('会议转写为空，无法生成纪要')

  const chunks: string[] = []
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
        onError: reject,
        onDone: () => resolve(),
      },
    )
  })
  return chunks.join('')
}
