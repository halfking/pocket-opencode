/**
 * Email assistant API — multi-account IMAP aggregation, AI classification,
 * and daily summaries. See docs/2026-07-02-email-assistant-design.md.
 */
import { http, LONG_REQUEST_TIMEOUT_MS } from './http'
import { assertNotHTML } from './jsonGuard'
import { resolveRuntimeApiBase } from '../config/api-base'
import { useAuthStore } from '../stores/auth'

/**
 * 邮件流水线（收信 → 清垃圾 → 重要提醒 → 发票采集 → 飞书/汇总）的客户端超时。
 *
 * 2026-10-03 审计到的缺陷：`runPipeline()` 早先**没有**传 timeoutMs，于是吃
 * 默认的 30s（http.ts 的 DEFAULT_TIMEOUT_MS）。而后端这一轮的实测耗时是
 * **1m30.67s**（见 backend/internal/server/server.go 里 longLivedPaths 的
 * 事故记录）。结果是必然的、每次都复现的：
 *
 *   - 30s 时前端 abort，报「操作失败」；
 *   - 后端毫不知情，继续跑到 1m30s 把发票行建好、推完飞书；
 *   - 用户以为没成，再点一次 → 第二个作业排队等 `emailPipelineMu`。
 *
 * 「操作成功但界面报错」和「用户重复点击」是两个后果，都比超时本身更糟。
 *
 * 取值必须**大于**后端自身的预算，否则前端会比服务端先放弃：
 *   runEmailPipeline: context.WithTimeout(ctx, 15*time.Minute)
 *   delegatePipeline: http.Client{Timeout: 16 * time.Minute}（server 模式委托）
 * 所以取 17 分钟留出余量。这条约束由
 * `__tests__/email-long-request-budget.test.mjs` 反推后端源码来守护。
 */
export const PIPELINE_TIMEOUT_MS = 17 * 60_000

/** 历史回补（按日期窗口回 IMAP 取回）的客户端超时。 */
export const BACKFILL_TIMEOUT_MS = 10 * 60_000

/**
 * 邮件归类（/api/emails/classify）的客户端超时。
 *
 * 与 PIPELINE_TIMEOUT_MS 同源的问题，但这里的账更难算，因为服务端**没有**
 * 整体超时——它的预算来自一个循环：
 *
 *   handleEmailClassify（server_email_classify.go）
 *     for 每封（默认 limit=20）:
 *       classifyViaKxmemory  context.WithTimeout(ctx, 20s)   ← 先试 kxmemory
 *       失败则回落 classifyViaGateway  context.WithTimeout(ctx, 25s)
 *
 * 所以单封最坏是 **20 + 25 = 45 秒**（kxmemory 慢/失败再走网关），20 封就是
 * **900 秒 = 15 分钟**。而这��请求原先吃的是通用的 LONG_REQUEST_TIMEOUT_MS
 * （120 秒）。
 *
 * 后果不是「慢一点」，是**静默截断**：120 秒时前端 abort，http 层断开连接，
 * 而服务端 handler 的 ctx 派生自 r.Context()，于是服务端当场被杀——按 45s/封
 * 算只能处理掉 2~3 封。剩下 17 封留在原地，用户看到的是
 * `请求超时（120s）：/api/emails/classify` 这样一条**原始技术串**，
 * 没有任何地方说「是超时，不是你没点对」。
 *
 * 这正是用户报的「邮件管理没有自动归纳整理的能力」最难查的那一种形态：
 * 链路是通的、网关是好的，只是批太大、窗口太短。
 *
 * 取 16 分钟（> 900 秒），由 __tests__/email-long-request-budget.test.mjs
 * 从后端源码反推 20s + 25s 与默认 limit 来守护。
 */
export const CLASSIFY_TIMEOUT_MS = 16 * 60_000

/**
 * 单封发票提取的客户端超时（要回 IMAP 拉原文）。
 *
 * 服务端 handleEmailInvoiceExtract **没有整体超时**——耗时全在
 * `FetchMessageRaw(r.Context(), …)` 上。后端 longLivedPaths 的事故记录里
 * 写着「命中发票但缺开票日期时，会只为这一封拉一次 IMAP 原文补日期，
 * 实测这一封就能超过 30s」。
 *
 * 原来的症状正是那行注释描述的：发票行建好了，界面却报错，用户以为没提取
 * 而反复点击。取 3 分钟，与 harvest 的 5 分钟同量级。
 */
