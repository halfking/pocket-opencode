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

/**
 * 规则动作：前端规则编辑器允许的动作集合。后端规则引擎已经支持全部五个：
 *   - mark-important / label-category：fetcher 入库时立即写入
 *   - archive / route-folder / trigger-autoreply：写入 email_action_intents 表，
 *     由后续 scheduler 消费。账户级 enable_dangerous_actions 决定是否真正执行。
 */
export type EmailRuleActionName =
  | 'mark-important'
  | 'label-category'
  | 'archive'
  | 'route-folder'
  | 'trigger-autoreply'

export interface EmailRuleActionSpec {
  name: EmailRuleActionName
  /** label-category 用：把分类名带到 emails.category。 */
  category?: string
  /** route-folder 用：目标 IMAP mailbox 名（archive / trigger-autoreply 留空）。 */
  folder?: string
}

export interface EmailRuleEntry {
  /** type: sender-whitelist | sender-blacklist | subject-keyword | domain-match | importance-min | category-match */
  type: string
  pattern: string
  /** 兼容旧字符串数组；新对象数组支持副参数。 */
  actions: (EmailRuleActionName | EmailRuleActionSpec)[]
}

export interface EmailSendInput {
  accountId?: string
  to: string[]
  subject: string
  body: string
}

export interface EmailSendResult {
  ok: boolean
  to: string[]
  from: string
}

export interface EmailBodyResult {
  emailId: string
  /** cache | imap — 缓存命中或 IMAP 实时拉取，便于前端展示刷新状态。 */
  source: 'cache' | 'imap'
  bytes: number
  body: string
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

  // Vacation replies: configuration CRUD. 投递由后端 scheduler.vacationLoop 自动消费
  // （对入站邮件按时间窗 + 幂等规则触发 SMTP 自动回复）。前端尚无配置 UI。
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
  /**
   * 完整正文懒加载：先读 dataDir/email-bodies/<id>.bin 加密缓存；未命中时按
   * 所属 account + UID 取 IMAP BODY[TEXT]，写入加密缓存后再返回。前端拿到
   // body 后可全文展示或自行做 mime 解析；不应把 body 写回本地 SQLCipher。
   */
  getEmailBody(id: string): Promise<EmailBodyResult> {
    return http(`/api/emails/${id}/body`)
  },
  patchEmail(id: string, patch: { isRead?: boolean; isStarred?: boolean }): Promise<void> {
    return http(`/api/emails/${id}`, { method: 'PATCH', body: JSON.stringify(patch) })
  },
  /**
   * 发送邮件：使用当前 user/workspace 第一个配置了 SMTP 的账户，除非显式
   * 指定 accountId。失败时返回结构化错误（status 4xx/5xx + body.message）。
   */
  sendEmail(input: EmailSendInput): Promise<EmailSendResult> {
    return http('/api/email/send', { method: 'POST', body: JSON.stringify(input) })
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
