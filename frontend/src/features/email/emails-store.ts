/**
 * emails-store.ts — 🦞 龙虾钳子：邮箱助手本地存储
 *
 * 数据全部本地存（SQLCipher 加密）。IMAP 凭证用主密码 AES-GCM 加密。
 * 邮件分类/总结时只发 snippet（前 ~500 字）给 LLM，不发完整邮件。
 */
import { localDB } from '../../native/local-db'
import { encryptString } from '../../native/crypto'
import { buildMirrorAccountWrite } from './account-mirror-write'
import { normalizeAccountStamp } from './account-lww'
import { emailDateToMs } from './cleanup-filter'

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
  /** 服务端 = SSOT 的最后修改时间；0 = 服务端未返回或本地旧版。LWW 同步：本地比服务端旧时拉服务端覆盖。 */
  updatedAt: number
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
  /**
   * AI 判重要度的依据（q2）。null = 上游没给理由。
   *
   * 与 suggestedAction 的分工：后者是「该做什么」，本字段是「为什么这么判」。
   * 判为重要时没有它，提醒就不可信——用户无法判断该不该点开。
   */
  actionReason: string | null
  hasAttachments: boolean
  createdAt: number
  updatedAt: number
  deletedAt: number
  bodyPurged: boolean
  /** 邮件所在目录（IMAP 信箱名）。空 = INBOX。 */
  folder: string
}

export interface ListFilter {
  accountId?: string
  category?: string
  importance?: string
  unreadOnly?: boolean
  uncategorized?: boolean
  /** 目录过滤：'' = 收件箱（默认）；具体目录名 = 该目录；'__all__' = 全部。 */
  folder?: string
  limit?: number
  offset?: number
}

// ---- 账户 ----

