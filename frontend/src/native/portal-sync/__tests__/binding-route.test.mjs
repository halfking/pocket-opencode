import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fixture, gate } from '../../../test-support/notes-sqlite-fixture.mjs'

function binding(f) {
  return { binding: 'authority-signed-binding', user_id: f.auth.userId,
    workspace_id: f.auth.workspaceId, tenant_id: 'tenant-a',
    expires_at: Math.floor(Date.now() / 1000) + 300 }
}

test('every portal module obtains its binding through the RedClaw authority bridge', () => fixture(async f => {
  localStorage.setItem('op_redclaw_portal_base', 'https://portal.example')
  await f.setModules({ notes: 'cloud', meetings: 'cloud', email: 'cloud', rss: 'cloud' })
  const { portalTransport } = await f.loadModule('/src/native/portal-sync/transport.ts')
  f.respond(async (url, options) => {
    assert.equal(options.headers.Authorization, 'Bearer token-A')
    if (url === 'https://portal.example/api/v1/portal/workspaces/workspace/binding') return binding(f)
    if (url.includes('/sync/v2/')) return { changes: [], next_cursor: '', epochs: { notes: 0 } }
    throw new Error('The ordinary pocket API does not accept a RedClaw admin token')
  })
  for (const module of ['notes', 'meetings', 'email', 'rss']) {
    const db = f.db.bindOperation(f.db.captureOperation({ kind: 'cloud-sync', module }))
    const context = await portalTransport(db, { module })
    await context.request(`changes?workspace_id=workspace&module=${module}`)
  }
  const calls = f.requests.filter(x => x.url.endsWith('/binding'))
  assert.equal(calls.length, 4)
  assert.ok(calls.every(x => x.url.startsWith('https://portal.example/api/v1/portal/workspaces/')))
  assert.ok(calls.every(x => x.options.redirect === 'error'))
}))

test('a former account cannot finish binding lookup or dispatch a portal write after A to B to A', () => fixture(async f => {
  const { portalTransport } = await f.loadModule('/src/native/portal-sync/transport.ts')
  const paused = gate(), original = binding(f)
  f.respond(async () => { await paused.pause(); return original })
  const db = f.db.bindOperation(f.db.captureOperation({ kind: 'cloud-sync', module: 'notes' }))
  const context = portalTransport(db)
  await paused.started
  await f.login('B'); await f.login('A')
  paused.release()
  await assert.rejects(context, { name: 'AbortError' })
  assert.equal(f.requests.filter(x => x.url.includes('/sync/v2/')).length, 0)
}))

test('binding failure never retries the ordinary pocket endpoint or clears the current session', () => fixture(async f => {
  const { portalTransport } = await f.loadModule('/src/native/portal-sync/transport.ts')
  f.respond(async () => { throw new Error('authority unavailable') })
  const db = f.db.bindOperation(f.db.captureOperation({ kind: 'cloud-sync', module: 'notes' }))
  await assert.rejects(portalTransport(db), /authority unavailable/)
  assert.equal(f.requests.length, 1)
  assert.match(f.requests[0].url, /\/api\/v1\/portal\/workspaces\/workspace\/binding$/)
  assert.equal(f.auth.token, 'token-A')
}))
