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

test('an invalid terminal replay response leaves the cursor usable and reports the failure', async t => {
  const { runtime, streams, polls, poll } = await fixture(t, { pollMs: 5 })
  let broken = true
  poll((req, res) => {
    const after = Number(new URL(req.url, 'http://localhost').searchParams.get('after'))
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify(broken ? { ok: true } : { events: after < 2 ? [event(2)] : [] }))
  })
  let snapshot
  runtime.subscribe('run-1', current => { snapshot = current })
  await until(() => streams.length === 1)
  send(streams[0], event(1, 'terminal', { event_type: 'run.terminal' }))
  await until(() => snapshot.reason === 'terminal')
  assert.equal(polls.length, 3)
  assert.equal(snapshot.lastSeq, 1)
  assert.equal(snapshot.notice, '暂时无法补读终态后的回执')
  broken = false
  runtime.refreshTerminal('run-1', runtime.captureAuthority())
  await until(() => snapshot.reason === 'terminal' && snapshot.lastSeq === 2)
  assert.match(polls[3].url, /after=1/)
  assert.equal(snapshot.notice, '')
})

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

test('terminal leaves the SSE but reads all later receipts and can refresh again without reopening SSE', async t => {
  const { runtime, streams, polls, poll } = await fixture(t, { pollMs: 5 })
  const receipts = [
    event(14, 'dispatch receipt', { event_type: 'dispatch.completed' }),
    event(15, 'review receipt', { event_type: 'task.review.rejected' }),
  ]
  poll((req, res) => {
    const after = Number(new URL(req.url, 'http://localhost').searchParams.get('after'))
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ events: receipts.filter(row => row.sequence > after) }))
  })
  let snapshot
  runtime.subscribe('run-1', current => { snapshot = current })
  await until(() => streams.length === 1)
  send(streams[0], event(13, 'terminal', { event_type: 'run.terminal' }))
  await until(() => snapshot.reason === 'terminal' && snapshot.lastSeq === 15 && streams[0].closed)
  assert.match(polls[0].url, /after=13/)
  assert.deepEqual(snapshot.events.map(row => row.seq), [13, 14, 15])
  receipts.push(event(16, 'late acknowledgement'))
  runtime.refreshTerminal('run-1', runtime.captureAuthority())
  await until(() => snapshot.reason === 'terminal' && snapshot.lastSeq === 16)
  assert.equal(streams.length, 1)
  assert.deepEqual(snapshot.events.map(row => row.seq), [13, 14, 15, 16])
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

test('EOF falls back to owned polling with the SSE cursor; overlap and late responses are discarded', async t => {
  const { runtime, streams, polls, poll } = await fixture(t, { pollMs: 10 })
  let pending
  const body = [event(2, 'same-event', { event_id: 'event-1' }), event(3, 'from-poll')]
  let count = 0
  let respond
  const ready = new Promise(resolve => { respond = resolve })
  poll((_req, res) => {
    count++
    if (count === 1) {
      res.writeHead(200, { 'Content-Type': 'application/json' })
      res.end(JSON.stringify({ events: body }))
    } else { pending = res; respond() }
  })
  let snapshot
  runtime.subscribe('run-1', value => { snapshot = value })
  await until(() => streams.length === 1)
  send(streams[0], event(1))
  await until(() => snapshot.lastSeq === 1)
  streams[0].res.end()
  await ready
  assert.match(polls[0].url, /after=1/)
  assert.equal(polls[0].headers.authorization, 'Bearer acc-a')
  assert.deepEqual(snapshot.events.map(e => e.seq), [1, 3])
  runtime.setAuthority(null)
  pending.end(JSON.stringify({ events: [event(4, 'late-private')] }))
  await delay(30)
  assert.equal(snapshot.events.length, 0)
  assert.equal(polls.length, 2)
})

test('oversized unterminated SSE frames are bounded and close before fallback', async t => {
  const { runtime, streams, polls } = await fixture(t, { maxFrameChars: 200 })
  let snapshot
  runtime.subscribe('run-1', value => { snapshot = value })
  await until(() => streams.length === 1)
  streams[0].res.write(`data: ${'x'.repeat(500)}`)
  await until(() => streams[0].closed && snapshot.mode === 'unavailable')
  assert.equal(snapshot.events.length, 0)
  assert.equal(polls.length, 1)
})

test('silence has a finite network budget even with an attached view', async t => {
  const { runtime, streams, polls } = await fixture(t, { networkIdleMs: 50 })
  let snapshot
  runtime.subscribe('run-1', value => { snapshot = value })
  await until(() => streams.length === 1)
  await until(() => streams[0].closed && snapshot.mode === 'unavailable')
  assert.equal(polls.length, 1)
})

