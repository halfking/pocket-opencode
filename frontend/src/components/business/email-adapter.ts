/**
 * email-adapter.ts — 把 emailsStore.LocalEmail 映射为 EmailCard 组件期望的 Email
 *
 * 字段名差异：store 用 camelCase，组件用 snake_case。
 * 字段裁剪：组件不关心 accountId / messageId / uid / aiSummary / suggestedAction / createdAt。
 */
import type { LocalEmail } from '../../features/email/emails-store'
import type { Email } from './EmailCard.vue'

export function toCardEmail(e: LocalEmail): Email {
  return {
    id: e.id,
    subject: e.subject ?? '(无主题)',
    snippet: e.snippet ?? '',
    from_name: e.fromName ?? undefined,
    from_address: e.fromAddress,
    date: e.date,
    is_read: e.isRead,
    is_starred: e.isStarred,
    category: e.category ?? undefined,
    importance: (e.importance as Email['importance']) ?? undefined,
    has_attachments: e.hasAttachments,
  }
}

export function toCardEmails(arr: LocalEmail[]): Email[] {
  return arr.map(toCardEmail)
}