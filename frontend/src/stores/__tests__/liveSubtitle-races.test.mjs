import assert from 'node:assert/strict'
import { after, test } from 'node:test'
import { createPinia, setActivePinia } from 'pinia'
import { loadSubtitleModules } from './subtitle-fixture.mjs'

const modules = await loadSubtitleModules()
after(() => modules.close())
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r }); return { promise, resolve } }

function fixture(t, automatic = false) {
  setActivePinia(createPinia())
  const auth = modules.useAuthStore()
  auth.$patch({ token: 'pocket-a', user: 'Alice', userId: 'alice', workspaceId: 'one', activationStatus: 'ready' })
  modules.installLiveSubtitleStorage({ getItem: () => null, setItem() {}, removeItem() {} })
  const store = modules.useLiveSubtitleStore()
  const previousFetch = globalThis.fetch, requests = []
  globalThis.fetch = (url, init) => {
    const result = deferred(), body = JSON.parse(init.body)
    requests.push({ url, init, body, result })
    if (automatic) result.resolve(Response.json({ translation: 'translated ' + body.text }))
    return result.promise
  }
  t.mock.timers.enable({ apis: ['setTimeout'] })
  t.after(() => { store.$dispose(); auth.$dispose(); globalThis.fetch = previousFetch })
  store.startSession('meeting-a')
  const add = text => store.addSegment({ meetingId: 'meeting-a', text, speaker: null, timestamp: 0 })
  const flush = async (ms = 0) => { t.mock.timers.tick(ms); for (let i = 0; i < 30; i++) await Promise.resolve() }
  const reply = (index, translation) => requests[index].result.resolve(Response.json({ translation }))
  return { store, auth, requests, add, flush, reply }
}

test('all pending segments translate once, with bounded concurrency', async t => {
  const f = fixture(t)
  for (const text of ['one', 'two', 'three', 'four', 'five']) f.add(text)
  f.store.toggleTranslation(); await f.flush(500)
  assert.equal(f.requests.length, 3)
  for (let i = 0; i < 3; i++) f.reply(i, 'translated ' + f.requests[i].body.text)
  await f.flush()
  assert.equal(f.requests.length, 5)
  for (let i = 3; i < 5; i++) f.reply(i, 'translated ' + f.requests[i].body.text)
  await f.flush()
  assert.deepEqual(f.store.segments.map(s => s.translation), ['one','two','three','four','five'].map(s => 'translated ' + s))
  assert.ok(f.requests.every(r => r.url === '/api/meetings/meeting-a/translate' && r.init.headers.Authorization === 'Bearer pocket-a'))
})

test('late old-language response cannot replace the current translation or clear its spinner', async t => {
  const f = fixture(t)
  f.add('hello'); f.store.toggleTranslation(); await f.flush(500)
  f.store.setTargetLang('ja'); await f.flush(500)
  assert.deepEqual(f.requests.map(r => r.body.targetLang), ['zh', 'ja'])
  f.reply(0, 'old Chinese'); await f.flush()
  assert.equal(f.store.segments[0].translation, undefined)
  assert.equal(f.store.segments[0].translating, true)
  f.reply(1, 'current Japanese'); await f.flush()
  assert.equal(f.store.segments[0].translation, 'current Japanese')
  assert.equal(f.store.segments[0].translating, false)
  assert.equal(f.requests[0].init.signal.aborted, true)
})

test('a stopped meeting cancels queued work before dispatch', async t => {
  const f = fixture(t)
  f.add('old meeting text'); f.store.toggleTranslation(); f.store.endSession()
  await f.flush(500)
  assert.equal(f.requests.length, 0)
  assert.equal(f.store.segments.length, 1, 'retained history is preserved')
})

test('same meeting id after restart does not admit an earlier response', async t => {
  const f = fixture(t)
  f.add('old text'); f.store.toggleTranslation(); await f.flush(500)
  const old = f.store.segments[0]
  f.store.endSession(); f.store.startSession('meeting-a'); f.add('new text')
  f.reply(0, 'stale'); await f.flush()
  assert.equal(old.translation, undefined)
  assert.equal(f.store.segments[0].text, 'new text')
  assert.equal(f.store.segments[0].translation, undefined)
})

test('owner ABA synchronously clears old text and fences response delivery', async t => {
  const f = fixture(t)
  f.add('private text'); f.store.toggleTranslation(); await f.flush(500)
  f.auth.token = 'pocket-b'; f.auth.token = 'pocket-a'
  assert.deepEqual(f.store.segments, [])
  assert.equal(f.store.activeMeetingId, null)
  assert.equal(f.store.visible, false)
  assert.equal(f.requests[0].init.signal.aborted, true)
  f.reply(0, 'private old response'); await f.flush()
  assert.deepEqual(f.store.segments, [])
})

test('new text supersedes an in-flight translation of the same segment', async t => {
  const f = fixture(t)
  f.add('draft'); f.store.toggleTranslation(); await f.flush(500)
  f.store.updateSegment(f.store.segments[0].id, { text: 'corrected' }); await f.flush(500)
  f.reply(1, 'current'); await f.flush(); f.reply(0, 'old'); await f.flush()
  assert.equal(f.store.segments[0].translation, 'current')
})

test('an owner change during JSON parsing cannot publish into retained segment references', async t => {
  const f = fixture(t), body = deferred()
  f.add('private body'); f.store.toggleTranslation(); await f.flush(500)
  const old = f.store.segments[0]
  f.requests[0].result.resolve({ ok: true, json: () => body.promise })
  await f.flush()
  f.auth.workspaceId = 'two'
  assert.deepEqual(f.store.segments, [])
  body.resolve({ translation: 'old owner data' }); await f.flush()
  assert.equal(old.translation, undefined)
  assert.deepEqual(f.store.segments, [])
})
