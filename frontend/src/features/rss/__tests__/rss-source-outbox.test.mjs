import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture, gate } from '../../../test-support/notes-sqlite-fixture.mjs'

const path = '/src/features/rss/rss-source-outbox.ts'
const first = { url: 'https://example.test/feed', title: 'Feed queued offline', enabled: true, fetchInterval: '45m' }
const receipt = (intent, user = 'user-A') => ({
  operation_id: intent.operationId, user_id: user, workspace_id: 'workspace', source_id: 'src-created',
})

test('the durable queue retries one immutable native operation after a lost response and a newer epoch', () => fixture(async f => {
  await f.setModules({ rss: 'cloud' })
  const { queueRssSource, flushQueuedRssSources, listQueuedRssSources } = await f.loadModule(path)
  const { captureRssPortalOperation, rssPortalStore } = await f.loadModule('/src/features/rss/rss-sync-store.ts')
  await rssPortalStore(captureRssPortalOperation().db).observeEpoch(3)
  const operation = captureRssPortalOperation()
  await queueRssSource(first, operation)
  assert.equal(f.requests.length, 0)
  assert.equal((await listQueuedRssSources())[0].label, first.title)
  const saved = await f.db.queryOne('SELECT request_json FROM local_rss_source_outbox')
  const intent = JSON.parse(saved.request_json)
  assert.equal(intent.portalEpoch, 3)
  assert.equal(intent.clientUpdatedAt, new Date(operation.modifiedAt).toISOString())
  f.respond(() => { throw new TypeError('Connection closed before acknowledgement') })
  await assert.rejects(flushQueuedRssSources())
  assert.equal(f.count('local_rss_source_outbox'), 1)
  await rssPortalStore(captureRssPortalOperation().db).observeEpoch(4)
  f.respond((_url, options) => receipt(JSON.parse(options.body), 'wrong-user'))
  await assert.rejects(flushQueuedRssSources(), /Invalid RSS source receipt/)
  assert.equal(f.count('local_rss_source_outbox'), 1)
  f.respond((_url, options) => receipt(JSON.parse(options.body)))
  await flushQueuedRssSources()
  assert.equal(f.count('local_rss_source_outbox'), 0)
  assert.deepEqual(f.requests.map(request => request.options.body), Array(3).fill(saved.request_json))
}))

test('switching accounts during submission retains the original work and a local policy blocks replay', () => fixture(async f => {
  await f.setModules({ rss: 'cloud' })
  const { queueRssSource, flushQueuedRssSources, listQueuedRssSources } = await f.loadModule(path)
  await queueRssSource(first)
  const waiting = gate()
  f.respond(async (_url, options) => {
    await waiting.pause()
    return receipt(JSON.parse(options.body))
  })
  const submission = flushQueuedRssSources()
  const stopped = assert.rejects(submission, { name: 'AbortError' })
  await waiting.started
  await f.login('B')
  await f.setModules({ rss: 'cloud' })
  waiting.release()
  await stopped
  assert.deepEqual(await listQueuedRssSources(), [])
  await flushQueuedRssSources()
  assert.equal(f.requests.length, 1)
  await f.login('A')
  await f.setModules({ rss: 'local' })
  assert.equal((await listQueuedRssSources()).length, 1)
  await assert.rejects(flushQueuedRssSources(), { name: 'AbortError' })
  assert.equal(f.requests.length, 1)
  await f.setModules({ rss: 'cloud' })
  await flushQueuedRssSources()
  assert.equal(f.count('local_rss_source_outbox'), 0)
  assert.equal(f.requests[0].options.body, f.requests[1].options.body)
}))

test('only known permanent rejections leave the retry queue and do not block the next feed', () => fixture(async f => {
  await f.setModules({ rss: 'cloud' })
  const { queueRssSource, flushQueuedRssSources, listQueuedRssSources, removeRejectedRssSource } = await f.loadModule(path)
  const second = { ...first, title: 'Next feed', url: 'https://example.test/next' }
  await queueRssSource(first)
  await queueRssSource(second)
  f.respond((_url, options) => {
    const intent = JSON.parse(options.body)
    if (intent.url === first.url) return Response.json({ error: 'rss_source_conflict' }, { status: 409 })
    return receipt(intent)
  })
  await flushQueuedRssSources()
  const [row] = await listQueuedRssSources()
  assert.equal(row.rejection, 'conflict')
  assert.equal(f.requests.length, 2)
  await flushQueuedRssSources()
  assert.equal(f.requests.length, 2)
  await removeRejectedRssSource(row.id)
  assert.deepEqual(await listQueuedRssSources(), [])
}))

test('the application encryption policy protects queued private feed addresses at rest', () => fixture(async f => {
  await f.setModules({ rss: 'cloud' })
  f.cryptoConfig.setFieldEncryption('enabled')
  const { queueRssSource, flushQueuedRssSources } = await f.loadModule(path)
  const url = 'https://example.test/feed?key=synthetic'
  await queueRssSource({ ...first, url })
  const row = await f.db.queryOne('SELECT request_json FROM local_rss_source_outbox')
  assert.match(row.request_json, /^e1:/)
  assert.doesNotMatch(row.request_json, /example|synthetic/)
  f.respond((_url, options) => {
    const intent = JSON.parse(options.body)
    assert.equal(intent.url, url)
    return receipt(intent)
  })
  await flushQueuedRssSources()
  assert.equal(f.count('local_rss_source_outbox'), 0)
}))