export async function listAccounts(): Promise<EmailAccount[]> {
  const rows = await localDB.query<any>(
    `SELECT id, display_name, email_address, imap_host, imap_port, auth_type,
            sync_interval_min, last_synced_uid, last_synced_at, enabled, created_at, updated_at
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
  // created_at / updated_at 用**秒**：updated_at 是 LWW 的基准，必须与服务端
  // 同单位（服务端 email_accounts.updated_at 是 Unix 秒）。此前这里写
  // Date.now()（毫秒），而读侧又不归一，导致同一列混着两种单位。
  const now = Math.floor(Date.now() / 1000)
  await localDB.run(
    `INSERT INTO local_email_accounts
       (id, display_name, email_address, imap_host, imap_port, auth_type, credential_encrypted,
        sync_interval_min, enabled, created_at, updated_at)
     VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
    [id, input.displayName, input.emailAddress, input.imapHost, input.imapPort ?? 993,
     input.authType ?? 'password', encrypted, input.syncIntervalMin ?? 15, 1, now, now],
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
    enabled: number; created_at: number; updated_at: number | null
  }>(
    `SELECT id, display_name, email_address, imap_host, imap_port, auth_type,
            sync_interval_min, last_synced_uid, last_synced_at, enabled, created_at, updated_at
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
  // 任何本地编辑都刷新 updated_at（服务端 SSOT 视角下的"已修改"）。
  const now = Math.floor(Date.now() / 1000)
  sets.push('updated_at = ?')
  vals.push(patch.updatedAt ?? now)
  vals.push(id)
  await localDB.run(`UPDATE local_email_accounts SET ${sets.join(', ')} WHERE id = ?`, vals)
}

/**
 * 写回本地账户（被 syncAccountsFromServer 使用，覆盖策略：LWW）。
 *
 * 必须字段：服务端 updatedAt > 本地 updatedAt 才覆盖（更新方是更新方），
 * 否则跳过——让本地的离线编辑继续上行至服务端时再 wins。
 */
export async function writeAccountIfNewer(acc: {
  id: string
  displayName: string
  emailAddress: string
  imapHost: string
  imapPort: number
  authType: string
  syncIntervalMin: number
  enabled: boolean
  updatedAt: number
}): Promise<boolean> {
  const local = await getAccount(acc.id)
  if (local && local.updatedAt >= acc.updatedAt) return false
  const stmt = buildMirrorAccountWrite(
    local ? { createdAt: local.createdAt } : null,
    acc,
    local?.createdAt ?? Date.now(),
  )
  await localDB.run(stmt.sql, stmt.values)
  return true
}

// ---- 邮件 ----

export async function listEmails(filter: ListFilter = {}): Promise<LocalEmail[]> {
  let sql = 'SELECT * FROM local_emails WHERE 1=1'
  const vals: unknown[] = []
  if (filter.accountId) { sql += ' AND account_id = ?'; vals.push(filter.accountId) }
  if (filter.category) { sql += ' AND category = ?'; vals.push(filter.category) }
  if (filter.importance) { sql += ' AND importance = ?'; vals.push(filter.importance) }
  if (filter.unreadOnly) { sql += ' AND is_read = 0' }
  if (filter.uncategorized) { sql += " AND (category IS NULL OR category = '')" }
  // 目录过滤与服务端同语义：'' = 收件箱（不在任何目录里）；具体名 = 该目录；
  // '__all__' = 全部。迁移到目录的邮件从收件箱视图消失、在目录视图可见。
  if (filter.folder === '__all__') { /* 不过滤 */ }
  else if (filter.folder) { sql += " AND IFNULL(folder, '') = ?"; vals.push(filter.folder) }
  else { sql += " AND IFNULL(folder, '') = ''" }
  sql += ' AND IFNULL(deleted_at, 0) = 0'
  sql += ' ORDER BY date DESC LIMIT ? OFFSET ?'
  vals.push(filter.limit ?? 200, filter.offset ?? 0)
  const rows = await localDB.query<any>(sql, vals)
  return rows.map(rowToEmail)
}

/** 插入/更新邮件（IMAP 抓取后调用）。返回 true=新插入。 */
export async function upsertEmail(e: Partial<LocalEmail> & { accountId: string; fromAddress: string; date: number }): Promise<boolean> {
  const id = e.id || `email-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  const now = Date.now()
  const updatedAt = e.updatedAt && e.updatedAt > 0 ? e.updatedAt : now
  const existing = await localDB.queryOne<{ deleted_at: number | null; body_purged: number | null; folder: string | null }>(
    'SELECT deleted_at, body_purged, folder FROM local_emails WHERE id = ?',
    [id],
  )
  if (existing && (Number(existing.deleted_at) > 0 || Number(existing.body_purged) === 1)) {
    return false
  }
  // 待同步的本地移动不能被服务端快照打回：本地已在某目录（且还有 pending 的
  // 迁移操作）时保留本地 folder，等「同步到服务器」完成后再随服务端收敛。
  let folder = e.folder ?? null
  if (existing && existing.folder) {
    const { hasPendingOpsForEmail } = await import('./email-folders-store')
    if (await hasPendingOpsForEmail(id)) folder = existing.folder
  }
  try {
    await localDB.run(
      `INSERT INTO local_emails
         (id, account_id, message_id, uid, from_address, from_name, subject, snippet,
          date, is_read, is_starred, category, importance, ai_summary, suggested_action, action_reason,
          has_attachments, created_at, updated_at, folder)
       VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
       ON CONFLICT(id) DO UPDATE SET
         subject=excluded.subject,
         snippet=excluded.snippet,
         from_name=excluded.from_name,
         category=excluded.category,
         importance=excluded.importance,
         ai_summary=excluded.ai_summary,
         suggested_action=excluded.suggested_action,
         -- COALESCE：服务端这轮没带理由时保留本地已有值。与服务端
         -- SetClassificationWithReasonScoped 的「非空才写」同一口径，
         -- 否则一次不带 reason 的同步会把已判定的依据抹掉。
         action_reason=COALESCE(excluded.action_reason, local_emails.action_reason),
         has_attachments=excluded.has_attachments,
         -- 已读/星标是本地用户操作状态：服务端不做权威回传（IMAP seen 不同步），
         -- 同步覆盖会把用户刚在详情页标记的状态抹掉，故保留本地值。
         is_read=local_emails.is_read,
         is_starred=local_emails.is_starred,
         uid=COALESCE(excluded.uid, local_emails.uid),
         message_id=COALESCE(excluded.message_id, local_emails.message_id),
         folder=COALESCE(excluded.folder, local_emails.folder),
         updated_at=excluded.updated_at`,
      [id, e.accountId, e.messageId ?? null, e.uid ?? null, e.fromAddress, e.fromName ?? null,
       e.subject ?? null, e.snippet ?? null, e.date, e.isRead ? 1 : 0, e.isStarred ? 1 : 0,
       e.category ?? null, e.importance ?? null, e.aiSummary ?? null, e.suggestedAction ?? null,
       e.actionReason ?? null,
       e.hasAttachments ? 1 : 0, now, updatedAt, folder],
    )
    return true
  } catch (err) {
    // 不能裸吞：任何 DB 错误都会被当成「不是新邮件」而静默丢弃。
    // 最典型的是 account_id 外键失败——对应账户没进 local_email_accounts
    // 时（account-sync 会按 isLocalTestAddress 跳过 *.local 账户），每一封
    // 邮件都写不进去，用户只看到「暂无邮件」，界面、接口、日志全无痕迹。
    // 真机实测就是这样静默丢了 6 封邮件。
    console.warn('[email] upsert into local mirror failed:', id, err)
    return false
  }
}

/**
 * 按 id 逐条删除本地镜像里的邮件（用户主动删除时调用）。
 *
 * 注意这是**删除**，不是同步。服务端把邮件删掉后本地并不会自动跟着删：
 * `email-cache-heal` 只检测「本地缺东西」（empty / server-ahead / stale），
 * 没有「本地比服务端多」这一路；账号被删时服务端还会
 * `email_accounts → emails → email_invoices` 三级 ON DELETE CASCADE，
 * 而本地镜像不会收到任何通知。详见 handoff round24 §25。
 */
export async function deleteEmailsByIds(ids: string[]): Promise<void> {
  for (const id of ids) {
    if (!id) continue
    await localDB.run('DELETE FROM local_emails WHERE id = ?', [id])
  }
}

export async function maxEmailUpdatedAt(): Promise<number> {
  const row = await localDB.queryOne<{ m: number | null }>(
    'SELECT MAX(updated_at) AS m FROM local_emails',
  )
  return Number(row?.m) || 0
}

/**
 * 本地未删除邮件数（墓碑行不算）。
 *
 * 缓存自愈要用它和服务端行数比大小。直接用 listEmails({limit:1}).length
 * 只能拿到「有没有」而拿不到「有多少」，判定不出服务端是否领先。
 */
export async function countLocalEmails(): Promise<number> {
  const row = await localDB.queryOne<{ c: number | null }>(
    'SELECT COUNT(*) AS c FROM local_emails WHERE IFNULL(deleted_at, 0) = 0',
  )
  return Number(row?.c) || 0
}

/**
 * 本地最新一封邮件的 `date`（毫秒），0 表示本地没有邮件。
 *
 * 判定缓存新鲜度**必须**用 date 而不是 updated_at：updated_at 会被重跑同步
 * 刷新，一封一年前的邮件重跑后 updated_at 也是今天，拿它判新鲜度会把
 * 陈旧缓存误判成「很新」，正好漏掉用户报的这类丢失。
 */
export async function newestLocalEmailDate(): Promise<number> {
  const row = await localDB.queryOne<{ m: number | null }>(
    'SELECT MAX(date) AS m FROM local_emails WHERE IFNULL(deleted_at, 0) = 0',
  )
  return Number(row?.m) || 0
}

/**
 * 从服务端增量拉取邮件并写入本地镜像。
 *
 * 增量同步协议（docs/2026-09-09-list-sync-rules.md）：
 * - since 取本地最大 updated_at，只回传变更行；
 * - 响应带 deletedIds（软删除墓碑）时同步移除本地行，做到「其他端删除、
 *   本端无刷新消失」，并防止墓碑行被再次回填；
 * - 写入路径始终先落本地 SQLite，列表随后从本地读（本地优先）。
 */
export async function syncEmailsFromServer(limit = 200, since = 0): Promise<number> {
  const r = await syncEmailsFromServerDetailed(limit, since)
  return r.inserted
}

export interface SyncEmailsResult {
  /** 本页写入本地成功的行数。 */
  inserted: number
  /** 本页收到的总行数（含重复）。 */
  received: number
  /** 本页携带的软删除墓碑数。 */
  tombstones: number
  /** 未能写入本地镜像的行数。 */
  failed: number
  /** 本页收到邮件里最早的 date（毫秒），用于回补翻页锚点。 */
  oldestDateMs: number
}

/**
 * 带明细的增量拉取。
 *
 * 回补翻页需要一个「本页实际收到的最早一封」作为锚点（见
 * email-cache-heal.nextPageSince），只返回写入条数拿不到它；同时把
 * received/failed/tombstones 分开，避免「拉到了但一条没写进去」被
 * 当成同步成功。
 */
export async function syncEmailsFromServerDetailed(limit = 200, since = 0): Promise<SyncEmailsResult> {
  const { emailApi } = await import('../../api/email')
  const res = await emailApi.listEmails({ limit, since: since > 0 ? since : undefined })
  const incoming = (res.emails ?? []).slice(0, limit)
  let n = 0
  // 「没写进去」和「本来就有」在返回值上无法区分，两者混在一起会让调用方
  // 以为同步成功。单独计数并告警，避免又变成一条查不出根因的静默路径。
  let failed = 0
  let oldestDateMs = 0
  for (const e of incoming) {
    const dateMs = emailDateToMs(typeof e.date === 'number' ? e.date : Date.parse(String(e.date)) || 0) || Date.now()
    if (oldestDateMs === 0 || dateMs < oldestDateMs) oldestDateMs = dateMs
    const ok = await upsertEmail({
      id: e.id,
      accountId: e.accountId,
      messageId: null,
      uid: null,
      fromAddress: e.fromAddress,
      fromName: e.fromName ?? null,
      subject: e.subject,
      snippet: e.snippet,
      date: dateMs,
      isRead: !!e.isRead,
      isStarred: !!e.isStarred,
      category: e.category ?? null,
      importance: e.importance ?? null,
      aiSummary: e.aiSummary ?? null,
      suggestedAction: e.suggestedAction ?? null,
      hasAttachments: !!e.hasAttachments,
      updatedAt: e.updatedAt && e.updatedAt > 0 ? e.updatedAt : dateMs,
      folder: e.folderName ?? '',
    })
    if (ok) n++
    else failed++
  }
  if (failed > 0) {
    console.warn(
      `[email] ${failed}/${incoming.length} 封未能写入本地镜像。` +
      '最常见原因是 account_id 外键失败：该账户没被写进 local_email_accounts' +
      '（account-sync 会按 isLocalTestAddress 跳过 *.local 账户），此时收件箱会一直显示为空。',
    )
  }
  const tombstones = res.deletedIds ?? []
  if (tombstones.length > 0) {
    // 与本端软删除同一语义：保留标题/摘要、清正文缓存、deleted_at 阻止回填。
    const { purgeEmailsLocal } = await import('./email-soft-delete')
    await purgeEmailsLocal(tombstones)
  }
  return { inserted: n, received: incoming.length, tombstones: tombstones.length, failed, oldestDateMs }
}

export async function markRead(id: string, read: boolean): Promise<void> {
  await localDB.run('UPDATE local_emails SET is_read = ? WHERE id = ?', [read ? 1 : 0, id])
}

export async function setStarred(id: string, starred: boolean): Promise<void> {
  await localDB.run('UPDATE local_emails SET is_starred = ? WHERE id = ?', [starred ? 1 : 0, id])
}

/** 本地记录邮件目录变更（服务端 move API 成功后调用，保持两端一致）。 */
export async function setFolder(id: string, folder: string): Promise<void> {
  await localDB.run('UPDATE local_emails SET folder = ? WHERE id = ?', [folder || '', id])
}

export async function setAiClassification(id: string, category: string, importance: string, summary: string, action: string): Promise<void> {
  await localDB.run(
    'UPDATE local_emails SET category = ?, importance = ?, ai_summary = ?, suggested_action = ? WHERE id = ?',
    [category, importance, summary, action, id],
  )
}

/**
 * 只写摘要，不动 category / importance / suggested_action。
 *
 * 手动总结（详情页「总结」按钮）只需要补 ai_summary。若复用 setAiClassification
 * 就得把现有分类原样写回，容易与并发的自动分类互相覆盖。
 */
export async function setAiSummary(id: string, summary: string): Promise<void> {
  await localDB.run(
    'UPDATE local_emails SET ai_summary = ? WHERE id = ?',
    [summary, id],
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
    suggested_action: string | null; action_reason: string | null; has_attachments: number; created_at: number
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
  /** AI 判重要度的依据（q2）。null = 服务端这轮没给理由。 */
  actionReason?: string | null
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
  // action_reason 同口径：这一轮没带理由不代表判定变了，抹掉会让已判为重要的
  // 邮件突然失去「为什么」——那正是这个字段存在的理由。
  await localDB.run(
    `UPDATE local_emails
       SET category = COALESCE(?, category),
           importance = COALESCE(?, importance),
           ai_summary = COALESCE(?, ai_summary),
           action_reason = COALESCE(?, action_reason)
     WHERE id = ?`,
    [category, importance, summary, payload.actionReason ?? null, payload.email_id],
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
    // updated_at 必须归一成秒：本列混存过毫秒（saveAccount 写 Date.now()），
    // 而它要拿去做 LWW 比较并当上行基准。单位错了会静默架空服务端守卫。
    // lastSyncedAt / createdAt 各自另有语义，**不要**一起归一。
    updatedAt: normalizeAccountStamp(r.updated_at),
  }
}

function rowToEmail(r: any): LocalEmail {
  return {
    id: r.id, accountId: r.account_id, messageId: r.message_id, uid: r.uid,
    fromAddress: r.from_address, fromName: r.from_name, subject: r.subject,
    snippet: r.snippet, date: r.date, isRead: r.is_read === 1, isStarred: r.is_starred === 1,
    category: r.category, importance: r.importance, aiSummary: r.ai_summary,
    suggestedAction: r.suggested_action, actionReason: r.action_reason ?? null,
    hasAttachments: r.has_attachments === 1,
    createdAt: r.created_at,
    updatedAt: r.updated_at ?? 0,
    deletedAt: Number(r.deleted_at) || 0,
    bodyPurged: r.body_purged === 1,
    folder: r.folder || '',
  }
}

/** AES-GCM 加密 IMAP 凭证（复用共享主密码派生的 key）。 */
async function encryptCredential(plain: string): Promise<string> {
  return encryptString(plain)
}
