/**
 * email-folders-store.ts — 自定义邮件目录 + 本地迁移操作日志的本地存储。
 *
 * 目录：服务端是 SSOT（email_folders + emails.folder_name），本地镜像供
 * 离线浏览；列表同步时按 updated_at 全量刷新（目录数量少，不做增量）。
 *
 * 操作日志：用户在本地把邮件移目录/删除时，除即时调用服务端 API 外，同一
 * 操作写进 local_email_ops（status=pending）。「同步到服务器」按钮把 pending
 * 推给 /api/emails/ops（幂等键去重，重放安全）再触发 /api/emails/ops/sync，
 * 由服务端经 IMAP 真正 MOVE / 移入垃圾箱；成功后本地置 applied。
 */
import { localDB } from '../../native/local-db'
import { emailApi } from '../../api/email'

export type { LocalFolder, LocalOpsEntry } from './email-folders-model'
import {
  folderDisplayName,
  mapServerFolder,
  opsIdempotencyKey,
  type LocalFolder,
  type LocalOpsEntry,
} from './email-folders-model'

// ---- 目录 ----

export async function listLocalFolders(accountId?: string): Promise<LocalFolder[]> {
  let sql = 'SELECT * FROM local_email_folders'
  const vals: unknown[] = []
  if (accountId) { sql += ' WHERE account_id = ?'; vals.push(accountId) }
  sql += ' ORDER BY created_at, name'
  const rows = await localDB.query<any>(sql, vals)
  return rows.map(rowToFolder)
}

export async function upsertLocalFolder(f: LocalFolder): Promise<void> {
  await localDB.run(
    `INSERT INTO local_email_folders
       (id, account_id, name, display_name, special, source, server_synced, email_count, created_at, updated_at)
     VALUES (?,?,?,?,?,?,?,?,?,?)
     ON CONFLICT(account_id, name) DO UPDATE SET
       display_name=excluded.display_name,
       special=excluded.special,
       source=excluded.source,
       server_synced=MAX(local_email_folders.server_synced, excluded.server_synced),
       email_count=excluded.email_count,
       updated_at=excluded.updated_at`,
    [f.id, f.accountId, f.name, f.displayName, f.special, f.source, f.serverSynced ? 1 : 0,
     f.emailCount, f.createdAt, f.updatedAt],
  )
}

export async function deleteLocalFolder(id: string): Promise<void> {
  await localDB.run('DELETE FROM local_email_folders WHERE id = ?', [id])
}

/** 从服务端刷新目录镜像（数量小，全量覆盖 email_count；服务端没有的本地行保留）。 */
export async function syncFoldersFromServer(accountId?: string): Promise<LocalFolder[]> {
  const { folders } = await emailApi.listFolders(accountId)
  const now = Date.now()
  for (const f of folders) {
    await upsertLocalFolder(mapServerFolder(f, now))
  }
  return listLocalFolders(accountId)
}

export async function createFolder(accountId: string, name: string, displayName?: string): Promise<void> {
  await emailApi.createFolder(accountId, name, displayName)
  await syncFoldersFromServer(accountId)
}

/** 删除目录：先退回收件箱视图（本地 folder 清空），再删镜像与服务端登记。 */
export async function removeFolder(folder: LocalFolder): Promise<void> {
  await localDB.run('UPDATE local_emails SET folder = ? WHERE folder = ?', ['', folder.name])
  await deleteLocalFolder(folder.id)
  try { await emailApi.deleteFolder(folder.id) } catch { /* 镜像已删，服务端下次同步收敛 */ }
}

// ---- 操作日志 ----

function rowToFolder(r: any): LocalFolder {
  return {
    id: r.id, accountId: r.account_id, name: r.name,
    displayName: r.display_name || folderDisplayName(r.name),
    special: r.special || '', source: r.source || 'user',
    serverSynced: r.server_synced === 1, emailCount: r.email_count ?? 0,
    createdAt: r.created_at, updatedAt: r.updated_at,
  }
}

function rowToOps(r: any): LocalOpsEntry {
  return {
    id: r.id, accountId: r.account_id, emailId: r.email_id, uid: r.uid ?? 0,
    action: r.action, targetFolder: r.target_folder || '', subject: r.subject || '',
    status: r.status, error: r.error || '', createdAt: r.created_at, updatedAt: r.updated_at,
  }
}

/**
 * 记录一条本地迁移操作。id 按 (email, action, target) 幂等：同一封邮件重复
 * 移动只保留最新目标（覆盖 pending 行），已推服务端的行不再翻回 pending。
 */
