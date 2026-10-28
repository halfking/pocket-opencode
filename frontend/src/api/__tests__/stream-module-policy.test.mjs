import assert from 'node:assert/strict'
import { test } from 'node:test'
import { createServer } from 'vite'
import { createPinia, setActivePinia } from 'pinia'

const tick = () => new Promise(resolve => setTimeout(resolve, 0))
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r }); return { promise, resolve } }
const sse = content => new Response(`data: ${JSON.stringify({ content, done: false })}\n\ndata: [DONE]\n\n`, {
  headers: { 'content-type': 'text/event-stream' },
})

async function fixture(run) {
  const saved = new Map(['localStorage', 'window', 'fetch'].map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]))
  const values = new Map()
  const storage = { getItem: k => values.get(k) ?? null, setItem: (k, v) => values.set(k, v), removeItem: k => values.delete(k) }
  Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: storage })
  const window = new EventTarget()
  window.location = { origin: 'http://localhost' }
  Object.defineProperty(globalThis, 'window', { configurable: true, value: window })
  delete globalThis.__openpocket_aiStreamRuntime__
  delete globalThis.__openpocket_appLifecycleHub__
  setActivePinia(createPinia())
  const requests = []
  globalThis.fetch = async (url, init) => {
    requests.push({ url: String(url), ...init })
    return runFetch(String(url), init)
  }
  let runFetch = async () => sse('answer')
  const vite = await createServer({ configFile: false, server: { middlewareMode: true, hmr: false, ws: false }, optimizeDeps: { noDiscovery: true }, plugins: [{
    name: 'stream-local-search', enforce: 'pre',
    resolveId(source, importer) {
      if (source === './notes-search' && importer?.endsWith('/note-search.ts')) return 'virtual:stream-note-search'
    },
    load(id) { if (id === 'virtual:stream-note-search') return 'export const searchHybrid = async () => [{ note: {id:"n1",title:"Private",content:"private note body",summary:"private note body"},score:1 }]' },
  }] })
  let runtime
  try {
    const { useAuthStore } = await vite.ssrLoadModule('/src/stores/auth.ts')
    const auth = useAuthStore()
    await auth.setAuthWithWorkspace('A-token', 'Alice', 'a', 'wa')
    auth.maybeRefresh = async () => {}
    const policy = await vite.ssrLoadModule('/src/stores/syncPolicyStore.ts')
    policy.installSyncPolicyStorage(storage)
    const mode = (module, value) => policy.useSyncPolicyStore().applyPolicy({ system_default: 'cloud', allow_override: true, modules: { [module]: value } }, 1)
    const bff = await vite.ssrLoadModule('/src/api/llm-bff.ts')
    const rt = await vite.ssrLoadModule('/src/native/aiStreamRuntime.ts')
    runtime = rt.aiStreamRuntime
    bff.installLlmBffStreamRuntime()
    const start = (input = {}, id) => {
      const deltas = [], errors = []
      let done
      const settled = new Promise(resolve => { done = resolve })
      const handle = bff.llmBffApi.streamChat({ messages: [{ role: 'user', content: 'private' }], ...input }, {
        onDelta: delta => { if (delta.content) deltas.push(delta.content) },
        onDone: () => done(), onError: error => { errors.push(error); done() },
      }, id)
      return { handle, deltas, errors, settled }
    }
    await run({ vite, auth, policy, mode, requests, start, runtime, setFetch: fn => { runFetch = fn } })
  } finally {
    for (const id of runtime?.listStreamIds() ?? []) runtime.abort(id)
    runtime?.stop()
    await vite.close()
    for (const [key, descriptor] of saved) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor)
      else delete globalThis[key]
    }
    delete globalThis.__openpocket_aiStreamRuntime__
    delete globalThis.__openpocket_appLifecycleHub__
  }
}

