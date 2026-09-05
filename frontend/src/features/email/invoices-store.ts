/**
 * invoices-store.ts — 发票自动整理本地镜像
 *
 * 服务端（PostgreSQL email_invoices）是 SSOT；本模块把结构化发票字段镜像进
 * 本地 SQLCipher（local_email_invoices），供离线浏览与列表首屏加速。
 * 只镜像结构化字段，不落邮件正文（正文仍留在服务端加密缓存）。
 */
import { localDB } from '../../native/local-db'
import { emailApi, type EmailInvoice } from '../../api/email'

export type LocalInvoice = EmailInvoice

function rowToInvoice(r: any): LocalInvoice {
  return {
    id: r.id,
    emailId: r.email_id,
    accountId: r.account_id ?? '',
    kind: r.kind ?? 'bill',
    category: r.category ?? '其他',
    title: r.title ?? '',
    seller: r.seller ?? '',
    amount: Number(r.amount) || 0,
    currency: r.currency ?? 'CNY',
    invoiceNo: r.invoice_no || undefined,
    invoiceDate: r.invoice_date || undefined,
    subject: r.subject ?? '',
    status: (r.status as 'new' | 'filed') ?? 'new',
    extractedBy: (r.extracted_by as 'rule' | 'llm') ?? 'rule',
    createdAt: Number(r.created_at) || 0,
    updatedAt: Number(r.updated_at) || 0,
  }
}

/** 读本地镜像（离线可用）。status 为空返回全部。 */
export async function listLocal(status?: 'new' | 'filed'): Promise<LocalInvoice[]> {
  const sql = status
    ? 'SELECT * FROM local_email_invoices WHERE status = ? ORDER BY created_at DESC'
    : 'SELECT * FROM local_email_invoices ORDER BY created_at DESC'
  const rows = await localDB.query<any>(sql, status ? [status] : [])
  return rows.map(rowToInvoice)
}

/** 拉取服务端全量并重建本地镜像（全量覆盖，数据量小；失败抛给调用方）。 */
export async function syncFromServer(): Promise<number> {
  const res = await emailApi.listInvoices()
  const invoices = res.invoices ?? []
  const stmts = invoices.map((inv) => ({
    statement: `INSERT OR REPLACE INTO local_email_invoices
      (id, email_id, account_id, kind, category, title, seller, amount, currency,
       invoice_no, invoice_date, subject, status, extracted_by, created_at, updated_at)
      VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
    values: [
      inv.id, inv.emailId, inv.accountId, inv.kind, inv.category, inv.title,
      inv.seller, inv.amount, inv.currency, inv.invoiceNo ?? '', inv.invoiceDate ?? '',
      inv.subject, inv.status, inv.extractedBy, inv.createdAt, inv.updatedAt,
    ],
  }))
  // 先清后写，保证镜像与服务端一致（含服务端已删除的记录）
  await localDB.runInTransaction([
    { statement: 'DELETE FROM local_email_invoices', values: [] },
    ...stmts,
  ])
  return invoices.length
}

/** 本地更新归档状态（乐观更新；服务端仍以 PATCH 为准）。 */
export async function setLocalStatus(id: string, status: 'new' | 'filed'): Promise<void> {
  await localDB.run('UPDATE local_email_invoices SET status = ?, updated_at = ? WHERE id = ?', [
    status,
    Math.floor(Date.now() / 1000),
    id,
  ])
}

/** 本地删除。 */
export async function removeLocal(id: string): Promise<void> {
  await localDB.run('DELETE FROM local_email_invoices WHERE id = ?', [id])
}
