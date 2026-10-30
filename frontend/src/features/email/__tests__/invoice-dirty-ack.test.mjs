import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture, gate } from '../../../test-support/notes-sqlite-fixture.mjs'

test('an invoice status edited during upload stays dirty until its own response', { timeout: 10000 }, () => fixture(async ({ loadModule, respond, setModules }) => {
  await setModules({ email: 'cloud' })
  const invoices = await loadModule('/src/features/email/invoices-store.ts')
  const { pushDirtyInvoices } = await loadModule('/src/features/email/invoice-list-pull.ts')
  await invoices.upsertFromServer([{
    id: 'invoice', emailId: 'mail', accountId: 'account', kind: 'bill', category: 'other', title: 'Invoice',
    seller: 'seller', amount: 3, currency: 'CNY', subject: 'private', status: 'new', extractedBy: 'rule', createdAt: 1, updatedAt: 1,
  }])
  await invoices.setLocalStatus('invoice', 'filed')
  const pause = gate(), statuses = []
  respond(async (url, options) => {
    assert.match(url, /\/api\/emails\/invoices\/invoice$/)
    statuses.push(JSON.parse(options.body).status)
    if (statuses.length === 1) await pause.pause()
    return { ok: true }
  })
  const first = pushDirtyInvoices()
  await pause.started
  await invoices.setLocalStatus('invoice', 'new')
  pause.release()
  await first
  const dirty = await invoices.listDirty()
  assert.equal(dirty.length, 1)
  assert.equal(dirty[0].status, 'new')
  await pushDirtyInvoices()
  assert.deepEqual(statuses, ['filed', 'new'])
  assert.deepEqual(await invoices.listDirty(), [])
}))