test('actual note and meeting streams reject local mode without sending content; note search keeps its local fallback', () => fixture(async f => {
  f.mode('notes', 'local')
  const notes = await f.vite.ssrLoadModule('/src/features/notes/note-search.ts')
  await assert.rejects(notes.extractTagsWithAi('private note'), error => error.body?.error === 'module_policy_local')
  const search = await notes.searchNotesWithIntent('private', 'wa')
  assert.equal(search.offline, true)
  assert.equal(search.results.length, 1)
  f.mode('meetings', 'local')
  const meetings = await f.vite.ssrLoadModule('/src/features/meetings/meetings-ai.ts')
  await assert.rejects(meetings.summarizeMeeting('private transcript'), error => error.body?.error === 'module_policy_local')
  assert.equal(f.requests.length, 0)
}))

test('ordinary chat remains usable and the client-only owner is absent from the network payload', () => fixture(async f => {
  f.mode('notes', 'cloud')
  const owned = f.start({ syncModule: 'notes' })
  await owned.settled
  assert.deepEqual(owned.deltas, ['answer'])
  assert.equal('syncModule' in JSON.parse(f.requests[0].body), false)
  f.mode('notes', 'local')
  const general = f.start()
  await general.settled
  assert.deepEqual(general.deltas, ['answer'])
  assert.equal(f.requests.length, 2)
}))

test('a local decision during refresh blocks the actual stream send', () => fixture(async f => {
  f.mode('notes', 'cloud')
  const wait = deferred()
  f.auth.maybeRefresh = () => wait.promise
  const stream = f.start({ syncModule: 'notes' })
  f.mode('notes', 'local')
  wait.resolve()
  await stream.settled
  assert.equal(f.requests.length, 0)
  assert.equal(stream.errors[0]?.body?.error, 'module_policy_local')
}))

test('a same-session 401 replay uses the renewed credential, never the captured old token', () => fixture(async f => {
  f.mode('notes', 'cloud')
  let calls = 0
  f.setFetch(async url => {
    if (url.endsWith('/api/auth/refresh')) return new Response(JSON.stringify({ token: 'A-renewed', user: 'Alice', user_id: 'a', workspace_id: 'wa' }), { headers: { 'content-type': 'application/json' } })
    return ++calls === 1 ? new Response('{}', { status: 401 }) : sse('renewed')
  })
  const stream = f.start({ syncModule: 'notes' })
  await stream.settled
  const requests = f.requests.filter(r => r.url.endsWith('/api/llm/stream'))
  assert.deepEqual(requests.map(r => r.headers.Authorization), ['Bearer A-token', 'Bearer A-renewed'])
  assert.deepEqual(stream.deltas, ['renewed'])
}))

test('an old 401 cannot refresh or replay a stream under a new account', () => fixture(async f => {
  const response = deferred()
  f.setFetch(() => response.promise)
  const stream = f.start()
  await tick()
  await f.auth.setAuthWithWorkspace('B-token', 'Bob', 'b', 'wb')
  response.resolve(new Response('{}', { status: 401 }))
  await stream.settled
  assert.equal(f.requests.length, 1)
  assert.equal(stream.errors[0]?.name, 'AbortError')
  assert.equal(f.auth.token, 'B-token')
}))

test('local and identity changes cancel a pending reader and suppress late frames', () => fixture(async f => {
  for (const change of [() => f.mode('notes', 'local'), () => f.auth.setAuthWithWorkspace('B-token', 'Bob', 'b', 'wb')]) {
    f.mode('notes', 'cloud')
    let controller, cancelled = 0
    f.setFetch(async () => new Response(new ReadableStream({ start(c) { controller = c }, cancel() { cancelled++ } }), { headers: { 'content-type': 'text/event-stream' } }))
    const stream = f.start({ syncModule: 'notes' })
    await tick()
    await change()
    await Promise.race([stream.settled, new Promise((_, reject) => setTimeout(() => reject(new Error('stream did not stop')), 500))])
    assert.equal(cancelled, 1)
    assert.deepEqual(stream.deltas, [])
    assert.equal(stream.errors.length, 1)
    assert.throws(() => controller.enqueue(new TextEncoder().encode('data: {"content":"late","done":false}\n\n')))
  }
}))

