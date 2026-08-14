/**
 * Hardened auth + lobster route guard helpers (PR4 of optimization v4).
 *
 * Implements the split laid out in
 *   docs/优化v4/15-PR1-契约冻结与发布前置.md §3.2
 *   docs/优化v4/08-移动端UI与交互规范.md §4.2
 *
 * Four mutually exclusive guard outcomes:
 *   - allow
 *   - redirectLogin(returnTo)
 *   - redirectUnlock(returnTo)   (authenticated but lobster uninitialized)
 *   - blockTerminal(reason)       (rare; e.g., unknown meta without auth)
 *
 * PR4 deliberately keeps the existing router-mobile.ts `beforeEach`
 * untouched in shape: this module exposes a single helper that the
 * router calls. Behaviour changes from previous logic:
 *   -401  was a single jump to /login; we now also handle the
 *    `authenticated but lobster not yet initialised` case by routing to
 *    a dedicated unlock screen that the page can render inline.
 *   - The `redirect` query is preserved as `returnTo` and survives
 *    page reloads (stored in localStorage as fallback).
 *   - If a page requires auth and the route has no `requiresLobster`
 *    hint, we still let the page render; the missing initialization is
 *    surfaced through AsyncState, not the guard.
 *
 * Test mirror at __tests__/routeGuards.test.mjs runs without TS deps.
 */

import type { RouteLocationNormalized, NavigationGuardNext } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { isLobsterReady } from '../native/lobster-init'

export type GuardOutcome =
  | { kind: 'allow' }
  | { kind: 'redirectLogin'; returnTo: string }
  | { kind: 'redirectUnlock'; returnTo: string }
  | { kind: 'blockTerminal'; reason: string }

const FALLBACK_RETURN_KEY = 'pocket:lastRoute'

function safeReturnTo(to: RouteLocationNormalized): string {
  const raw = (to.query?.returnTo as string | undefined) || to.fullPath || '/'
  // Only allow same-origin paths to avoid open-redirect.
  if (typeof raw !== 'string') return '/'
  if (!raw.startsWith('/') || raw.startsWith('//')) return '/'
  return raw
}

export function evaluateRoute(
  to: RouteLocationNormalized,
): GuardOutcome {
  const auth = useAuthStore()
  // Always reflect the latest persisted auth state before evaluating.
  if (typeof auth.syncFromStorage === 'function') {
    auth.syncFromStorage()
  }

  const requiresAuth = Boolean((to.meta as Record<string, unknown>)?.requiresAuth)
  const requiresLobster = Boolean((to.meta as Record<string, unknown>)?.requiresLobster)
  const title = (to.meta as Record<string, unknown>)?.title as string | undefined

  // Case A: login page — bounce authenticated + ready users home.
  if (to.path === '/login') {
    if (auth.isAuthenticated && (!requiresLobster || isLobsterReady())) {
      return { kind: 'redirectLogin', returnTo: '/' }
    }
    return { kind: 'allow' }
  }

  // Case B: needs auth but no token.
  if (requiresAuth && !auth.isAuthenticated) {
    return { kind: 'redirectLogin', returnTo: safeReturnTo(to) }
  }

  // Case C: authenticated but lobster not ready AND page explicitly
  //         requires lobster (notes / email / vault / meetings / pkm).
  if (requiresAuth && auth.isAuthenticated && requiresLobster && !isLobsterReady()) {
    return { kind: 'redirectUnlock', returnTo: safeReturnTo(to) }
  }

  // Case D: authenticated without workspace_id claim (rare post-login race).
  if (requiresAuth && auth.isAuthenticated && !auth.workspaceId) {
    // The /servers picker or workspace chooser will resolve it; do not
    // block, but log a diagnostic for QA.
    if (typeof console !== 'undefined' && title) {
      console.debug('[guard] missing workspaceId for', to.path)
    }
  }

  return { kind: 'allow' }
}

/**
 * Apply the outcome to the router. Centralised so behaviour changes
 * (e.g., adding analytics) can be made in one place.
 */
export function applyOutcome(
  outcome: GuardOutcome,
  to: RouteLocationNormalized,
  next: NavigationGuardNext,
): void {
  switch (outcome.kind) {
    case 'allow':
      // Persist the last successfully navigated route for diagnostics.
      if (typeof window !== 'undefined') {
        try {
          window.localStorage.setItem(FALLBACK_RETURN_KEY, to.fullPath)
        } catch (_) {
          /* ignore quota / private mode */
        }
      }
      next()
      return
    case 'redirectLogin':
      next({ path: '/login', query: { returnTo: outcome.returnTo } })
      return
    case 'redirectUnlock':
      // Route to login with `unlock=1` flag so the login view can show
      // the local-init flow instead of credentials.
      next({ path: '/login', query: { returnTo: outcome.returnTo, unlock: '1' } })
      return
    case 'blockTerminal':
      // We do not have a dedicated terminal route today; route to home
      // with a banner-ready flag.
      next({ path: '/', query: { blocked: '1' } })
      return
  }
}

/** Convenience: combines evaluate + apply. */
export function runGuard(
  to: RouteLocationNormalized,
  next: NavigationGuardNext,
): void {
  applyOutcome(evaluateRoute(to), to, next)
}
