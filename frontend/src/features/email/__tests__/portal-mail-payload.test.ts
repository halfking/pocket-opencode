import assert from 'node:assert/strict'
import { test } from 'node:test'
import { portalMailPayload } from '../portal-mail-adapter.ts'

test('mail metadata permits only the portable account and summary fields', () => {
  assert.deepEqual(portalMailPayload({ address: 'a@example.com', display_name: 'A' }, 'account'), { address: 'a@example.com', display_name: 'A', last_sync_at: '' })
  assert.deepEqual(portalMailPayload({ from: 'B', subject: 'Subject', snippet: '  full summary\n', received_at: '2026-10-30T00:00:00Z' }, 'summary'), { from: 'B', subject: 'Subject', snippet: '  full summary\n', received_at: '2026-10-30T00:00:00Z' })
  for (const forbidden of ['password', 'credential_encrypted', 'body', 'html', 'attachments', 'translations', 'account_id']) {
    assert.throws(() => portalMailPayload({ from: 'B', [forbidden]: 'private' }, 'summary'), /Unknown/)
  }
  assert.throws(() => portalMailPayload({ snippet: '中'.repeat(1366) }, 'summary'), /4096 bytes/)
  assert.throws(() => portalMailPayload({ snippet: { secret: 'nested' } }, 'summary'), /must be a string/)
  assert.throws(() => portalMailPayload({ snippet: 'content' }, 'item'), /Unsupported/)
})
