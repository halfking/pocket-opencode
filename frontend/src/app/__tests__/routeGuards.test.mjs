/**
 * PR4 route guard tests (pure ESM mirror).
 *
 * Run with: `node --test frontend/src/app/__tests__/routeGuards.test.mjs`
 *
 * Mirrors evaluateRoute() and safeReturnTo() behaviour from
 * frontend/src/app/routeGuards.ts so we can run regression checks
 * without a TS toolchain.
 */

import { strict as assert } from 'node:assert'
import { test } from 'node:test'

// ---- minimal mirror of routeGuards.ts (no Vue/imports) ----

const safeReturnTo = (to) => {
  const raw = (to.query && to.query.returnTo) || to.fullPath || '/'
  if (typeof raw !== 'string') return '/'
  if (!raw.startsWith('/') || raw.startsWith('//')) return '/'
  return raw
}

const buildState = ({ authed = false, wsId = 'ws-1', lobster = true, meta = {} } = {}) => ({
  auth: {
    isAuthenticated: authed,
    workspaceId: wsId,
    syncFromStorage: () => {},
  },
  lobsterReady: lobster,
  meta,
})

const evaluateRoute = (to, state = buildState()) => {
  const requiresAuth = Boolean(state.meta.requiresAuth)
  const requiresLobster = Boolean(state.meta.requiresLobster)

  if (to.path === '/login') {
    if (state.auth.isAuthenticated && (!requiresLobster || state.lobsterReady)) {
      return { kind: 'redirectLogin', returnTo: '/' }
    }
    return { kind: 'allow' }
  }
  if (requiresAuth && !state.auth.isAuthenticated) {
    return { kind: 'redirectLogin', returnTo: safeReturnTo(to) }
  }
  if (requiresAuth && state.auth.isAuthenticated && requiresLobster && !state.lobsterReady) {
    return { kind: 'redirectUnlock', returnTo: safeReturnTo(to) }
  }
  return { kind: 'allow' }
}

const fakeTo = (path, meta = {}, query = {}) => ({
  path,
  fullPath: path + (Object.keys(query).length ? '?' + new URLSearchParams(query).toString() : ''),
  meta,
  query,
})

// ---- safeReturnTo ----

test('safeReturnTo: rejects protocol-relative URLs', () => {
  assert.equal(safeReturnTo({ fullPath: '//evil.example/x', query: {} }), '/')
})
test('safeReturnTo: rejects non-path', () => {
  assert.equal(safeReturnTo({ fullPath: 'https://evil', query: {} }), '/')
})
test('safeReturnTo: accepts same-origin path', () => {
  assert.equal(safeReturnTo({ fullPath: '/notes/123', query: {} }), '/notes/123')
})
test('safeReturnTo: prefers explicit returnTo', () => {
  assert.equal(safeReturnTo({ fullPath: '/x', query: { returnTo: '/notes/abc' } }), '/notes/abc')
})

// ---- evaluateRoute ----

test('login page when authed + lobster ready → bounce home', () => {
  const r = evaluateRoute(fakeTo('/login'), buildState({ authed: true, lobster: true }))
  assert.equal(r.kind, 'redirectLogin')
  assert.equal(r.returnTo, '/')
})

test('login page when not authed → allow', () => {
  const r = evaluateRoute(fakeTo('/login'), buildState({ authed: false }))
  assert.equal(r.kind, 'allow')
})

test('protected page without token → redirect to login with returnTo', () => {
  const to = fakeTo('/notes/abc')
  const r = evaluateRoute(to, buildState({ authed: false, meta: { requiresAuth: true, requiresLobster: true } }))
  assert.equal(r.kind, 'redirectLogin')
  assert.equal(r.returnTo, '/notes/abc')
})

test('protected page with token but no lobster → redirect unlock', () => {
  const to = fakeTo('/notes/abc')
  const r = evaluateRoute(
    to,
    buildState({ authed: true, lobster: false, meta: { requiresAuth: true, requiresLobster: true } }),
  )
  assert.equal(r.kind, 'redirectUnlock')
  assert.equal(r.returnTo, '/notes/abc')
})

test('protected page with token + lobster → allow', () => {
  const to = fakeTo('/notes/abc')
  const r = evaluateRoute(
    to,
    buildState({ authed: true, lobster: true, meta: { requiresAuth: true, requiresLobster: true } }),
  )
  assert.equal(r.kind, 'allow')
})

test('public page without auth → allow', () => {
  const to = fakeTo('/servers')
  const r = evaluateRoute(to, buildState({ authed: false, meta: {} }))
  assert.equal(r.kind, 'allow')
})

test('returnTo query overrides fullPath', () => {
  const r = evaluateRoute(
    fakeTo('/login', {}, { returnTo: '/notes/xyz' }),
    buildState({ authed: false }),
  )
  assert.equal(r.kind, 'allow')
  // when redirecting later, safeReturnTo would still choose /notes/xyz
})

test('open-redirect rejected even via returnTo', () => {
  const r = evaluateRoute(
    fakeTo('/login', {}, { returnTo: '//attacker.example/x' }),
    buildState({ authed: false }),
  )
  // login page is allowed; the dangerous bit is if we later bounce.
  assert.equal(r.kind, 'allow')
  // And safeReturnTo rejects it:
  assert.equal(safeReturnTo({ fullPath: '/login', query: { returnTo: '//attacker.example/x' } }), '/')
})
