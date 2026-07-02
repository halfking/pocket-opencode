/**
 * chat-store.ts — 🦞 龙虾钳子：聊天聚合本地存储（Phase 6B）
 *
 * 三路消息（微信/SMS/飞书/Telegram）抓取后统一本地存。
 * 总结/回复建议发片段给 LLM，不发完整对话。
 */
import { localDB } from '../../native/local-db'

export interface ChatConversation {
  id: string
  source: string // wechat / sms / feishu / telegram
  name: string | null
  lastMessageAt: number | null
  unreadCount: number
  createdAt: number
}

export interface ChatMessage {
  id: string
  source: string
  conversationId: string
  sender: string | null
  text: string
  ts: number
  isOutgoing: boolean
}

export async function listConversations(source?: string, limit = 50): Promise<ChatConversation[]> {
  let sql = 'SELECT * FROM local_chat_conversations WHERE 1=1'
  const vals: unknown[] = []
  if (source) { sql += ' AND source = ?'; vals.push(source) }
  sql += ' ORDER BY last_message_at DESC NULLS LAST LIMIT ?'
  vals.push(limit)
  const rows = await localDB.query<any>(sql, vals)
  return rows.map(rowToConversation)
}

export async function upsertConversation(c: { id: string; source: string; name?: string }): Promise<void> {
  await localDB.run(
    `INSERT INTO local_chat_conversations (id, source, name, last_message_at, unread_count, created_at)
     VALUES (?,?,?,?,?,?)
     ON CONFLICT(id) DO UPDATE SET name = EXCLUDED.name`,
    [c.id, c.source, c.name ?? null, null, 0, Date.now()],
  )
}

export async function saveMessage(m: Omit<ChatMessage, 'id'>): Promise<string> {
  const id = `msg-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  await localDB.run(
    `INSERT INTO local_chat_messages (id, source, conversation_id, sender, text, ts, is_outgoing, created_at)
     VALUES (?,?,?,?,?,?,?,?)`,
    [id, m.source, m.conversationId, m.sender, m.text, m.ts, m.isOutgoing ? 1 : 0, Date.now()],
  )
  // 更新会话最后消息时间
  await localDB.run(
    'UPDATE local_chat_conversations SET last_message_at = ? WHERE id = ?',
    [m.ts, m.conversationId],
  )
  return id
}

export async function getMessages(conversationId: string, limit = 100): Promise<ChatMessage[]> {
  const rows = await localDB.query<any>(
    'SELECT * FROM local_chat_messages WHERE conversation_id = ? ORDER BY ts ASC LIMIT ?',
    [conversationId, limit],
  )
  return rows.map(rowToMessage)
}

export async function markRead(conversationId: string): Promise<void> {
  await localDB.run(
    'UPDATE local_chat_conversations SET unread_count = 0 WHERE id = ?',
    [conversationId],
  )
}

function rowToConversation(r: any): ChatConversation {
  return {
    id: r.id, source: r.source, name: r.name, lastMessageAt: r.last_message_at,
    unreadCount: r.unread_count ?? 0, createdAt: r.created_at,
  }
}

function rowToMessage(r: any): ChatMessage {
  return {
    id: r.id, source: r.source, conversationId: r.conversation_id, sender: r.sender,
    text: r.text, ts: r.ts, isOutgoing: r.is_outgoing === 1,
  }
}
