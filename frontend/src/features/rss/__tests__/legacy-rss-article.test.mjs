import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture } from '../../../test-support/notes-sqlite-fixture.mjs'

const original = {
  id: 'legacy-item', source_id: 'src_original', title: 'Original article', url: 'https://example.test/article',
  summary: 'Portable summary', updated_at: '2026-01-02T03:04:05.123456Z',
  user_id: 'user-A', workspace_id: 'workspace', content: 'Native-only body', status: 'starred',
}

test('legacy article archiving keeps its source version and shares one identity with later explicit publication', () => fixture(async f => {
  f.respond(() => original)
  const { prepareLegacyRssSnapshot } = await f.loadModule('/src/features/rss/legacy-rss-snapshot.ts')
  const { captureRssPortalOperation, rssPortalStore, listRssPortalRecords, copyRssItemToPortal } = await f.loadModule('/src/features/rss/rss-sync-store.ts')
  const operation = captureRssPortalOperation()
  const store = rssPortalStore(operation.db)
  await store.observeEpoch(0)
  const prepared = await prepareLegacyRssSnapshot('item', original.id, operation)
  assert.equal(prepared.title, original.title)
  assert.equal(f.count('local_portal_records'), 0)
  assert.equal(await prepared.save(), true)
  const [entry] = await listRssPortalRecords()
  assert.equal(entry.item.ext_id, 'rss-item:pocketd-item:legacy-item')
  assert.equal(entry.item.client_updated_at, original.updated_at)
  assert.equal(entry.item.epoch, null)
  assert.deepEqual(entry.item.payload, { source: original.source_id, url: original.url, summary: original.summary })
  assert.equal(f.count('local_portal_records'), 1)
  assert.equal(f.count('local_portal_record_outbox'), 0)
  assert.equal(f.requests.length, 2)
  assert.ok(f.requests.every(request => request.url.endsWith('/items/legacy-item/migration-snapshot') && request.options.method === 'GET'))
  await copyRssItemToPortal({ ...original, sourceId: original.source_id })
  assert.equal(f.count('local_portal_records'), 1)
  const [queued] = await store.pending()
  assert.equal(queued.ext_id, entry.item.ext_id)
  await f.login('B')
  assert.deepEqual(await listRssPortalRecords(), [])
  await assert.rejects(prepareLegacyRssSnapshot('item', original.id, captureRssPortalOperation()), /Invalid historical RSS snapshot/)
}))

test('an article with a changed or missing original version cannot create a new projection', () => fixture(async f => {
  let snapshot = { ...original }
  f.respond(() => snapshot)
  const { prepareLegacyRssSnapshot } = await f.loadModule('/src/features/rss/legacy-rss-snapshot.ts')
  const { captureRssPortalOperation, copyRssItemToPortal } = await f.loadModule('/src/features/rss/rss-sync-store.ts')
  const operation = captureRssPortalOperation()
  const prepare = () => prepareLegacyRssSnapshot('item', original.id, operation)
  const prepared = await prepare()
  snapshot = { ...original, summary: 'Different summary at the same timestamp' }
  await assert.rejects(prepared.save(), /changed during confirmation/)
  for (const patch of [
    { updated_at: null, published_at: original.updated_at, fetched_at: original.updated_at },
    { source_id: '' }, { workspace_id: 'other' }, { url: 'https://example.test/path#private' },
  ]) {
    snapshot = { ...original, ...patch }
    await assert.rejects(prepare())
  }
  assert.equal(f.count('local_portal_records'), 0)
  assert.equal(f.count('local_portal_record_outbox'), 0)
  snapshot = { ...original }
  await copyRssItemToPortal({ ...original, sourceId: original.source_id })
  assert.equal(await prepare(), null)
  assert.equal(await prepared.save(), false)
  assert.equal(f.count('local_portal_records'), 1)
  assert.equal(f.count('local_portal_record_outbox'), 1)
}))
