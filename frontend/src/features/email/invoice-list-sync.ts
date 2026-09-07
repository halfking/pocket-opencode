import type { EmailInvoice } from '../../api/email.ts'
import { applyIdRemap, type IdRemap } from '../../native/list-sync/id-align.ts'
import { isLocalOnlyId, type ListStamp } from '../../native/list-sync/planner.ts'

export function invoiceToStamp(inv: Pick<EmailInvoice, 'id' | 'updatedAt'> & { dirty?: boolean }): ListStamp {
  return { id: inv.id, updatedAt: inv.updatedAt || 0, dirty: !!inv.dirty }
}

/** 本地临时票对齐服务端：同邮件 + 票号，或同邮件 + 销售方 + 金额。 */
export function matchInvoiceForAlign(
  local: Array<Pick<EmailInvoice, 'id' | 'emailId' | 'invoiceNo' | 'seller' | 'amount'>>,
  remote: Array<Pick<EmailInvoice, 'id' | 'emailId' | 'invoiceNo' | 'seller' | 'amount'>>,
): IdRemap[] {
  const remaps: IdRemap[] = []
  const used = new Set<string>()
  for (const l of local) {
    if (!isLocalOnlyId(l.id)) continue
    const hit = remote.find((r) => {
      if (used.has(r.id) || r.emailId !== l.emailId) return false
      const no = (l.invoiceNo || '').trim()
      if (no && no === (r.invoiceNo || '').trim()) return true
      return !!(l.seller && r.seller && l.seller === r.seller && Number(l.amount) === Number(r.amount))
    })
    if (!hit) continue
    used.add(hit.id)
    remaps.push({ localId: l.id, serverId: hit.id })
  }
  return remaps
}

export function alignInvoiceList(list: EmailInvoice[], remaps: IdRemap[]): EmailInvoice[] {
  return remaps.reduce((acc, remap) => applyIdRemap(acc, remap), list)
}
