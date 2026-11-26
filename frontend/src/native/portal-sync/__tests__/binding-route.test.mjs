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

test('portal sync reauthentication retains the original and its pending operation', () => fixture(async f => {
  const { portalTransport } = await f.loadModule('/src/native/portal-sync/transport.ts')
  await f.setMode('local')
  await f.notes.createNote({ content: 'offline original', workspaceId: 'workspace' })
  const localStore = () => f.portalNotesStore(f.db.bindOperation(f.db.captureOperation({ kind: 'local-edit' })))
  const pending = await localStore().pending()
  assert.equal(pending.length, 1)
  await f.setMode('cloud')
  f.respond(async url => url.endsWith('/binding') ? binding(f) :
    Response.json({ error: { code: 'parent_session_invalid' } }, { status: 401 }))
  const db = f.db.bindOperation(f.db.captureOperation({ kind: 'cloud-sync', module: 'notes' }))
  const context = await portalTransport(db)
  await assert.rejects(context.transport.push(pending), /401/)
  assert.equal(f.auth.token, '')
  assert.equal(f.requests.filter(x => x.url.includes('/sync/v2/push')).length, 1)
  await f.login('B')
  assert.equal((await localStore().pending()).length, 0)
  await f.login('A')
  assert.deepEqual(await localStore().pending(), pending)
  assert.equal(f.count('local_notes'), 1)
}))

test('a confirmed parent rejection at binding acquisition prevents a sync request', () => fixture(async f => {
  const { portalTransport } = await f.loadModule('/src/native/portal-sync/transport.ts')
  f.respond(async () => Response.json({ error: { code: 'parent_session_invalid' } }, { status: 401 }))
  const db = f.db.bindOperation(f.db.captureOperation({ kind: 'cloud-sync', module: 'notes' }))
  await assert.rejects(portalTransport(db), { status: 401, code: 'parent_session_invalid' })
  assert.equal(f.auth.token, '')
  assert.equal(f.requests.length, 1)
  assert.match(f.requests[0].url, /\/binding$/)
}))

test('a workspace binding 401 does not clear the login during sync', () => fixture(async f => {
  const { portalTransport } = await f.loadModule('/src/native/portal-sync/transport.ts')
  f.respond(async url => url.endsWith('/binding') ? binding(f) :
    Response.json({ error: { code: 'unauthorized' } }, { status: 401 }))
  const db = f.db.bindOperation(f.db.captureOperation({ kind: 'cloud-sync', module: 'notes' }))
  const context = await portalTransport(db)
  await assert.rejects(context.transport.changes(''), /401/)
  assert.equal(f.auth.token, 'token-A')
}))

test('a late rejection for the previous token cannot clear a renewed login', () => fixture(async f => {
  await f.auth.setAuthWithWorkspace('token-A', 'A', 'user-A', 'workspace', 'legacy')
  await f.setMode('cloud')
  const { portalTransport } = await f.loadModule('/src/native/portal-sync/transport.ts')
  const paused = gate()
  f.respond(async url => {
    if (url.endsWith('/binding')) return binding(f)
    if (url.endsWith('/api/auth/refresh')) return { token: 'renewed-A', user_id: f.auth.userId, workspace_id: f.auth.workspaceId }
    await paused.pause()
    return Response.json({ error: { code: 'parent_session_invalid' } }, { status: 401 })
  })
  const db = f.db.bindOperation(f.db.captureOperation({ kind: 'cloud-sync', module: 'notes' }))
  const context = await portalTransport(db)
  const rejected = assert.rejects(context.transport.changes(''), { name: 'AbortError' })
  await paused.started
  assert.equal(await f.auth.refreshSession(), true)
  paused.release()
  await rejected
  assert.equal(f.auth.token, 'renewed-A')
}))

const nearExpiry = () => `header.${Buffer.from(JSON.stringify({ exp: Math.floor(Date.now() / 1000) + 60 })).toString('base64url')}.signature`
const beginSSO = async f => {
  await f.auth.setAuthWithWorkspace(nearExpiry(), 'A', 'user-A', 'workspace', 'redclaw-oidc')
  await f.setMode('cloud')
}
const renewalResult = extra => ({ token: 'renewed-A', user_id: 'user-A', workspace_id: 'workspace',
  auth_method: 'redclaw-oidc', ...extra })