export const INVOICE_EXTRACT_TIMEOUT_MS = 3 * 60_000

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
  /** 配置最后修改时间（Unix 秒）。LWW 同步：与服务端/本地库比新旧，新者胜。 */
  updatedAt?: number
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
  createdAt?: number
  updatedAt?: number
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
  /** cache | imap | purged — 伪删除后正文已清空，不再回源 IMAP。 */
  source: 'cache' | 'imap' | 'purged'
  bytes: number
  body: string
  purged?: boolean
}

/** /api/emails/{id}/summarize 的响应。 */
export interface EmailSummaryResult {
  emailId: string
  summary: string
  /** true = 复用已有摘要，本次没有调用 LLM。 */
  cached: boolean
}

export interface EmailClassifyResult {
  emailId: string
  category?: string
  importance?: string
  summary?: string
  error?: string
}

export interface EmailClassifyReport {
  classified: number
  remaining: number
  results: EmailClassifyResult[]
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
  /** 服务端变更时间（Unix ms）；缺省时客户端回退到 date。 */
  updatedAt?: number
  /** 邮件所在目录（IMAP 信箱名）。空/缺省 = INBOX。 */
  folderName?: string
}

// ── 自定义邮件目录 ──────────────────────────────────────────────────────

export interface EmailFolder {
  id: string
  accountId: string
  /** 完整 IMAP 信箱名（可含层级分隔符）。 */
  name: string
  displayName?: string
  /** inbox/trash/junk/sent/drafts/archive/... 空 = 普通目录。 */
  special?: string
  /** user = 本产品创建；server = IMAP LIST 发现。 */
  source?: string
  serverSynced?: boolean
  createdAt?: number
  updatedAt?: number
  /** 查询期派生字段：目录内邮件数。 */
  extra?: { emailCount?: number }
}

export interface EmailOpsEntry {
  id: string
  accountId: string
  emailId: string
  uid?: number
  action: 'move' | 'delete'
  targetFolder?: string
  subject?: string
  status: 'pending' | 'applied' | 'failed' | 'skipped'
  error?: string
  idempotencyKey?: string
  createdAt: number
  updatedAt: number
  appliedAt?: number
}

export interface EmailOpsSyncReport {
  executed: number
  applied: number
  failed: number
  skipped: number
  remaining: number
  errors?: string[]
}

export interface EmailOrganizeReport {
  dryRun?: boolean
  folder?: string
  count?: number
  ids?: string[]
  reasons?: string[]
  scanned?: number
  moved?: number
  applied?: number
  pending?: number
  errors?: string[]
}

export interface DailySummary {
  id: string
  summaryDate: string
  totalCount: number
  importantCount: number
  content: string
  actionItems?: { text: string; done: boolean }[]
  createdAt?: number
  lastUpdatedAt?: number
}

export interface EmailFilter {
  accountId?: string
  category?: EmailCategory
  importance?: EmailImportance
  unreadOnly?: boolean
  limit?: number
  /** 只拉 updatedAt > since 的变更（Unix ms）。 */
  since?: number
  /** 目录过滤：'' = 收件箱；具体目录名 = 该目录；'__all__' = 全部。 */
  folder?: string
}