export async function recordOpsEntry(entry: Omit<LocalOpsEntry, 'id' | 'status' | 'error' | 'createdAt' | 'updatedAt'>): Promise<void> {
  const now = Date.now()
  const existing = await localDB.queryOne<{ id: string; status: string }>(
    'SELECT id, status FROM local_email_ops WHERE email_id = ? AND action = ?',
    [entry.emailId, entry.action],
  )
  if (existing) {
    if (existing.status === 'pending' || existing.status === 'failed') {
      await localDB.run(
        'UPDATE local_email_ops SET target_folder = ?, subject = ?, uid = ?, status = ?, error = ?, updated_at = ? WHERE id = ?',
        [entry.targetFolder, entry.subject, entry.uid, 'pending', '', now, existing.id],
      )
      return
    }
    // pushed/applied：服务端已有一条同键记录（幂等键相同，服务端不会重复执行），
    // 目标若变了才需要再记一条新的 pending。
    if (entry.action === 'move') {
      const prev = await localDB.queryOne<{ target_folder: string }>('SELECT target_folder FROM local_email_ops WHERE id = ?', [existing.id])
      if (prev && prev.target_folder === entry.targetFolder) return
    } else {
      return
    }
  }
  const id = `ops-${now}-${Math.random().toString(36).slice(2, 8)}`
  await localDB.run(
    `INSERT INTO local_email_ops (id, account_id, email_id, uid, action, target_folder, subject, status, error, created_at, updated_at)
     VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
    [id, entry.accountId, entry.emailId, entry.uid, entry.action, entry.targetFolder, entry.subject,
     'pending', '', now, now],
  )
}

export async function listOpsEntries(status?: LocalOpsEntry['status']): Promise<LocalOpsEntry[]> {
  let sql = 'SELECT * FROM local_email_ops'
  const vals: unknown[] = []
  if (status) { sql += ' WHERE status = ?'; vals.push(status) }
  sql += ' ORDER BY created_at DESC LIMIT 200'
  const rows = await localDB.query<any>(sql, vals)
  return rows.map(rowToOps)
}

export async function countPendingOps(): Promise<number> {
  const row = await localDB.queryOne<{ cnt: number }>('SELECT COUNT(*) AS cnt FROM local_email_ops WHERE status IN (?, ?)', ['pending', 'failed'])
  return row?.cnt ?? 0
}

/** 该邮件是否还有未同步到服务器的本地迁移操作（同步快照回写时的保护伞）。 */
export async function hasPendingOpsForEmail(emailId: string): Promise<boolean> {
  const row = await localDB.queryOne<{ cnt: number }>(
    'SELECT COUNT(*) AS cnt FROM local_email_ops WHERE email_id = ? AND status IN (?, ?)',
    [emailId, 'pending', 'failed'],
  )
  return (row?.cnt ?? 0) > 0
}

export interface FlushOpsReport {
  pushed: number
  applied: number
  failed: number
  remaining: number
  errors: string[]
}

/**
 * 同步按钮的执行体：pending → 服务端日志（幂等）→ 服务端 IMAP 执行 → 本地状态回写。
 * 任一步失败都不丢操作：失败行留在本地等下次同步。
 */
export async function flushEmailOps(opts: { ids?: string[] } = {}): Promise<FlushOpsReport> {
  const report: FlushOpsReport = { pushed: 0, applied: 0, failed: 0, remaining: 0, errors: [] }
  const all = await listOpsEntries()
  let batch = all.filter((o) => o.status === 'pending' || o.status === 'failed')
  if (opts.ids?.length) {
    const want = new Set(opts.ids)
    batch = batch.filter((o) => want.has(o.id))
  }
  if (batch.length === 0) {
    report.remaining = await countPendingOps()
    return report
  }
  // 1) 推服务端日志（幂等；重复推送只是被去重）。
  try {
    await emailApi.pushOps(batch.map((o) => ({
      accountId: o.accountId, emailId: o.emailId, uid: o.uid, action: o.action,
      targetFolder: o.targetFolder, subject: o.subject,
      idempotencyKey: opsIdempotencyKey(o),
    })))
  } catch (e: any) {
    report.errors.push(String(e?.message || e))
    report.remaining = batch.length
    return report // 网络不通：全部保持 pending
  }
  const now = Date.now()
  for (const o of batch) {
    await localDB.run('UPDATE local_email_ops SET status = ?, updated_at = ? WHERE id = ?', ['pushed', now, o.id])
  }
  report.pushed = batch.length
  // 2) 服务端执行（可选：只执行刚推的这一批）。
  try {
    const rep = await emailApi.syncOps(batch.map((o) => opsIdempotencyKey(o)))
    report.applied = rep.applied ?? 0
    report.failed = rep.failed ?? 0
    report.errors = rep.errors ?? []
    report.remaining = rep.remaining ?? 0
  } catch (e: any) {
    report.errors.push(String(e?.message || e))
    report.remaining += batch.length
    return report
  }
  // 3) 本地回写：服务端 applied/failed 的统计按批次聚合，逐条无法对应时以
  // 「批次成功与否」近似——全无错误则全部 applied，否则保持 pushed 等下次对账。
  const done = report.failed === 0 && report.errors.length === 0
  for (const o of batch) {
    await localDB.run('UPDATE local_email_ops SET status = ?, error = ?, updated_at = ? WHERE id = ?',
      [done ? 'applied' : 'pushed', done ? '' : (report.errors[0] || ''), Date.now(), o.id])
  }
  return report
}

/** 服务端日志里已终结的行，本地对应置 applied（对账）。 */
export async function reconcileOpsFromServer(): Promise<void> {
  const { ops } = await emailApi.listOps('applied', 200)
  const doneAt = new Map(ops.map((o) => [o.idempotencyKey || `ops:${o.emailId}:${o.action}:${o.targetFolder}`, o]))
  const local = await listOpsEntries()
  const now = Date.now()
  for (const o of local) {
    if (o.status !== 'pushed') continue
    if (doneAt.has(opsIdempotencyKey(o))) {
      await localDB.run('UPDATE local_email_ops SET status = ?, updated_at = ? WHERE id = ?', ['applied', now, o.id])
    }
  }
}
