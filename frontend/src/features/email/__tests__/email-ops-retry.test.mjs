import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture, gate } from '../../../test-support/notes-sqlite-fixture.mjs'

test('an interrupted mail execution remains visible and retries the original idempotency key', () => fixture(async ({ loadModule, respond }) => {
  const folders = await loadModule('/src/features/email/email-folders-store.ts')
  await folders.recordOpsEntry({ accountId: 'account', emailId: 'mail', uid: 1, action: 'move', targetFolder: 'Archive', subject: 'Move me' })
  const pushes = [], executions = []
  let offline = true
  respond((url, options) => {
    const body = JSON.parse(options.body)
    if (url.endsWith('/api/emails/ops')) { pushes.push(body.ops); return { recorded: 1 } }
    if (url.endsWith('/api/emails/ops/sync')) {
      executions.push(body.ids)
      if (offline) throw new Error('lost execution response')
      return { applied: 1, failed: 0, remaining: 0, errors: [] }
    }
    throw new Error(`Unexpected request: ${url}`)
  })
  const first = await folders.flushEmailOps()
  assert.equal(first.errors.length, 1)
  offline = false
  const second = await folders.flushEmailOps()
  assert.equal(second.applied, 1)
  assert.deepEqual(pushes[1], pushes[0])
  assert.deepEqual(executions, [['ops:mail:move:Archive'], ['ops:mail:move:Archive']])
  assert.equal(await folders.countPendingOps(), 0)
}))

test('an execution awaiting retry counts as unfinished mail work', () => fixture(async ({ loadModule, respond }) => {
  const folders = await loadModule('/src/features/email/email-folders-store.ts')
  await folders.recordOpsEntry({ accountId: 'account', emailId: 'mail', uid: 1, action: 'delete', targetFolder: '', subject: 'Delete me' })
  respond(url => {
    if (url.endsWith('/api/emails/ops')) return { recorded: 1 }
    throw new Error('offline before execution')
  })
  await folders.flushEmailOps()
  assert.equal(await folders.countPendingOps(), 1)
  assert.equal(await folders.hasPendingOpsForEmail('mail'), true)
}))

test('completed mail history cannot hide an older unfinished operation from the bounded batch', () => fixture(async ({ loadModule, raw, respond, login }) => {
  const folders = await loadModule('/src/features/email/email-folders-store.ts')
  await folders.recordOpsEntry({ accountId: 'account', emailId: 'old-mail', uid: 1, action: 'move', targetFolder: 'Archive', subject: 'Old pending' })
  raw.run('UPDATE local_email_ops SET created_at=1')
  for (let n = 0; n < 205; n++) {
    await folders.recordOpsEntry({ accountId: 'account', emailId: `done-${n}`, uid: n + 2, action: 'delete', targetFolder: '', subject: 'Completed history' })
  }
  raw.run("UPDATE local_email_ops SET status='applied',created_at=2 WHERE email_id<>'old-mail'")
  const pushes = []
  respond((url, options) => {
    if (url.endsWith('/api/emails/ops')) { pushes.push(JSON.parse(options.body).ops); return { recorded: 1 } }
    return { applied: 1, failed: 0, remaining: 0, errors: [] }
  })
  await login('B')
  assert.equal((await folders.flushEmailOps()).pushed, 0)
  await login('A')
  const report = await folders.flushEmailOps()
  assert.equal(report.applied, 1)
  assert.deepEqual(pushes.map(batch => batch.map(item => item.emailId)), [['old-mail']])
  assert.equal(await folders.countPendingOps(), 0)
}))

test('a delayed mail acknowledgement cannot complete a newer move saved during the push', () => fixture(async ({ loadModule, respond }) => {
  const folders = await loadModule('/src/features/email/email-folders-store.ts')
  const entry = { accountId: 'account', emailId: 'mail', uid: 1, action: 'move', targetFolder: 'Archive', subject: 'Move me' }
  await folders.recordOpsEntry(entry)
  const pause = gate(), pushes = []
  respond(async (url, options) => {
    if (url.endsWith('/api/emails/ops')) {
      pushes.push(JSON.parse(options.body).ops)
      if (pushes.length === 1) await pause.pause()
      return { recorded: 1 }
    }
    return { applied: 1, failed: 0, remaining: 0, errors: [] }
  })
  const first = folders.flushEmailOps()
  await pause.started
  await folders.recordOpsEntry({ ...entry, targetFolder: 'Later' })
  pause.release()
  await first
  assert.equal(await folders.countPendingOps(), 1)
  assert.equal((await folders.listOpsEntries('pending'))[0].targetFolder, 'Later')
  await folders.flushEmailOps()
  assert.deepEqual(pushes.map(batch => batch[0].targetFolder), ['Archive', 'Later'])
  assert.equal(await folders.countPendingOps(), 0)
}))

test('an aggregate response without applied items cannot silently complete an uncertain mail operation', () => fixture(async ({ loadModule, respond }) => {
  const folders = await loadModule('/src/features/email/email-folders-store.ts')
  await folders.recordOpsEntry({ accountId: 'account', emailId: 'mail', uid: 1, action: 'delete', targetFolder: '', subject: 'Delete me' })
  respond(url => url.endsWith('/api/emails/ops') ? { recorded: 0 } : { applied: 0, failed: 0, remaining: 0, errors: [] })
  const report = await folders.flushEmailOps()
  assert.equal(report.remaining, 1)
  assert.equal(report.errors.length, 1)
  assert.equal(await folders.countPendingOps(), 1)
  assert.equal((await folders.listOpsEntries())[0].status, 'pushed')
}))

test('a selected operation beyond the first batch is sent alone and remaining work stays visible', () => fixture(async ({ loadModule, raw, respond }) => {
  const folders = await loadModule('/src/features/email/email-folders-store.ts')
  for (let n = 0; n < 201; n++) {
    await folders.recordOpsEntry({ accountId: 'account', emailId: `mail-${n}`, uid: n + 1, action: 'delete', targetFolder: '', subject: 'Pending' })
  }
  raw.run('UPDATE local_email_ops SET created_at=uid')
  const selected = (await folders.listOpsEntries())[0], batches = []
  let size = 0
  respond((url, options) => {
    if (url.endsWith('/api/emails/ops')) { const ops = JSON.parse(options.body).ops; size = ops.length; batches.push(ops); return { recorded: size } }
    return { applied: size, failed: 0, remaining: 0, errors: [] }
  })
  const first = await folders.flushEmailOps({ ids: [selected.id] })
  assert.deepEqual(batches[0].map(item => item.emailId), ['mail-200'])
  assert.equal(first.remaining, 200)
  const second = await folders.flushEmailOps()
  assert.equal(batches[1].length, 200)
  assert.equal(second.remaining, 0)
}))
