/**
 * invoices-store — 发票本地镜像。服务端是 SSOT；本地供首屏与离线。
 * 同步只 upsert，禁止整表 DELETE。
 */
import { localDB } from '../../native/local-db'
import type { EmailInvoice, EmailInvoiceStatus } from '../../api/email'
import { pageHasMore } from '../../native/list-sync/page'
import { INVOICE_PAGE_SIZE } from './invoice-list'

export type LocalInvoice = EmailInvoice

const INVOICE_COLS = `i.id, i.email_id, i.account_id, i.kind, i.category, i.title, i.seller,
  i.amount, i.currency, i.invoice_no, i.invoice_date, i.subject, i.status, i.extracted_by,
  i.created_at, i.updated_at, i.file_name, i.file_source, i.attempts, i.last_error,
  i.feishu_sent_at, i.dirty, i.client_id,
  COALESCE(i.email_date, e.date, 0) AS email_date`

function rowToInvoice(r: Record<string, unknown>): LocalInvoice {
  return {
    id: String(r.id),
    emailId: String(r.email_id ?? ''),
    accountId: String(r.account_id ?? ''),
    kind: String(r.kind ?? 'bill'),
    category: String(r.category ?? '其他'),
    title: String(r.title ?? ''),
    seller: String(r.seller ?? ''),
    amount: Number(r.amount) || 0,
    currency: String(r.currency ?? 'CNY'),
    invoiceNo: String(r.invoice_no || '') || undefined,
    invoiceDate: String(r.invoice_date || '') || undefined,
    emailDate: Number(r.email_date) || 0,
    subject: String(r.subject ?? ''),
    status: (r.status as EmailInvoice['status']) ?? 'new',
    extractedBy: (r.extracted_by as 'rule' | 'llm') ?? 'rule',
    createdAt: Number(r.created_at) || 0,
    updatedAt: Number(r.updated_at) || 0,
    fileName: String(r.file_name || '') || undefined,
    fileSource: String(r.file_source || '') || undefined,
    attempts: Number(r.attempts) || 0,
    lastError: String(r.last_error || '') || undefined,
    feishuSentAt: Number(r.feishu_sent_at) || 0,
    dirty: Number(r.dirty) === 1,
    clientId: String(r.client_id || '') || undefined,
  }
}

function statusClause(status?: EmailInvoiceStatus | ''): { sql: string; vals: unknown[] } {
  if (!status) return { sql: '', vals: [] }
  return { sql: ' WHERE i.status = ?', vals: [status] }
}

export async function listLocal(status?: EmailInvoiceStatus | ''): Promise<LocalInvoice[]> {
  const page = await listLocalPage({ status, limit: 500, offset: 0 })
  return page.rows
}

export async function listLocalPage(opts: {
  status?: EmailInvoiceStatus | ''
  limit?: number
  offset?: number
}): Promise<{ rows: LocalInvoice[]; hasMore: boolean }> {
  const limit = opts.limit ?? INVOICE_PAGE_SIZE
  const offset = opts.offset ?? 0
  const { sql: where, vals } = statusClause(opts.status)
  const rows = await localDB.query<Record<string, unknown>>(
    `SELECT ${INVOICE_COLS}
     FROM local_email_invoices i
     LEFT JOIN local_emails e ON e.id = i.email_id
     ${where}
     ORDER BY COALESCE(i.email_date, e.date, i.created_at) DESC, i.id DESC
     LIMIT ? OFFSET ?`,
    [...vals, limit + 1, offset],
  )
  const hasMore = pageHasMore(rows.length, limit + 1) && rows.length > limit
  return { rows: rows.slice(0, limit).map(rowToInvoice), hasMore }
}

function upsertValues(inv: EmailInvoice, dirty: number): unknown[] {
  return [
    inv.id, inv.emailId, inv.accountId, inv.kind, inv.category, inv.title,
    inv.seller, inv.amount, inv.currency, inv.invoiceNo ?? '', inv.invoiceDate ?? '',
    inv.subject, inv.status, inv.extractedBy, inv.createdAt, inv.updatedAt,
    inv.fileName ?? '', inv.fileSource ?? '', inv.attempts ?? 0, inv.lastError ?? '',
    inv.feishuSentAt ?? 0, Number(inv.emailDate) || 0, dirty, inv.clientId ?? '',
  ]
}

const UPSERT_SQL = `INSERT OR REPLACE INTO local_email_invoices
  (id, email_id, account_id, kind, category, title, seller, amount, currency,
   invoice_no, invoice_date, subject, status, extracted_by, created_at, updated_at,
   file_name, file_source, attempts, last_error, feishu_sent_at, email_date, dirty, client_id)
  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

/** 按页 upsert。本地 dirty 且更新不早于服务端时跳过，避免冲掉未回推改动。 */
export async function upsertFromServer(invoices: EmailInvoice[]): Promise<number> {
  let n = 0
  for (const inv of invoices) {
    if (!inv.id) continue
    const local = await localDB.queryOne<{ dirty: number; updated_at: number }>(
      'SELECT dirty, updated_at FROM local_email_invoices WHERE id = ?',
      [inv.id],
    )
    if (local && Number(local.dirty) === 1 && Number(local.updated_at) >= (inv.updatedAt || 0)) {
      continue
    }
    await localDB.run(UPSERT_SQL, upsertValues(inv, 0))
    n++
  }
  return n
}

/** @deprecated 全量覆盖会丢掉未同步页与本地 dirty；请用 upsertFromServer。 */
export async function syncFromServer(invoices?: EmailInvoice[]): Promise<number> {
  if (!invoices) return 0
  return upsertFromServer(invoices)
}

export async function setLocalStatus(id: string, status: EmailInvoiceStatus): Promise<void> {
  await localDB.run(
    'UPDATE local_email_invoices SET status = ?, updated_at = ?, dirty = 1 WHERE id = ?',
    [status, Math.floor(Date.now() / 1000), id],
  )
}

export async function clearDirty(id: string): Promise<void> {
  await localDB.run('UPDATE local_email_invoices SET dirty = 0 WHERE id = ?', [id])
}

export async function listDirty(): Promise<LocalInvoice[]> {
  const rows = await localDB.query<Record<string, unknown>>(
    `SELECT ${INVOICE_COLS}
     FROM local_email_invoices i
     LEFT JOIN local_emails e ON e.id = i.email_id
     WHERE i.dirty = 1`,
  )
  return rows.map(rowToInvoice)
}

export async function remapLocalId(localId: string, serverId: string): Promise<void> {
  if (!localId || !serverId || localId === serverId) return
  const existing = await localDB.queryOne<{ id: string }>(
    'SELECT id FROM local_email_invoices WHERE id = ?',
    [serverId],
  )
  if (existing) {
    await localDB.run('DELETE FROM local_email_invoices WHERE id = ?', [localId])
    return
  }
  await localDB.run(
    'UPDATE local_email_invoices SET id = ?, client_id = ?, dirty = 0 WHERE id = ?',
    [serverId, localId, localId],
  )
}

export async function removeLocal(id: string): Promise<void> {
  await localDB.run('DELETE FROM local_email_invoices WHERE id = ?', [id])
}
