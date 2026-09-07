import assert from 'node:assert/strict'
import { describe, it } from 'node:test'
import {
  invoiceFileKind,
  invoiceIssueDateLabel,
  invoicePageHasMore,
  invoiceReceivedLabel,
  invoiceReceivedSortKey,
  mergeInvoicePages,
  sortInvoicesByReceived,
} from './invoice-list.ts'
import type { EmailInvoice } from '../../api/email'

function inv(partial: Partial<EmailInvoice>): EmailInvoice {
  return {
    id: 'i1',
    emailId: 'e1',
    accountId: 'a1',
    kind: 'e-invoice',
    category: '其他',
    title: '',
    seller: '甲',
    amount: 1,
    subject: '发票',
    status: 'downloaded',
    extractedBy: 'rule',
    createdAt: 100,
    updatedAt: 100,
    ...partial,
  }
}

describe('invoice-list', () => {
  it('sorts by received email date descending', () => {
    const rows = [
      inv({ id: 'old', emailDate: 100, createdAt: 999 }),
      inv({ id: 'new', emailDate: 300, createdAt: 1 }),
      inv({ id: 'mid', emailDate: 0, createdAt: 200 }),
    ]
    assert.deepEqual(sortInvoicesByReceived(rows).map((r) => r.id), ['new', 'mid', 'old'])
  })

  it('uses createdAt when emailDate is missing', () => {
    assert.equal(invoiceReceivedSortKey(inv({ createdAt: 42 })), 42)
    assert.equal(invoiceReceivedSortKey(inv({ emailDate: 9, createdAt: 42 })), 9)
  })

  it('labels issue and received dates', () => {
    assert.equal(invoiceIssueDateLabel('2026-09-01'), '开票 2026-09-01')
    assert.equal(invoiceIssueDateLabel(''), '开票日期未识别')
    assert.equal(invoiceReceivedLabel(1_725_000_000, 0, 1_725_000_000 * 1000 + 3_600_000), '收到 1小时前')
  })

  it('classifies invoice files for thumb vs document icon', () => {
    assert.equal(invoiceFileKind('a.jpg'), 'image')
    assert.equal(invoiceFileKind('a.PNG'), 'image')
    assert.equal(invoiceFileKind('a.pdf'), 'pdf')
    assert.equal(invoiceFileKind(''), 'unknown')
  })

  it('appends a later page without duplicating or breaking received desc', () => {
    const first = [inv({ id: 'new', emailDate: 300 }), inv({ id: 'mid', emailDate: 200 })]
    const second = [inv({ id: 'mid', emailDate: 200 }), inv({ id: 'old', emailDate: 50, createdAt: 999 })]
    assert.deepEqual(mergeInvoicePages(first, second).map((r) => r.id), ['new', 'mid', 'old'])
    assert.equal(invoicePageHasMore(30), true)
    assert.equal(invoicePageHasMore(29), false)
  })
})
