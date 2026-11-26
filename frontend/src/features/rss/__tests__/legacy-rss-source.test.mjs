import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture } from '../../../test-support/notes-sqlite-fixture.mjs'

const path = '/src/features/rss/legacy-rss-source.ts'
const original = {
  id: 'src_legacy', user_id: 'user-A', workspace_id: 'workspace', title: 'Original feed',
  updated_at: '2026-01-02T03:04:05.123456Z', url: 'https://example.test/feed', category: 'news',
}

test('legacy RSS metadata snapshot keeps the exact source version and never queues an upload', () => fixture(async f => {
  const snapshot = { ...original, description: 'native private source description', enabled: true }
  f.respond(() => snapshot)
  const { prepareLegacyRssSource } = await f.loadModule(path)
  const { captureRssPortalOperation, rssPortalStore, listRssPortalRecords, saveRssPortalRecord } = await f.loadModule('/src/features/rss/rss-sync-store.ts')
  const operation = captureRssPortalOperation()
  const store = rssPortalStore(operation.db)
  await store.observeEpoch(0)
  const prepared = await prepareLegacyRssSource(original.id, operation)
  assert.equal(prepared.title, original.title)
  assert.equal(prepared.workspaceId, original.workspace_id)
  assert.equal(f.count('local_portal_records'), 0)
  assert.equal(await prepared.save(), true)
  const [entry] = await listRssPortalRecords()
  assert.equal(entry.id, 'native:source:pocketd-source:src_legacy')
  assert.equal(entry.item.ext_id, 'pocketd-source:src_legacy')
  assert.equal(entry.item.client_updated_at, original.updated_at)
  assert.equal(entry.item.epoch, null)
  assert.deepEqual(entry.item.payload, { url: original.url, category: original.category })
  assert.equal(f.count('local_portal_record_outbox'), 0)
  assert.equal(f.requests.length, 2)
  assert.ok(f.requests.every(request => request.url.endsWith('/src_legacy/migration-snapshot') && request.options.method === 'GET'))
  assert.equal(await prepareLegacyRssSource(original.id, operation), null)
  assert.equal(await prepared.save(), false)
  assert.equal((await store.find(entry.id)).op_id, entry.item.op_id)
  await f.login('B')
  assert.deepEqual(await listRssPortalRecords(), [])
  await assert.rejects(prepareLegacyRssSource(original.id, captureRssPortalOperation()), /Invalid historical RSS/)
  await f.login('A')
  await saveRssPortalRecord(entry, { title: 'Later edit', payload: entry.item.payload })
  const [queued] = await rssPortalStore(captureRssPortalOperation().db).pending()
  assert.equal(queued.ext_id, entry.item.ext_id)
}))

test('changed snapshots, wrong workspaces, unsafe addresses and stale account confirmations cannot import', () => fixture(async f => {
  let snapshot = { ...original }
  f.respond(() => snapshot)
  const { prepareLegacyRssSource } = await f.loadModule(path)
  const { captureRssPortalOperation } = await f.loadModule('/src/features/rss/rss-sync-store.ts')
  const operation = captureRssPortalOperation()
  const prepared = await prepareLegacyRssSource(original.id, operation)
  snapshot = { ...original, title: 'Changed at the same timestamp' }
  await assert.rejects(prepared.save(), /changed during confirmation/)
  for (const patch of [{ workspace_id: 'other' }, { url: 'https://example.test/?private=value' },
    { url: 'https://user:secret@example.test/feed' }, { updated_at: '' }]) {
    snapshot = { ...original, ...patch }
    await assert.rejects(prepareLegacyRssSource(original.id, operation))
  }
  snapshot = { ...original }
  await f.login('B')
  const requestsBefore = f.requests.length
  await assert.rejects(prepared.save(), { name: 'AbortError' })
  assert.equal(f.requests.length, requestsBefore)
  assert.equal(f.count('local_portal_records'), 0)
  assert.equal(f.count('local_portal_record_outbox'), 0)
}))
