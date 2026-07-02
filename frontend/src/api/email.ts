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
  authType: AuthType
  syncIntervalMin: number
  lastSyncedAt?: string
  rules?: EmailRules
  enabled: boolean
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
  addAccount(input: Omit<EmailAccount, 'id'> & { credential: string }): Promise<EmailAccount> {
    return http('/api/email/accounts', { method: 'POST', body: JSON.stringify(input) })
  },
  updateAccount(id: string, patch: Partial<EmailAccount>): Promise<EmailAccount> {
    return http(`/api/email/accounts/${id}`, { method: 'PUT', body: JSON.stringify(patch) })
  },
  deleteAccount(id: string): Promise<void> {
    return http(`/api/email/accounts/${id}`, { method: 'DELETE' })
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
  syncNow(): Promise<void> {
    return http('/api/emails/sync', { method: 'POST' })
  },

  // Daily summaries
  listSummaries(): Promise<{ summaries: DailySummary[] }> {
    return http('/api/email/summaries')
  },
  getSummary(date: string): Promise<DailySummary> {
    return http(`/api/email/summaries/${date}`)
  },
}
