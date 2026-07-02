/**
 * emails-store.ts — 🦞 龙虾钳子：邮箱助手本地存储
 *
 * 数据全部本地存（SQLCipher 加密）。IMAP 凭证用主密码 AES-GCM 加密。
 * 邮件分类/总结时只发 snippet（前 ~500 字）给 LLM，不发完整邮件。
 */
import { localDB } from '../../native/local-db'
import { encryptString } from '../../native/crypto'

export interface EmailAccount {
  id: string
  displayName: string
  emailAddress: string
  imapHost: string
  imapPort: number
  authType: string
  syncIntervalMin: number
  lastSyncedUid: number | null
  lastSyncedAt: number | null
  enabled: boolean
  createdAt: number
}

export interface LocalEmail {
  id: string
  accountId: string
  messageId: string | null
  uid: number | null
  fromAddress: string
  fromName: string | null
  subject: string | null
  snippet: string | null
  date: number
  isRead: boolean
  isStarred: boolean
  category: string | null
  importance: string | null
  aiSummary: string | null
  suggestedAction: string | null
  hasAttachments: boolean
  createdAt: number
}

export interface ListFilter {
  accountId?: string
  category?: string
  importance?: string
  unreadOnly?: boolean
}

// ---- 账户 ----

export async function listAccounts(): Promise<EmailAccount[]> {
  const rows = await localDB.query<any>(
    `SELECT id, display_name, email_address, imap_host, imap_port, auth_type,
            sync_interval_min, last_synced_uid, last_synced_at, enabled, created_at
     FROM local_email_accounts ORDER BY created_at`,
  )
  return rows.map(rowToAccount)
}

