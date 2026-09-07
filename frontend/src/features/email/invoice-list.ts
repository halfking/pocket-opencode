import type { EmailInvoice } from '../../api/email'
import { formatEmailRelTime } from './cleanup-filter.ts'

export type InvoiceFileKind = 'image' | 'pdf' | 'unknown'

export function invoiceReceivedSortKey(inv: Pick<EmailInvoice, 'emailDate' | 'createdAt'>): number {
  return inv.emailDate && inv.emailDate > 0 ? inv.emailDate : inv.createdAt
}

/** 按来源邮件收到时间倒排；没有 emailDate 时用 createdAt。 */
export function sortInvoicesByReceived<T extends Pick<EmailInvoice, 'emailDate' | 'createdAt'>>(
  list: T[],
): T[] {
  return [...list].sort((a, b) => {
    const da = invoiceReceivedSortKey(a)
    const db = invoiceReceivedSortKey(b)
    if (da !== db) return db - da
    return (b.createdAt || 0) - (a.createdAt || 0)
  })
}

export function invoiceIssueDateLabel(invoiceDate?: string): string {
  const d = (invoiceDate || '').trim()
  return d ? `开票 ${d}` : '开票日期未识别'
}

export function invoiceReceivedLabel(
  emailDate?: number,
  createdAt?: number,
  nowMs = Date.now(),
): string {
  const raw = emailDate && emailDate > 0 ? emailDate : createdAt || 0
  const rel = formatEmailRelTime(raw, nowMs)
  return rel ? `收到 ${rel}` : ''
}

export function invoiceFileKind(fileName?: string): InvoiceFileKind {
  const n = (fileName || '').toLowerCase()
  if (/\.(png|jpe?g|webp|gif)$/.test(n)) return 'image'
  if (n.endsWith('.pdf')) return 'pdf'
  return 'unknown'
}

export function invoiceHasFile(inv: Pick<EmailInvoice, 'fileName'>): boolean {
  return !!(inv.fileName && inv.fileName.trim())
}