test('a slow-drip poll body cannot renew the request deadline indefinitely', async t => {
  const { runtime, streams, polls, poll } = await fixture(t, { networkIdleMs: 50, pollMs: 10 })
  const timers = []
  t.after(() => timers.forEach(clearInterval))
  poll((_req, res) => {
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.write('[')
    const timer = setInterval(() => res.write(' '), 10)
    timers.push(timer)
    res.on('close', () => clearInterval(timer))
  })
  let snapshot
  runtime.subscribe('run-1', value => { snapshot = value })
  await until(() => streams.length === 1)
  streams[0].res.end()
  await until(() => snapshot.mode === 'unavailable', 'slow polling must exhaust a finite request budget')
  assert.equal(polls.length, 3)
  assert.equal(snapshot.events.length, 0)
})

test('an oversized terminal cannot be silently dropped while the connection stays live', async t => {
  const { runtime, streams } = await fixture(t, { maxReplayChars: 250 })
  let snapshot
  runtime.subscribe('run-1', value => { snapshot = value })
  await until(() => streams.length === 1)
  send(streams[0], event(1, 'x'.repeat(500), { event_type: 'run.terminal' }))
  await until(() => streams[0].closed, 'oversized events must fail closed rather than advancing a hidden cursor')
  assert.equal(snapshot.mode, 'unavailable')
  assert.equal(snapshot.events.length, 0)
  assert.equal(snapshot.lastSeq, 0)
})

test('process entry capacity never evicts attached readers, but reclaims an unobserved run', async t => {
  const { runtime, streams } = await fixture(t, { maxRuns: 2 })
  const detach = runtime.subscribe('run-1', () => {})
  runtime.subscribe('run-2', () => {})
  await until(() => streams.length === 2)
  let full
  runtime.subscribe('run-3', value => { full = value })
  assert.equal(full.mode, 'unavailable')
  assert.equal(streams.length, 2)
  detach()
  runtime.subscribe('run-3', () => {})
  await until(() => streams.length === 3 && streams[0].closed)
  assert.equal(streams[1].closed, false)
})

test('a projection gap resumes polling from the last delivered sequence, not the synthetic gap ID', async t => {
  const { runtime, streams, polls, poll } = await fixture(t)
  poll((_req, res) => {
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ events: [event(2), event(3, 'done', { event_type: 'run.terminal' })] }))
  })
  let snapshot
  runtime.subscribe('run-1', value => { snapshot = value })
  await until(() => streams.length === 1)
  send(streams[0], event(1))
  await until(() => snapshot.lastSeq === 1)
  send(streams[0], event(99, 'gap', { event_type: 'projection.gap' }))
  await until(() => snapshot.reason === 'terminal' && streams[0].closed)
  assert.match(polls[0].url, /after=1/)
  assert.deepEqual(snapshot.events.map(e => e.seq), [1, 2, 3])
})

test('oversized poll bodies fail within the byte cap without ingesting partial private data', async t => {
  const { runtime, streams, polls, poll } = await fixture(t, { maxPollBytes: 80, pollMs: 10 })
  poll((_req, res) => {
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ events: [event(2, 'oversized'.repeat(50))] }))
  })
  let snapshot
  runtime.subscribe('run-1', value => { snapshot = value })
  await until(() => streams.length === 1)
  send(streams[0], event(1))
  await until(() => snapshot.lastSeq === 1)
  streams[0].res.end()
  await until(() => snapshot.mode === 'unavailable')
  assert.equal(polls.length, 3)
  assert.deepEqual(snapshot.events.map(e => e.seq), [1])
  assert.equal(snapshot.lastSeq, 1)
})

test('CRLF delimiters and a multi-byte UTF-8 character may span separate network chunks', async t => {
  const { runtime, streams } = await fixture(t)
  let snapshot
  runtime.subscribe('run-1', value => { snapshot = value })
  await until(() => streams.length === 1)
  const value = event(1, '🦞中文')
  const bytes = Buffer.from(`id: 1\r\nevent: agent.log_line\r\ndata: ${JSON.stringify(value)}\r\n\r\n`)
  const emoji = bytes.indexOf(Buffer.from('🦞'))
  const ends = [5, 6, emoji + 1, emoji + 3, bytes.length - 1, bytes.length]
  let start = 0
  for (const end of ends) {
    streams[0].res.write(bytes.subarray(start, end))
    start = end
    await delay(5)
  }
  await until(() => snapshot.events.length === 1)
  assert.equal(snapshot.events[0].payload.text, '🦞中文')
})
