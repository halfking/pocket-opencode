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

export const INVOICE_PAGE_SIZE = 30

/** 分页追加：去重后按收到日期倒排，避免后一页打乱前一页顺序。 */
export function mergeInvoicePages<T extends Pick<EmailInvoice, 'id' | 'emailDate' | 'createdAt'>>(
  existing: T[],
  incoming: T[],
): T[] {
  const seen = new Set(existing.map((i) => i.id))
  const added = incoming.filter((i) => i.id && !seen.has(i.id))
  return sortInvoicesByReceived([...existing, ...added])
}

export function invoicePageHasMore(pageLen: number, pageSize = INVOICE_PAGE_SIZE): boolean {
  return pageLen >= pageSize
}

export interface PipelineToast {
  kind: 'success' | 'error'
  text: string
}

/**
 * 把一次手工「收信整理」（POST /api/email/pipeline/run）的结果变成一条提示。
 *
 * 抽成纯函数是因为这个判定以前藏在 use-invoice-list 的 runPipeline 里，
 * 且**无视 report.errors**：流水线是会失败的——5 个账户全部 IMAP 超时、
 * 垃圾箱 MOVE 被服务器拒绝、发票下载失败——这些都进 rep.errors。原实现
 * 无条件弹成功，于是「整轮失败」和「一切正常」在界面上长得一模一样，
 * 只能去翻日志才知道出了什么事。
 *
 * 这个类型里 errors 旁边就记着 shareDocUrl 曾被静默丢弃的教训
 * （见 api/email.ts 的 EmailPipelineReport），同一个错误不该犯两次。
 */
export function pipelineToast(rep: {
  newEmails?: number
  remindersSent?: number
  invoices?: { downloaded?: number; pending?: number }
  errors?: string[]
}): PipelineToast {
  const errs = rep.errors ?? []
  if (errs.length > 0) {
    return { kind: 'error', text: `整理完成但有 ${errs.length} 处失败：${errs[0]}` }
  }
  return {
    kind: 'success',
    text:
      `整理完成：新邮件 ${rep.newEmails ?? 0} · 提醒 ${rep.remindersSent ?? 0} · ` +
      `发票 ${rep.invoices?.downloaded ?? 0} 下载 / ${rep.invoices?.pending ?? 0} 待重试`,
  }
}
