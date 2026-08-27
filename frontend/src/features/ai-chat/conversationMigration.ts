/**
 * 纯函数：聊天状态恢复时把流式消息标记为「中断」。
 * 抽离到独立模块以便在 Node 测试里直接 import（避开 aiChatStore.ts 的
 * pinia/Vue 副作用）。
 */

export interface ConvLike {
  id?: string
  title?: string
  model?: string
  mode?: string
  messages?: unknown[]
  createdAt?: number
  updatedAt?: number
  archivedAt?: number
  [key: string]: unknown
}

export function migrateConversations(parsed: unknown): ConvLike[] {
  if (!Array.isArray(parsed)) return []
  for (const raw of parsed as ConvLike[]) {
    const c = raw as ConvLike
    if (!Array.isArray(c.messages)) c.messages = []
    for (const m of c.messages as Array<Record<string, unknown>>) {
      if (m && m.streaming) {
        m.streaming = false
        m.interrupted = true
      }
    }
  }
  return parsed as ConvLike[]
}