export const emailApi = {
  // Accounts
  listAccounts(since = 0): Promise<{ accounts: EmailAccount[] }> {
    const qs = since > 0 ? `?since=${since}` : ''
    return http(`/api/email/accounts${qs}`)
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
  listVacations(accountId?: string, since = 0): Promise<{ vacations: VacationReply[] }> {
    const qs = new URLSearchParams()
    if (accountId) qs.set('account_id', accountId)
    if (since > 0) qs.set('since', String(since))
    const q = qs.toString()
    return http(`/api/email/vacations${q ? `?${q}` : ''}`)
  },
  upsertVacation(v: VacationReply): Promise<VacationReply> {
    return http('/api/email/vacations', { method: 'POST', body: JSON.stringify(v) })
  },
  deleteVacation(id: string): Promise<{ deleted: boolean }> {
    return http(`/api/email/vacations/${id}`, { method: 'DELETE' })
  },

  // Emails
  // 响应信封：带 since 时附 deletedIds（软删除墓碑）与 serverTimeMs（服务器时钟），
  // 客户端据此做无刷新差异合并。见 docs/2026-09-09-list-sync-rules.md §增量同步协议。
  // total 是**不受 limit 影响**的匹配总数，用来做缓存缺口判定（见 email-cache-heal）；
  // 老服务端不返回时为 undefined，调用方需退回 emails.length。
  listEmails(filter: EmailFilter = {}): Promise<{ emails: Email[]; deletedIds?: string[]; serverTimeMs?: number; total?: number }> {
    const qs = new URLSearchParams()
    if (filter.accountId) qs.set('account_id', filter.accountId)
    if (filter.category) qs.set('category', filter.category)
    if (filter.importance) qs.set('importance', filter.importance)
    if (filter.unreadOnly) qs.set('unread', '1')
    if (filter.limit) qs.set('limit', String(filter.limit))
    if (filter.since && filter.since > 0) qs.set('since', String(filter.since))
    if (filter.folder === '__all__') qs.set('folder', '__all__')
    else if (filter.folder) qs.set('folder', filter.folder)
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
  /**
   * 服务端历史回补：让服务端按**日期窗口**从 IMAP 重新拉取最近 N 天的邮件
   * 并入库（默认 30 天，见 backend/internal/email/backfill.go）。
   *
   * 为什么需要它：增量同步只按 `LastSyncedUID` 往后搜新邮件，且每轮只取最近
   * 50 封。客户端再怎么样回补 `since`，也只能补回**服务端库里已有**的行——
   * 如果那一天的邮件当初就没进过服务端库（被 50 封上限截断、或首次同步时
   * 就在窗口外），客户端无论拉多少次都补不回来。这一层是唯一能回到 IMAP
   * 源头重新取数的入口。
   *
   * 幂等：按 (account_id, message_id) 去重，重复调用安全。
   */
  backfill(opts: { accountId?: string; days?: number; maxMessages?: number } = {}, signal?: AbortSignal): Promise<{
    accounts: Array<{ accountId: string; fetched: number; saved: number; skipped: number; days: number; error?: string }>
  }> {
    return http('/api/email/backfill', {
      method: 'POST',
      // 走 IMAP 逐批取回，30 天大邮箱要跑几十秒到几分钟，不能用默认超时。
      timeoutMs: BACKFILL_TIMEOUT_MS,
      signal,
      body: JSON.stringify({
        ...(opts.accountId ? { accountId: opts.accountId } : {}),
        ...(opts.days ? { days: opts.days } : {}),
        ...(opts.maxMessages ? { maxMessages: opts.maxMessages } : {}),
      }),
    })
  },
  patchEmail(id: string, patch: { isRead?: boolean; isStarred?: boolean }): Promise<void> {
    return http(`/api/emails/${id}`, { method: 'PATCH', body: JSON.stringify(patch) })
  },
  /**
   * 对单封邮件按需生成摘要。服务端是幂等的：已有摘要会直接返回
   * （cached=true）而不再调 LLM，所以重复点击不会重复消耗 token。
   */
  summarizeEmail(id: string, signal?: AbortSignal): Promise<EmailSummaryResult> {
    return http(`/api/emails/${encodeURIComponent(id)}/summarize`, {
      method: 'POST',
      signal,
      // 总结要调 LLM，天然比普通读接口慢；给足超时再由用户中止。
      timeoutMs: LONG_REQUEST_TIMEOUT_MS,
    })
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
  /**
   * 批量归类。传入 signal 可真正中止在途请求（不只是停批间循环），
   * 对应需求「后台执行的 api 可以强行终止」。
   */
  classifyInbox(limit = 20, signal?: AbortSignal): Promise<EmailClassifyReport> {
    return http('/api/emails/classify', {
      method: 'POST',
      body: JSON.stringify({ limit }),
      signal,
      // 归类逐封调 LLM，天然慢；给足额度再由用户手动中止。
      // 注意不能用通用的 120s：服务端单封最坏 45s × 20 封 = 900s，
      // 见 CLASSIFY_TIMEOUT_MS 的注释。
      timeoutMs: CLASSIFY_TIMEOUT_MS,
    })
  },
  purgeEmails(ids: string[]): Promise<{ purged: number }> {
    return http('/api/emails/purge', {
      method: 'POST',
      body: JSON.stringify({ ids }),
    })
  },

  // ── 自定义邮件目录（同邮箱服务器能力：创建/列表/移动） ────────────────
  listFolders(accountId?: string): Promise<{ folders: EmailFolder[] }> {
    const qs = accountId ? `?account_id=${encodeURIComponent(accountId)}` : ''
    return http(`/api/email/folders${qs}`)
  },
  /** 在服务器上真实创建目录（IMAP CREATE）并登记。 */
  createFolder(accountId: string, name: string, displayName?: string): Promise<{ folder: EmailFolder }> {
    return http('/api/email/folders', {
      method: 'POST',
      body: JSON.stringify({ accountId, name, displayName }),
    })
  },
  /** 删除目录登记（目录内邮件退回收件箱视图；服务器目录本身不删）。 */
  deleteFolder(id: string): Promise<{ deleted: boolean }> {
    return http(`/api/email/folders/${encodeURIComponent(id)}`, { method: 'DELETE' })
  },
  /**
   * 移动邮件到目录（folder 传 '' = 移回收件箱）。本地立即生效并记操作日志，
   * 服务端尽力即时 IMAP MOVE；失败的操作留在日志里等 /ops/sync 重放。
   */
  moveEmails(ids: string[], folder: string): Promise<{ moved: number; applied: number; pending: number; errors?: string[]; folder?: string }> {
    return http('/api/emails/move', {
      method: 'POST',
      body: JSON.stringify({ ids, folder }),
      timeoutMs: LONG_REQUEST_TIMEOUT_MS,
    })
  },

  // ── 本地迁移操作日志 + 同步按钮 ────────────────────────────────────────
  /** 服务端操作日志（status: pending/applied/failed，空 = 全部）。 */
  listOps(status?: string, limit?: number): Promise<{ ops: EmailOpsEntry[] }> {
    const qs = new URLSearchParams()
    if (status) qs.set('status', status)
    if (limit) qs.set('limit', String(limit))
    const q = qs.toString()
    return http(`/api/emails/ops${q ? `?${q}` : ''}`)
  },
  /** 离线队列回放：把本地 pending 操作推给服务端日志（幂等键去重）。 */
  pushOps(ops: { accountId: string; emailId: string; uid?: number; action: string; targetFolder?: string; subject?: string; idempotencyKey: string }[]): Promise<{ recorded: number }> {
    return http('/api/emails/ops', { method: 'POST', body: JSON.stringify({ ops }) })
  },
  /** 同步执行：keys 传幂等键数组 = 可选同步；不传 = 全量 pending。 */
  syncOps(keys?: string[]): Promise<EmailOpsSyncReport> {
    return http('/api/emails/ops/sync', {
      method: 'POST',
      body: JSON.stringify(keys?.length ? { ids: keys } : {}),
      timeoutMs: LONG_REQUEST_TIMEOUT_MS,
    })
  },

  // ── 智能识别系统通知邮件 → 整理进目录 ─────────────────────────────────
  /** dryRun=true 只预览（返回命中的 id 与原因）；false 直接整理。 */
  organizeInbox(opts: { accountId?: string; folder?: string; dryRun?: boolean } = {}): Promise<EmailOrganizeReport> {
    return http('/api/emails/organize', {
      method: 'POST',
      body: JSON.stringify(opts),
      timeoutMs: LONG_REQUEST_TIMEOUT_MS,
    })
  },

  // Daily summaries
  listSummaries(since = 0): Promise<{ summaries: DailySummary[] }> {
    const qs = since > 0 ? `?since=${since}` : ''
    return http(`/api/email/summaries${qs}`)
  },
  getSummary(date: string): Promise<DailySummary> {
    return http(`/api/email/summaries/${date}`)
  },

  // ── 发票自动整理 ──────────────────────────────────────────────────────
  // 后端规则提取（subject/snippet/缓存正文），分类为 bill 的邮件同步后自动提取；
  // 这里提供列表/手动提取/归档/删除 + 文件采集/导出/推送。
  listInvoices(status?: EmailInvoiceStatus, limit?: number, offset?: number, since?: number): Promise<EmailInvoiceListResult> {
    const qs = new URLSearchParams()
    if (status) qs.set('status', status)
    if (limit) qs.set('limit', String(limit))
    if (offset) qs.set('offset', String(offset))
    if (since && since > 0) qs.set('since', String(since))
    const q = qs.toString()
    return http(`/api/emails/invoices${q ? `?${q}` : ''}`)
  },
  /**
   * 手动对单封邮件做发票提取。
   *
   * 2026-10-03：这里原先没传 timeoutMs，吃默认 30s。而服务端
   * handleEmailInvoiceExtract **根本没有设整体超时**——它的耗时全在
   * `emailFetcher.FetchMessageRaw(r.Context(), …)` 回 IMAP 拉原文上，
   * 后端 longLivedPaths 的事故记录里写着「实测单封就能超过 30s」。
   *
   * 后果是仓库里早就记过的那一幕：**发票行建好了，界面却报错**，用户以为
   * 没提取而反复点击。取 3 分钟，与 harvest 的 5 分钟同量级。
   */
  extractInvoice(emailId: string, signal?: AbortSignal): Promise<EmailInvoiceExtractResult> {
    return http('/api/emails/invoices/extract', {
      method: 'POST',
      body: JSON.stringify({ emailId }),
      signal,
      timeoutMs: INVOICE_EXTRACT_TIMEOUT_MS,
    })
  },
  setInvoiceStatus(id: string, status: EmailInvoiceStatus): Promise<void> {
    return http(`/api/emails/invoices/${id}`, { method: 'PATCH', body: JSON.stringify({ status }) })
  },
  deleteInvoice(id: string): Promise<void> {
    return http(`/api/emails/invoices/${id}`, { method: 'DELETE' })
  },
  /**
   * 下载单张已采集发票 PDF（带鉴权的 blob；配合 utils/download.downloadFile
   * 静默落盘到系统「下载」目录）。
   *
   * 必须走 resolveRuntimeApiBase() 拼绝对地址：APK 里页面 origin 是
   * https://localhost，相对路径 /api/* 会被 Capacitor WebView 的本地资源服务
   * 兜底成 index.html（200, text/html）。真机实测（2026-10-01）：写出去的
   * 「PDF」其实是 <!doctype html>，PdfRenderer 报 "file not in PDF format"，
   * 旧实现还会把这份 HTML 当发票交给系统分享面板。
   */
  async fetchInvoiceFile(id: string): Promise<Blob> {
    const auth = useAuthStore()
    const res = await fetch(`${resolveRuntimeApiBase()}/api/emails/invoices/${encodeURIComponent(id)}/file`, {
      headers: auth.token ? { Authorization: `Bearer ${auth.token}` } : undefined,
    })
    if (!res.ok) throw new Error(`下载失败（${res.status}）`)
    assertNotHTML(res)
    return res.blob()
  },
  async fetchInvoiceThumb(id: string): Promise<Blob> {
    const auth = useAuthStore()
    const res = await fetch(`${resolveRuntimeApiBase()}/api/emails/invoices/${encodeURIComponent(id)}/thumb`, {
      headers: auth.token ? { Authorization: `Bearer ${auth.token}` } : undefined,
    })
    if (!res.ok) throw new Error(`缩略图不可用（${res.status}）`)
    assertNotHTML(res)
    return res.blob()
  },
  /** 合并导出 A4 网格 PDF（grid=2 → 2x2 每页 4 张；3 → 3x3 每页 9 张）。 */
  exportInvoicesGrid(ids: string[], grid: 2 | 3): Promise<EmailInvoiceExportResult> {
    return http('/api/emails/invoices/export', {
      method: 'POST',
      body: JSON.stringify({ ids, grid }),
    })
  },
  async fetchInvoiceExport(file: string): Promise<Blob> {
    const auth = useAuthStore()
    const res = await fetch(
      `${resolveRuntimeApiBase()}/api/emails/invoices/export/download?file=${encodeURIComponent(file)}`,
      { headers: auth.token ? { Authorization: `Bearer ${auth.token}` } : undefined },
    )
    if (!res.ok) throw new Error(`下载失败（${res.status}）`)
    assertNotHTML(res)
    return res.blob()
  },
  /** 推送发票到飞书；ids 省略 = 全部已下载未推送。失败回退共享汇总文档。 */
  pushInvoicesToFeishu(ids?: string[]): Promise<EmailInvoicePushResult> {
    return http('/api/emails/invoices/push', {
      method: 'POST',
      body: JSON.stringify(ids ? { ids } : {}),
    })
  },
  /** 共享汇总清单（CSV + Markdown 文档路径 + 行数据 + 合计金额）。 */
  invoiceSummary(): Promise<EmailInvoiceSummary> {
    return http('/api/emails/invoices/summary')
  },

  // ── 邮件处理流水线 ──────────────────────────────────────────────────
  /**
   * 手动触发一轮：收信 → 清理垃圾 → 重要提醒 → 发票采集 → 飞书/汇总。
   *
   * 传 signal 才能真正中止（需求「后台执行的 api 可以强行终止」）：服务端
   * handler 把 r.Context() 一路传进 `context.WithTimeout(ctx, 15*time.Minute)`，
   * 客户端断开即中止。
   */
  runPipeline(signal?: AbortSignal): Promise<EmailPipelineReport> {
    return http('/api/email/pipeline/run', {
      method: 'POST',
      body: '{}',
      signal,
      // 必须给足：后端实测 1m30s，默认 30s 会让每次都「假失败」。
      timeoutMs: PIPELINE_TIMEOUT_MS,
    })
  },
}

/** 从邮件提取出的结构化发票/账单记录（对齐后端 email.Invoice）。 */
export type EmailInvoiceStatus = 'new' | 'pending' | 'downloaded' | 'failed' | 'filed'

export interface EmailInvoice {
  id: string
  emailId: string
  accountId: string
  kind: string // e-invoice | vat-special | paper | receipt | bill
  category: string // 餐饮 | 交通 | 住宿 | 通信 | 办公 | 其他
  title: string
  seller: string
  amount: number
  /** 后端 omitempty，运行时可能缺省（缺省视为 CNY） */
  currency?: string
  invoiceNo?: string
  invoiceDate?: string
  /** 来源邮件收到时间（Unix 秒）。列表按它倒排。 */
  emailDate?: number
  subject: string
  status: EmailInvoiceStatus
  extractedBy: 'rule' | 'llm'
  createdAt: number
  updatedAt: number
  /** 文件采集产物：规范名 {费用类型}-{对方单位}-{金额}-{日期}.pdf。 */
  fileName?: string
  filePath?: string
  /** attachment=邮件附件 | pdf-url=正文链接直下 | xml-render=XML 解析重渲染 */
  fileSource?: string
  /** 下载尝试次数（部分平台需多次操作，pending 时由流水线自动重试）。 */
  attempts?: number
  lastError?: string
  /** Unix 秒；进入 A4 网格导出的最近时间。 */
  exportedAt?: number
  /** Unix 秒；>0 = 已推送飞书。 */
  feishuSentAt?: number
  /** 本地未回推的改动（归档等）。 */
  dirty?: boolean
  /** 对齐前的本地临时 id。 */
  clientId?: string
}

export interface EmailInvoiceListResult {
  invoices: EmailInvoice[]
  total: number
  filed: number
  amount: number
  hasMore?: boolean
  offset?: number
}

export interface EmailInvoiceExtractResult {
  matched: boolean
  message?: string
  invoice?: EmailInvoice
}

export interface EmailInvoiceExportResult {
  file: string
  count: number
  grid: number
  url: string
}

export interface EmailInvoicePushResult {
  pushed: number
  failed: number
  errors?: string[]
  shareDocCsv?: string
  shareDocMd?: string
  /** 飞书共享台账（电子表格）链接；飞书未配置/未授权时为空。 */
  shareDocUrl?: string
  ledgerError?: string
  message?: string
}

export interface EmailInvoiceSummary {
  count: number
  amountTotal: number
  downloaded: number
  pending: number
  failed: number
  rows: {
    id: string
    category: string
    seller: string
    amount: number
    currency?: string
    invoiceNo: string
    invoiceDate: string
    status: EmailInvoiceStatus
    fileName: string
    feishuSent: boolean
  }[]
  shareDocCsv?: string
  shareDocMd?: string
  /** 飞书共享台账链接（别人可打开的电子表格，含清单与合计）。 */
  shareDocUrl?: string
}

/** 一轮流水线的执行报告（对齐后端 email.PipelineReport）。 */
export interface EmailPipelineReport {
  startedAt: number
  finishedAt: number
  durationMs: number
  accountsSynced: number
  newEmails: number
  spamMoved: number
  spamLocalOnly: number
  remindersSent: number
  invoices: {
    processed: number
    downloaded: number
    pending: number
    failed: number
    skipped: number
  }
  feishuPushed: number
  feishuFailed: number
  shareDocCsv?: string
  shareDocMd?: string
  /**
   * 飞书共享台账链接。
   *
   * 后端 PipelineReport 自带 shareDocUrl（未配置飞书时省略），但这个类型
   * 早于该字段加上：不声明的话定时跑产出的链接会被静默丢弃，只有手动推送
   * 那条路径（走 EmailInvoicePushResult）才可能填上界面上的芯片。
   */
  shareDocUrl?: string
  errors?: string[]
}

