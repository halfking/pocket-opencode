import { emailApi, type EmailInvoice, type EmailInvoiceStatus } from '../../api/email'
import { isLocalOnlyId } from '../../native/list-sync/planner'
import { INVOICE_PAGE_SIZE, sortInvoicesByReceived } from './invoice-list'
import { matchInvoiceForAlign } from './invoice-list-sync'
import * as invoiceStore from './invoices-store'
import type { IdRemap } from '../../native/list-sync/id-align'

export async function pullInvoiceServerPage(input: {
  status?: EmailInvoiceStatus
  offset: number
  current: EmailInvoice[]
  pageSize?: number
}): Promise<{
  rows: EmailInvoice[]
  hasMore: boolean
  remaps: IdRemap[]
  totals: { total: number; filed: number; amount: number }
}> {
  const pageSize = input.pageSize ?? INVOICE_PAGE_SIZE
  const res = await emailApi.listInvoices(input.status, pageSize, input.offset)
  const page = res.invoices ?? []
  const remaps = matchInvoiceForAlign(input.current, page)
  for (const remap of remaps) {
    await invoiceStore.remapLocalId(remap.localId, remap.serverId)
  }
  await invoiceStore.upsertFromServer(page)
  const window = Math.max(pageSize, input.current.length, input.offset + page.length)
  const local = await invoiceStore.listLocalPage({
    status: input.status,
    limit: window,
    offset: 0,
  })
  return {
    rows: sortInvoicesByReceived(local.rows),
    hasMore: res.hasMore ?? local.hasMore,
    remaps,
    totals: { total: res.total, filed: res.filed, amount: res.amount },
  }
}

export async function pushDirtyInvoices(): Promise<void> {
  const dirty = await invoiceStore.listDirty()
  for (const inv of dirty) {
    if (isLocalOnlyId(inv.id)) continue
    await emailApi.setInvoiceStatus(inv.id, inv.status)
    await invoiceStore.clearDirty(inv.id)
  }
}
