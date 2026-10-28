import { http, LLM_CHAT_TIMEOUT_MS } from '../../api/http'

/** Shared by automatic and manually requested email-body translations. */
export async function emailTranslationChat(prompt: string): Promise<string> {
  const response = await http<{ content: string }>('/api/llm/chat', {
    syncModule: 'email',
    method: 'POST',
    body: JSON.stringify({ messages: [{ role: 'user', content: prompt }] }),
    timeoutMs: LLM_CHAT_TIMEOUT_MS,
  })
  return response.content
}
