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
  lastSyncedAt?: string
  rules?: EmailRules
  enabled: boolean
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
  addAccount(input: Omit<EmailAccount, 'id'> & { password?: string; oauthToken?: string }): Promise<EmailAccount> {
    return http('/api/email/accounts', { method: 'POST', body: JSON.stringify(input) })
  },
  updateAccount(id: string, patch: Partial<EmailAccount> & { password?: string; oauthToken?: string }): Promise<EmailAccount> {
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