test('portal sync renews the OIDC session before acquiring the workspace binding', () => fixture(async f => {
  await beginSSO(f)
  const old = f.auth.token, version = f.auth.activationVersion
  const { portalTransport } = await f.loadModule('/src/native/portal-sync/transport.ts')
  f.respond(async (url, init) => {
    if (url.endsWith('/api/auth/sso/refresh')) {
      assert.equal(init.headers.Authorization, `Bearer ${old}`)
      assert.deepEqual(JSON.parse(init.body), { workspace_id: 'workspace' })
      assert.equal(init.redirect, 'error')
      assert.equal(init.credentials, 'omit')
      return renewalResult()
    }
    assert.equal(init.headers.Authorization, 'Bearer renewed-A')
    if (url.endsWith('/binding')) return binding(f)
    if (url.includes('/sync/v2/changes')) return { changes: [] }
    assert.fail(`unexpected route ${url}`)
  })
  const db = f.db.bindOperation(f.db.captureOperation({ kind: 'cloud-sync', module: 'notes' }))
  const context = await portalTransport(db)
  await context.transport.changes('')
  assert.deepEqual(f.requests.map(x => new URL(x.url, 'https://app.example').pathname), [
    '/api/auth/sso/refresh', '/api/v1/portal/workspaces/workspace/binding', '/api/v1/portal/sync/v2/changes',
  ])
  assert.equal(f.auth.activationVersion, version)
  assert.equal(f.auth.token, 'renewed-A')
  assert.equal(JSON.parse(localStorage.getItem('pocket_auth_snapshot_v1')).token, 'renewed-A')
}))

test('a revoked renewal preserves the local note and pending operation across sign-in', () => fixture(async f => {
  await beginSSO(f)
  await f.setMode('local')
  await f.notes.createNote({ content: 'retained during SSO renewal', workspaceId: 'workspace' })
  const pending = () => f.portalNotesStore(f.db.bindOperation(f.db.captureOperation({ kind: 'local-edit' }))).pending()
  const before = await pending()
  assert.equal(before.length, 1)
  await f.setMode('cloud')
  const db = f.db.bindOperation(f.db.captureOperation({ kind: 'cloud-sync', module: 'notes' }))
  const { portalTransport } = await f.loadModule('/src/native/portal-sync/transport.ts')
  f.respond(async url => {
    assert.ok(url.endsWith('/api/auth/sso/refresh'))
    return Response.json({ code: 'parent_session_invalid', error: 'interactive sign-in required' }, { status: 401 })
  })
  await assert.rejects(portalTransport(db), { name: 'AbortError' })
  assert.equal(f.auth.token, '')
  assert.equal(f.requests.filter(x => x.url.endsWith('/binding')).length, 0)
  await f.login('B')
  assert.equal((await pending()).length, 0)
  await f.login('A')
  assert.deepEqual(await pending(), before)
  assert.equal(f.count('local_notes'), 1)
}))

for (const status of [200, 401]) {
  test(`late OIDC renewal (${status}) cannot replace or clear a later login with the same scope`, () => fixture(async f => {
    await beginSSO(f)
    const old = f.auth.token
    const paused = gate()
    f.respond(async url => {
      assert.ok(url.endsWith('/api/auth/sso/refresh'))
      await paused.pause()
      return status === 200 ? renewalResult() : Response.json({ code: 'reauth_required' }, { status })
    })
    const renewal = f.auth.refreshSession()
    await paused.started
    await f.login('B')
    await f.auth.setAuthWithWorkspace(old, 'A', 'user-A', 'workspace', 'redclaw-oidc')
    paused.release()
    assert.equal(await renewal, false)
    assert.equal(f.auth.token, old)
  }))
}

test('OIDC renewal rejects mismatched scope and keeps the current bearer after a stale response', () => fixture(async f => {
  await beginSSO(f)
  const old = f.auth.token
  for (const response of [
    renewalResult({ auth_method: 'casdoor-oidc' }),
    renewalResult({ user_id: 'user-B' }),
    renewalResult({ workspace_id: 'other' }),
    renewalResult({ workspace_id: undefined }),
    Response.json({ code: 'stale_session_token', error: 'another request already renewed' }, { status: 409 }),
  ]) {
    f.respond(async url => {
      assert.ok(url.endsWith('/api/auth/sso/refresh'))
      return response
    })
    assert.equal(await f.auth.refreshSession(), false)
    assert.equal(f.auth.token, old)
  }
  assert.equal(f.requests.filter(x => x.url.endsWith('/api/auth/sso/refresh')).length, 5)
}))