test('the same stream id is isolated between login generations and old replay is inaccessible', () => fixture(async f => {
  const first = f.start({}, 'shared-chat')
  await first.settled
  await f.auth.setAuthWithWorkspace('B-token', 'Bob', 'b', 'wb')
  const replay = []
  const replayErrors = []
  f.runtime.subscribe('shared-chat', { onDelta: d => replay.push(d), onError: e => replayErrors.push(e) })
  assert.deepEqual(replay, [])
  assert.equal(replayErrors[0]?.name, 'AbortError')
  const second = f.start({}, 'shared-chat')
  await second.settled
  assert.equal(f.requests.at(-1).headers.Authorization, 'Bearer B-token')
  assert.deepEqual(second.deltas, ['answer'])
}))

test('a pending response is rejected after local is selected, and a pending refresh cannot replay it', () => fixture(async f => {
  for (const firstStatus of [200, 401]) {
    f.mode('notes', 'cloud')
    const response = deferred(), refresh = deferred()
    let refreshStarted = false
    f.setFetch(url => {
      if (url.endsWith('/api/auth/refresh')) { refreshStarted = true; return refresh.promise }
      return response.promise
    })
    const stream = f.start({ syncModule: 'notes' })
    await tick()
    if (firstStatus === 401) {
      response.resolve(new Response('{}', { status: 401 }))
      while (!refreshStarted) await tick()
    }
    f.mode('notes', 'local')
    if (firstStatus === 200) response.resolve(sse('private'))
    refresh.resolve(new Response(JSON.stringify({ token: 'A-renewed', user_id: 'a', workspace_id: 'wa' }), { headers: { 'content-type': 'application/json' } }))
    await stream.settled
    assert.deepEqual(stream.deltas, [])
    assert.equal(stream.errors[0]?.body?.error, 'module_policy_local')
  }
  assert.equal(f.requests.filter(r => r.url.endsWith('/api/llm/stream')).length, 2)
}))

test('a policy change in one frame suppresses later private frames in the same chunk', () => fixture(async f => {
  f.mode('notes', 'cloud')
  f.setFetch(async () => new Response('data: {"content":"first","done":false}\n\ndata: {"content":"second","done":false}\n\ndata: [DONE]\n\n', { headers: { 'content-type': 'text/event-stream' } }))
  const { llmBffApi } = await f.vite.ssrLoadModule('/src/api/llm-bff.ts')
  const chunks = [], errors = []
  await new Promise(resolve => {
    llmBffApi.streamChat({ syncModule: 'notes', messages: [] }, {
      onDelta(d) { chunks.push(d.content); f.mode('notes', 'local') },
      onDone: resolve, onError(e) { errors.push(e); resolve() },
    })
  })
  assert.deepEqual(chunks, ['first'])
  assert.equal(errors[0]?.body?.error, 'module_policy_local')
}))

test('an old handle cannot cancel a replacement stream with the same id in a new login', () => fixture(async f => {
  f.setFetch(async () => new Response(new ReadableStream({ start() {}, cancel() {} }), { headers: { 'content-type': 'text/event-stream' } }))
  const old = f.start({}, 'running-shared-chat')
  await tick()
  await f.auth.setAuthWithWorkspace('B-token', 'Bob', 'b', 'wb')
  const next = f.start({}, 'running-shared-chat')
  await tick()
  assert.notEqual(old.handle, next.handle)
  assert.equal(old.handle.abort(), false)
  assert.equal(next.handle.status(), 'running')
  next.handle.abort()
  await next.settled
}))

