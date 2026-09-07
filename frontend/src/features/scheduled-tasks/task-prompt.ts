export type PromptField = 'messages' | 'query' | 'prompt' | 'none'

const KIND_FIELD: Record<string, PromptField> = {
  redclaw_chat: 'messages',
  llmbff_summary: 'messages',
  redclaw_knowledge: 'query',
  agent_bridge: 'prompt',
  local_agent: 'prompt',
  cloud_dispatch: 'prompt',
}

export function promptFieldForKind(kind: string): PromptField {
  return KIND_FIELD[kind] ?? 'none'
}

export function extractPrompt(kind: string, payload: unknown): string {
  const obj = asObject(payload)
  if (!obj) return ''
  const field = promptFieldForKind(kind)
  if (field === 'query' || field === 'prompt') {
    return typeof obj[field] === 'string' ? obj[field] : ''
  }
  if (field === 'messages') {
    const messages = Array.isArray(obj.messages) ? obj.messages : []
    for (let i = messages.length - 1; i >= 0; i--) {
      const item = messages[i]
      if (item && typeof item === 'object' && typeof (item as { content?: unknown }).content === 'string') {
        return (item as { content: string }).content
      }
    }
  }
  return ''
}

export function applyPrompt(kind: string, payload: unknown, prompt: string): unknown {
  const field = promptFieldForKind(kind)
  if (field === 'none') return payload
  const obj = asObject(payload) ?? {}
  const text = prompt.trim()
  if (field === 'query' || field === 'prompt') return { ...obj, [field]: text }
  return { ...obj, messages: [{ role: 'user', content: text }] }
}

function asObject(payload: unknown): Record<string, unknown> | null {
  return payload && typeof payload === 'object' && !Array.isArray(payload)
    ? payload as Record<string, unknown>
    : null
}
