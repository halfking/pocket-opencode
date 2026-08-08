/**
 * Email assistant API — multi-account IMAP aggregation, AI classification,
 * and daily summaries. See docs/2026-07-02-email-assistant-design.md.
 */
import { http } from './http'

export type EmailCategory =
  | 'work' | 'bill' | 'notification' | 'personal' | 'marketing' | 'spam'
export type EmailImportance = 'high' | 'medium' | 'low'
export type AuthType = 'password' | 'oauth2'

export interface EmailAccount {
  id: string
  displayName: string
  emailAddress: string
  imapHost: string
  imapPort: number
  smtpHost?: string
  smtpPort?: number
  authType: AuthType
  syncIntervalMin: number
  /** Unix 秒（后端 email.Account 用 int64），不是 ISO 字符串。 */
  lastSyncedAt?: number
  /** Unix 秒。 */
  createdAt?: number
  rules?: EmailRules
  enabled: boolean
}

/**
 * 凭证类字段——服务端只接收、永不回传，因此不放进 EmailAccount。
 *
 * password / oauthToken 互斥（后端会拒绝同时提供）。
 * smtpPassword 独立于 IMAP 凭证，存在单独的加密列里。
 */
export interface EmailCredentialInput {
  password?: string
  oauthToken?: string
  smtpPassword?: string
}

export interface VacationReply {
  id?: string
  accountId: string
  enabled: boolean
  startAt: number
  endAt: number
  subject: string
  bodyText: string
}

export interface EmailRules {
  whitelist?: string[]
  blacklist?: string[]
  keywords?: string[]
}

export interface Email {
  id: string
  accountId: string
  fromAddress: string
  fromName?: string
  subject: string
  snippet: string
  date: string
  isRead: boolean
  isStarred: boolean
  category?: EmailCategory
  importance?: EmailImportance
  aiSummary?: string
  suggestedAction?: string
  hasAttachments: boolean
}

export interface DailySummary {
  id: string
  summaryDate: string
  totalCount: number
  importantCount: number
  content: string
  actionItems?: { text: string; done: boolean }[]
}

export interface EmailFilter {
  accountId?: string
  category?: EmailCategory
  importance?: EmailImportance
  unreadOnly?: boolean
}

export const emailApi = {
  // Accounts
  listAccounts(): Promise<{ accounts: EmailAccount[] }> {
    return http('/api/email/accounts')
  },
  addAccount(input: Omit<EmailAccount, 'id'> & EmailCredentialInput): Promise<EmailAccount> {
    return http('/api/email/accounts', { method: 'POST', body: JSON.stringify(input) })
  },
  /**
   * 部分更新。未出现在 patch 里的字段保留原值。
   *
   * SMTP 语义（与后端 updateEmailAccount 一致）：
   *   - 只有携带 smtpHost 时后端才会写 SMTP 列；单独传 smtpPort/smtpPassword 无效。
   *   - smtpPassword 省略 → 保留原凭证；传 '' → 清空凭证；传非空 → 重新加密写入。
   */
  updateAccount(id: string, patch: Partial<EmailAccount> & EmailCredentialInput): Promise<EmailAccount> {
    return http(`/api/email/accounts/${id}`, { method: 'PUT', body: JSON.stringify(patch) })
  },
  deleteAccount(id: string): Promise<void> {
    return http(`/api/email/accounts/${id}`, { method: 'DELETE' })
  },
  testSmtp(id: string): Promise<{ ok: boolean; smtp: string }> {
    return http(`/api/email/accounts/${id}/test-smtp`, { method: 'POST', body: '{}' })
  },

  // Vacation replies (configuration only; SMTP delivery not yet implemented)
  listVacations(accountId?: string): Promise<{ vacations: VacationReply[] }> {
    const qs = accountId ? `?account_id=${encodeURIComponent(accountId)}` : ''
    return http(`/api/email/vacations${qs}`)
  },
  upsertVacation(v: VacationReply): Promise<VacationReply> {
    return http('/api/email/vacations', { method: 'POST', body: JSON.stringify(v) })
  },
  deleteVacation(id: string): Promise<{ deleted: boolean }> {
    return http(`/api/email/vacations/${id}`, { method: 'DELETE' })
  },

  // Emails
  listEmails(filter: EmailFilter = {}): Promise<{ emails: Email[] }> {
    const qs = new URLSearchParams()
    if (filter.accountId) qs.set('account_id', filter.accountId)
    if (filter.category) qs.set('category', filter.category)
    if (filter.importance) qs.set('importance', filter.importance)
    if (filter.unreadOnly) qs.set('unread', '1')
    const q = qs.toString()
    return http(`/api/emails${q ? `?${q}` : ''}`)
  },
  getEmail(id: string): Promise<Email & { body: string }> {
    return http(`/api/emails/${id}`)
  },
  patchEmail(id: string, patch: { isRead?: boolean; isStarred?: boolean }): Promise<void> {
    return http(`/api/emails/${id}`, { method: 'PATCH', body: JSON.stringify(patch) })
  },
  syncNow(accountId?: string): Promise<{ mode?: string; synced?: number; new?: number; failed?: string[] }> {
    return http('/api/emails/sync', {
      method: 'POST',
      body: JSON.stringify(accountId ? { account_id: accountId } : {}),
    })
  },

  // Daily summaries
  listSummaries(): Promise<{ summaries: DailySummary[] }> {
    return http('/api/email/summaries')
  },
  getSummary(date: string): Promise<DailySummary> {
    return http(`/api/email/summaries/${date}`)
  },
}