export async function saveAccount(input: {
  displayName: string; emailAddress: string; imapHost: string; imapPort?: number
  password: string; authType?: string; syncIntervalMin?: number
}): Promise<string> {
  const id = `acct-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  const encrypted = await encryptCredential(input.password)
  await localDB.run(
    `INSERT INTO local_email_accounts
       (id, display_name, email_address, imap_host, imap_port, auth_type, credential_encrypted,
        sync_interval_min, enabled, created_at)
     VALUES (?,?,?,?,?,?,?,?,?,?)`,
    [id, input.displayName, input.emailAddress, input.imapHost, input.imapPort ?? 993,
     input.authType ?? 'password', encrypted, input.syncIntervalMin ?? 15, 1, Date.now()],
  )
  return id
}

export async function deleteAccount(id: string): Promise<void> {
  await localDB.run('DELETE FROM local_email_accounts WHERE id = ?', [id])
}

/** 按 ID 取单个账户（EmailAccountSetup 编辑模式依赖）。 */
export async function getAccount(id: string): Promise<EmailAccount | null> {
  const row = await localDB.queryOne<{
    id: string; display_name: string; email_address: string; imap_host: string;
    imap_port: number; auth_type: string; sync_interval_min: number;
    last_synced_uid: number | null; last_synced_at: number | null;
    enabled: number; created_at: number
  }>(
    `SELECT id, display_name, email_address, imap_host, imap_port, auth_type,
            sync_interval_min, last_synced_uid, last_synced_at, enabled, created_at
     FROM local_email_accounts WHERE id = ?`,
    [id],
  )
  return row ? rowToAccount(row) : null
}

/**
 * 局部更新账户（EmailAccountSetup 编辑模式依赖）。
 * 注意：不更新 credential_encrypted —— 改密码请走专用接口。
 */
export async function updateAccount(id: string, patch: Partial<EmailAccount>): Promise<void> {
  const sets: string[] = []
  const vals: unknown[] = []
  if (patch.displayName !== undefined) { sets.push('display_name = ?'); vals.push(patch.displayName) }
  if (patch.emailAddress !== undefined) { sets.push('email_address = ?'); vals.push(patch.emailAddress) }
  if (patch.imapHost !== undefined) { sets.push('imap_host = ?'); vals.push(patch.imapHost) }
  if (patch.imapPort !== undefined) { sets.push('imap_port = ?'); vals.push(patch.imapPort) }
  if (patch.authType !== undefined) { sets.push('auth_type = ?'); vals.push(patch.authType) }
  if (patch.syncIntervalMin !== undefined) { sets.push('sync_interval_min = ?'); vals.push(patch.syncIntervalMin) }
  if (patch.enabled !== undefined) { sets.push('enabled = ?'); vals.push(patch.enabled ? 1 : 0) }
  if (sets.length === 0) return
  vals.push(id)
  await localDB.run(`UPDATE local_email_accounts SET ${sets.join(', ')} WHERE id = ?`, vals)
}

// ---- 邮件 ----

export async function listEmails(filter: ListFilter = {}): Promise<LocalEmail[]> {
  let sql = 'SELECT * FROM local_emails WHERE 1=1'
  const vals: unknown[] = []
  if (filter.accountId) { sql += ' AND account_id = ?'; vals.push(filter.accountId) }
  if (filter.category) { sql += ' AND category = ?'; vals.push(filter.category) }
  if (filter.importance) { sql += ' AND importance = ?'; vals.push(filter.importance) }
  if (filter.unreadOnly) { sql += ' AND is_read = 0' }
  sql += ' ORDER BY date DESC LIMIT 200'
  const rows = await localDB.query<any>(sql, vals)
  return rows.map(rowToEmail)
}

/** 插入/更新邮件（IMAP 抓取后调用）。返回 true=新插入。 */
export async function upsertEmail(e: Partial<LocalEmail> & { accountId: string; fromAddress: string; date: number }): Promise<boolean> {
  const id = e.id || `email-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  try {
    await localDB.run(
      `INSERT INTO local_emails
         (id, account_id, message_id, uid, from_address, from_name, subject, snippet,
          date, is_read, is_starred, category, importance, ai_summary, suggested_action,
          has_attachments, created_at)
       VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
      [id, e.accountId, e.messageId ?? null, e.uid ?? null, e.fromAddress, e.fromName ?? null,
       e.subject ?? null, e.snippet ?? null, e.date, e.isRead ? 1 : 0, e.isStarred ? 1 : 0,
       e.category ?? null, e.importance ?? null, e.aiSummary ?? null, e.suggestedAction ?? null,
       e.hasAttachments ? 1 : 0, Date.now()],
    )
    return true
  } catch {
    // UNIQUE(account_id, message_id) 冲突 = 已存在，忽略
    return false
  }
}

export async function markRead(id: string, read: boolean): Promise<void> {
  await localDB.run('UPDATE local_emails SET is_read = ? WHERE id = ?', [read ? 1 : 0, id])
}

export async function setStarred(id: string, starred: boolean): Promise<void> {
  await localDB.run('UPDATE local_emails SET is_starred = ? WHERE id = ?', [starred ? 1 : 0, id])
}

export async function setAiClassification(id: string, category: string, importance: string, summary: string, action: string): Promise<void> {
  await localDB.run(
    'UPDATE local_emails SET category = ?, importance = ?, ai_summary = ?, suggested_action = ? WHERE id = ?',
    [category, importance, summary, action, id],
  )
}

export async function updateSyncState(accountId: string, lastUid: number): Promise<void> {
  await localDB.run(
    'UPDATE local_email_accounts SET last_synced_uid = ?, last_synced_at = ? WHERE id = ?',
    [lastUid, Date.now(), accountId],
  )
}

/** 按 ID 取单封邮件（EmailDetailView 依赖）。 */
export async function getEmail(id: string): Promise<LocalEmail | null> {
  const row = await localDB.queryOne<{
    id: string; account_id: string; message_id: string | null; uid: number | null;
    from_address: string; from_name: string | null; subject: string | null;
    snippet: string | null; date: number; is_read: number; is_starred: number;
    category: string | null; importance: string | null; ai_summary: string | null;
    suggested_action: string | null; has_attachments: number; created_at: number
  }>('SELECT * FROM local_emails WHERE id = ?', [id])
  return row ? rowToEmail(row) : null
}

export async function getUnreadCount(accountId?: string): Promise<number> {
  let sql = 'SELECT COUNT(*) as cnt FROM local_emails WHERE is_read = 0'
  const vals: unknown[] = []
  if (accountId) { sql += ' AND account_id = ?'; vals.push(accountId) }
  const row = await localDB.queryOne<{ cnt: number }>(sql, vals)
  return row?.cnt ?? 0
}

// ---- WS 事件接入 ----

/** email.classified 服务器推送载荷。 */
export interface EmailClassifiedPayload {
  email_id: string
  category?: string | null
  importance?: string | null
  summary?: string | null
}

/** 视图层订阅用的字段三元组。 */
export interface EmailClassifiedFields {
  category: string | null
  importance: string | null
  summary: string | null
}

/**
 * 注册"邮件分类完成"事件的回调。
 * EmailInboxView 在 onMounted 注册，在 onUnmounted 反注册。
 */
const emailClassifiedHandlers = new Set<(emailId: string, fields: EmailClassifiedFields) => void>()

/** 注册一个 email 分类事件处理器，返回反注册函数。 */
export function registerEmailClassifiedHandler(
  cb: (emailId: string, fields: EmailClassifiedFields) => void,
): () => void {
  emailClassifiedHandlers.add(cb)
  return () => { emailClassifiedHandlers.delete(cb) }
}

/**
 * 处理 email.classified 服务器推送：
 *   - 把分类 / 重要度 / AI 摘要写回本地 SQLCipher（不动 suggested_action，
 *     那是用户行为字段，留在前端控制）
 *   - 通知所有已注册的视图层处理器更新内存列表
 *
 * 幂等：相同 email_id 重复调用，结果一致（最后一份服务器字段生效）。
 */
export async function handleClassifiedEvent(payload: EmailClassifiedPayload): Promise<void> {
  if (!payload || !payload.email_id) return

  const category = payload.category ?? null
  const importance = payload.importance ?? null
  const summary = payload.summary ?? null

  // 用 COALESCE：服务器字段为 null 时保留本地原值，避免覆盖用户手动设置的字段。
  await localDB.run(
    `UPDATE local_emails
       SET category = COALESCE(?, category),
           importance = COALESCE(?, importance),
           ai_summary = COALESCE(?, ai_summary)
     WHERE id = ?`,
    [category, importance, summary, payload.email_id],
  )

  emailClassifiedHandlers.forEach((cb) => {
    try { cb(payload.email_id, { category, importance, summary }) }
    catch (e) { console.warn('[emails-store] classified handler threw:', e) }
  })
}

// ---- 辅助 ----

function rowToAccount(r: any): EmailAccount {
  return {
    id: r.id, displayName: r.display_name, emailAddress: r.email_address,
    imapHost: r.imap_host, imapPort: r.imap_port, authType: r.auth_type,
    syncIntervalMin: r.sync_interval_min, lastSyncedUid: r.last_synced_uid,
    lastSyncedAt: r.last_synced_at, enabled: r.enabled === 1, createdAt: r.created_at,
  }
}

function rowToEmail(r: any): LocalEmail {
  return {
    id: r.id, accountId: r.account_id, messageId: r.message_id, uid: r.uid,
    fromAddress: r.from_address, fromName: r.from_name, subject: r.subject,
    snippet: r.snippet, date: r.date, isRead: r.is_read === 1, isStarred: r.is_starred === 1,
    category: r.category, importance: r.importance, aiSummary: r.ai_summary,
    suggestedAction: r.suggested_action, hasAttachments: r.has_attachments === 1,
    createdAt: r.created_at,
  }
}

/** AES-GCM 加密 IMAP 凭证（复用共享主密码派生的 key）。 */
async function encryptCredential(plain: string): Promise<string> {
  return encryptString(plain)
}
