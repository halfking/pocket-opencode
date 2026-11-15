import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { test } from 'node:test'
import { setTimeout as delay } from 'node:timers/promises'
import { AccLogRuntime } from '../acc-log-runtime.ts'

async function until(predicate, message = 'condition was not reached') {
  const deadline = Date.now() + 2500
  while (!predicate()) {
    assert.ok(Date.now() < deadline, message)
    await delay(5)
  }
}

// Real loopback HTTP + fetch/ReadableStream. The server owns protocol fixtures,
// not the runtime's transport, parser, cursor, cancellation or replay logic.
async function fixture(t, limits = {}) {
  const streams = []
  const polls = []
  let poll = (_req, res) => { res.writeHead(404); res.end() }
  const server = createServer((req, res) => {
    if (req.url.includes('/events/stream')) {
      const stream = { req, res, closed: false }
      streams.push(stream)
      res.writeHead(200, { 'Content-Type': 'text/event-stream' })
      res.write(': connected\n\n')
      res.on('close', () => { stream.closed = true })
    } else {
      polls.push(req)
      poll(req, res)
    }
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const base = `http://127.0.0.1:${server.address().port}`
  const runtime = new AccLogRuntime({ detachGraceMs: 1000, authorityCheckMs: 10, ...limits })
  let valid = true
  const owner = (workspaceId = 'workspace-a', token = 'acc-a') => ({
    session: {}, workspaceId, base, token,
    assertCurrent() { assert.ok(valid, 'authority was revoked') },
  })
  runtime.setAuthority(owner())
  t.after(async () => {
    runtime.dispose()
    server.closeAllConnections()
    await new Promise(resolve => server.close(resolve))
  })
  return {
    runtime, streams, polls, owner,
    revoke() { valid = false },
    poll(handler) { poll = handler },
  }
}

function event(seq, text = `line-${seq}`, extra = {}) {
  return { sequence: seq, event_id: `event-${seq}`, event_type: 'agent.log_line',
    run_id: 'run-1', payload: { text }, ...extra }
}

function send(stream, value, newline = '\n') {
  stream.res.write(`id: ${value.sequence}${newline}event: ${value.event_type}${newline}data: ${JSON.stringify(value)}${newline}${newline}`)
}

test('detach/remount replays events received offscreen, without a second request or duplicates', async t => {
  const { runtime, streams } = await fixture(t)
  let first
  const detach = runtime.subscribe('run-1', snapshot => { first = snapshot })
  await until(() => streams.length === 1)
  send(streams[0], event(1))
  await until(() => first.events.length === 1)
  detach()
  send(streams[0], event(2, 'offscreen', { event_type: 'future.named.event' }), '\r\n')
  send(streams[0], event(2, 'duplicate'))
  await delay(20)
  let remounted
  runtime.subscribe('run-1', snapshot => { remounted = snapshot })
  await until(() => remounted.events.length === 2)
  assert.equal(streams.length, 1)
  assert.equal(streams[0].closed, false)
  assert.deepEqual(remounted.events.map(e => e.payload.text), ['line-1', 'offscreen'])
  assert.equal(remounted.events[1].eventType, 'future.named.event')
  assert.equal(first.events.length, 1, 'detached view receives no further updates')
})

test('terminal closes the actual reader and replays its final state without restarting', async t => {
  const { runtime, streams } = await fixture(t)
  let snapshot
  const detach = runtime.subscribe('run-1', value => { snapshot = value })
  await until(() => streams.length === 1)
  send(streams[0], event(1, 'finished', { event_type: 'run.terminal', new_state: 'succeeded' }))
  await until(() => snapshot.reason === 'terminal' && streams[0].closed)
  detach()
  runtime.subscribe('run-1', value => { snapshot = value })
  assert.equal(snapshot.reason, 'terminal')
  assert.equal(snapshot.events.length, 1)
  assert.equal(streams.length, 1)
})

test('workspace/authority changes erase replay, close old transport and isolate identical run IDs', async t => {
  const { runtime, streams, owner } = await fixture(t)
  let oldView
  runtime.subscribe('run-1', value => { oldView = value })
  await until(() => streams.length === 1)
  send(streams[0], event(1, 'private-a'))
  await until(() => oldView.events.length === 1)
  const staleOwner = runtime.captureAuthority()
  runtime.setAuthority(owner('workspace-b', 'acc-b'))
  assert.equal(oldView.events.length, 0, 'invalidate visible private rows synchronously')
  await until(() => streams[0].closed)
  let newView
  runtime.subscribe('run-1', value => { newView = value })
  await until(() => streams.length === 2)
  assert.equal(streams[1].req.headers.authorization, 'Bearer acc-b')
  assert.equal(new URL(streams[1].req.url, 'http://fixture').searchParams.has('after'), false)
  assert.equal(runtime.cancel('run-1', staleOwner), false, 'late cancellation cannot stop a new authority')
  send(streams[1], event(1, 'private-b'))
  await until(() => newView.events.length === 1)
  assert.equal(newView.events[0].payload.text, 'private-b')
  assert.equal(oldView.events.length, 0)
})

test('revocation while no view is attached closes the socket and forbids stale replay', async t => {
  const { runtime, streams, revoke } = await fixture(t)
  let snapshot
  const detach = runtime.subscribe('run-1', value => { snapshot = value })
  await until(() => streams.length === 1)
  send(streams[0], event(1, 'private'))
  await until(() => snapshot.events.length === 1)
  detach()
  revoke()
  await until(() => streams[0].closed)
  runtime.subscribe('run-1', value => { snapshot = value })
  assert.equal(snapshot.events.length, 0)
  assert.equal(runtime.captureAuthority(), null)
  assert.equal(streams.length, 1)
})

test('explicit cancellation closes transport; detach alone does not cancel', async t => {
  const { runtime, streams } = await fixture(t)
  let snapshot
  const detach = runtime.subscribe('run-1', value => { snapshot = value })
  await until(() => streams.length === 1)
  detach()
  assert.equal(streams[0].closed, false)
  assert.equal(runtime.cancel('run-1', runtime.captureAuthority()), true)
  await until(() => streams[0].closed)
  runtime.subscribe('run-1', value => { snapshot = value })
  assert.equal(snapshot.reason, 'cancelled')
  assert.equal(runtime.cancel('run-1', runtime.captureAuthority()), false)
})

test('unobserved streams expire despite heartbeats; an observed stream outlives that grace', async t => {
  const { runtime, streams } = await fixture(t, { detachGraceMs: 70 })
  const detach = runtime.subscribe('run-1', () => {})
  await until(() => streams.length === 1)
  await delay(100)
  assert.equal(streams[0].closed, false, 'attached view must not be evicted by idle GC')
  detach()
  const heartbeat = setInterval(() => streams[0].res.write(': keepalive\n\n'), 10)
  t.after(() => clearInterval(heartbeat))
  await until(() => streams[0].closed)
  clearInterval(heartbeat)
  let snapshot
  runtime.subscribe('run-1', value => { snapshot = value })
  await until(() => streams.length === 2)
  assert.equal(snapshot.events.length, 0, 'expired replay and cursors were released')
})

test('run isolation and replay count/size bounds are enforced on actual SSE frames', async t => {
  const { runtime, streams } = await fixture(t, { maxEvents: 3, maxReplayChars: 500 })
  let snapshot
  runtime.subscribe('run-1', value => { snapshot = value })
  await until(() => streams.length === 1)
  send(streams[0], event(99, 'wrong-run', { run_id: 'run-other' }))
  for (let i = 1; i <= 8; i++) send(streams[0], event(i, 'x'.repeat(70)))
  await until(() => snapshot.lastSeq === 8)
  assert.ok(snapshot.events.length > 0 && snapshot.events.length <= 3)
  assert.ok(snapshot.events.reduce((sum, e) => sum + JSON.stringify(e).length, 0) <= 500)
  assert.equal(snapshot.events.at(-1).seq, 8)
  assert.ok(snapshot.events.every(e => e.payload.text !== 'wrong-run'))
})