for (const change of ['account', 'policy']) {
  for (const event of ['onDelta', 'onRetry', 'onDone', 'onError']) {
    test(`${event} fanout rechecks ${change} between raw runtime subscribers`, () => fixture(async f => {
      f.mode('notes', 'cloud')
      const payload = event === 'onRetry' ? { retry: 'private-model', done: false }
        : event === 'onError' ? { error: 'private-error', done: false }
          : { content: 'private-answer', usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 }, done: false }
      f.setFetch(async () => new Response(`data: ${JSON.stringify(payload)}\n\ndata: [DONE]\n\n`, { headers: { 'content-type': 'text/event-stream' } }))
      const switchContext = () => change === 'account'
        ? f.auth.setAuthWithWorkspace('B-token', 'Bob', 'b', 'wb') : f.mode('notes', 'local')
      const input = { syncModule: 'notes', messages: [{ role: 'user', content: 'secret' }] }
      const seen = [], errors = []
      let settle
      const done = new Promise(resolve => { settle = resolve })
      f.runtime.spawnChat(`fanout-${event}`, input, { [event]: switchContext })
      f.runtime.spawnChat(`fanout-${event}`, input, {
        [event]: value => { seen.push(value); settle() },
        ...(event === 'onError' ? {} : { onError: error => { errors.push(error); settle() } }),
      })
      await done
      if (event === 'onError') {
        assert.equal(seen.length, 1)
        assert.equal(change === 'account' ? seen[0]?.name : seen[0]?.body?.error, change === 'account' ? 'AbortError' : 'module_policy_local')
      } else {
        assert.deepEqual(seen, [])
        assert.equal(change === 'account' ? errors[0]?.name : errors[0]?.body?.error, change === 'account' ? 'AbortError' : 'module_policy_local')
      }
    }))
  }
  test(`replay does not deliver final usage after its last delta changes ${change}`, () => fixture(async f => {
    f.mode('notes', 'cloud')
    f.setFetch(async () => new Response('data: {"content":"answer","done":false,"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}\n\ndata: [DONE]\n\n', { headers: { 'content-type': 'text/event-stream' } }))
    const first = f.start({ syncModule: 'notes' }, 'completed-with-usage')
    await first.settled
    const done = [], errors = []
    f.runtime.subscribe('completed-with-usage', {
      onDelta() { if (change === 'account') f.auth.setAuthWithWorkspace('B-token', 'Bob', 'b', 'wb'); else f.mode('notes', 'local') },
      onDone: usage => done.push(usage), onError: error => errors.push(error),
    })
    assert.deepEqual(done, [])
    assert.equal(change === 'account' ? errors[0]?.name : errors[0]?.body?.error, change === 'account' ? 'AbortError' : 'module_policy_local')
  }))
}

test('subscriber exceptions remain isolated during live and rejected replay delivery', () => fixture(async f => {
  f.mode('notes', 'cloud')
  const input = { syncModule: 'notes', messages: [] }
  const seen = []
  let settle
  const done = new Promise(resolve => { settle = resolve })
  f.runtime.spawnChat('throwing-subscriber', input, { onDelta() { throw new Error('subscriber failure') } })
  f.runtime.spawnChat('throwing-subscriber', input, { onDelta: d => seen.push(d.content), onDone: settle })
  await done
  assert.deepEqual(seen, ['answer'])
  await f.auth.setAuthWithWorkspace('B-token', 'Bob', 'b', 'wb')
  assert.doesNotThrow(() => f.runtime.subscribe('throwing-subscriber', { onError() { throw new Error('error handler failure') } }))
}))

test('user cancellation preserves its reason with the guarded production fetcher', () => fixture(async f => {
  f.mode('notes', 'cloud')
  f.setFetch(async () => new Response(new ReadableStream({ start() {} }), { headers: { 'content-type': 'text/event-stream' } }))
  const errors = []
  const handle = f.runtime.spawnChat('user-stop', { syncModule: 'notes', messages: [] }, { onError: (error, reason) => errors.push({ error, reason }) })
  await tick()
  handle.abort()
  assert.equal(errors.length, 1)
  assert.equal(errors[0].reason, 'user')
  assert.equal(errors[0].error.message, '已停止')
}))
