import { effectScope, watch } from 'vue'
import { assertAuthSession, type AuthSession, useAuthStore } from '../stores/auth'
import { resolveRuntimeApiBase } from '../config/api-base'
import { resolveRedclawPortalBase } from '../config/redclaw-portal'
import { fetchWithTimeout } from './fetch-timeout'
import { usesDirectPocketAuthority } from './authentication-mode'

export type PocketResourceScope = 'rss' | 'email-metadata' | 'embedding'
type Auth = ReturnType<typeof useAuthStore>
interface ResourceToken { token: string; expiresAt: number }
interface CachedToken { generation: object; promise: Promise<ResourceToken> }
const caches = new WeakMap<Auth, Map<string, CachedToken>>()

export class PocketResourceAuthorityError extends Error {
  constructor(readonly status: number, readonly code = '') { super('pocket_resource_authority_rejected') }
}
export class PocketResourceUnavailableError extends Error {
  constructor() { super('pocket_resource_unavailable') }
}

/** Login mode chooses one route, without decoding unverified JWT claims or
 * falling back after denial. Unknown modes use the restricted exchange. */
export function usesPocketResourceCredentials(auth: Pick<Auth, 'authMethod'>): boolean {
  return !usesDirectPocketAuthority(String(auth.authMethod ?? ''))
}

/** null is reserved for the existing authentication contract. Every other
 * pocket request needs a supported resource scope; new routes fail closed. */
export function pocketResourceScope(path: string, method = 'GET'): PocketResourceScope | 'unavailable' | null {
  const route = path.split('?')[0], verb = method.toUpperCase()
  // This logout handler explicitly proxies the captured RedClaw credential.
  // pocket /me, /refresh and /reset-password use pocket's own JWT validator.
  if (verb === 'POST' && route === '/api/auth/logout') return null
  if (/^\/api\/rss(?:\/|$)/.test(route)) return 'rss'
  if (route === '/api/embed') return verb === 'POST' ? 'embedding' : 'unavailable'
  if (/^\/api\/(?:email|emails)(?:\/|$)/.test(route)) {
    if (verb === 'GET' && (route === '/api/emails' || /^\/api\/email\/(?:accounts|folders|summaries(?:\/[^/]+)?)$/.test(route))) return 'email-metadata'
    return 'unavailable'
  }
  if (/^\/api\/(?:notes|meetings|llm)(?:\/|$)/.test(route)) return 'unavailable'
  return 'unavailable'
}

/** Legacy clients have no scoped-credential support. They cannot borrow a
 * RedClaw bearer, even when a newly added URL happens to resemble a resource. */
export function assertDirectPocketRequest(auth: Pick<Auth, 'authMethod'>): void {
  if (usesPocketResourceCredentials(auth)) throw new PocketResourceUnavailableError()
}

function cacheFor(auth: Auth): Map<string, CachedToken> {
  let cache = caches.get(auth)
  if (cache) return cache
  cache = new Map()
  caches.set(auth, cache)
  const owned = cache, scope = effectScope(true)
  scope.run(() => watch(() => [auth.token, auth.userId, auth.workspaceId, auth.authMethod, auth.activationVersion], () => owned.clear(), { flush: 'sync' }))
  const dispose = auth.$dispose.bind(auth)
  auth.$dispose = () => { owned.clear(); scope.stop(); dispose() }
  return cache
}

export interface PocketResourceCredential {
  readonly token: string
  readonly base: string
  assertCurrent(): void
  invalidate(): void
}

/** Tokens live only in memory, scoped to the original session, owner, module and
 * both resource origins. A failed exchange cannot borrow replacement identity. */
export async function pocketResourceCredential(
  auth: Auth,
  session: AuthSession,
  resource: PocketResourceScope,
  assertOperation: () => void,
): Promise<PocketResourceCredential> {
  const token = auth.token, user = auth.userId, workspace = auth.workspaceId
  const portal = resolveRedclawPortalBase(), base = resolveRuntimeApiBase()
  const assertOwner = () => {
    assertAuthSession(auth, session)
    if (!token || !user || !workspace || auth.token !== token || auth.userId !== user || auth.workspaceId !== workspace ||
      resolveRedclawPortalBase() !== portal || resolveRuntimeApiBase() !== base) {
      throw Object.assign(new Error('The pocket resource owner changed.'), { name: 'AbortError' })
    }
  }
  const assertCurrent = () => { assertOwner(); assertOperation(); assertOwner() }
  assertCurrent()
  const cache = cacheFor(auth), key = JSON.stringify([resource, token, user, workspace, portal, base])
  let entry = cache.get(key)
  const wasCached = !!entry && entry.generation === session.generation
  if (!entry || entry.generation !== session.generation) {
    const promise = (async () => {
      assertOwner()
      const response = await fetchWithTimeout(`${portal}/api/v1/portal/workspaces/${encodeURIComponent(workspace)}/resource-token`, {
        method: 'POST', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ scopes: [resource] }), redirect: 'error', credentials: 'omit',
      })
      assertOwner()
      if (!response.ok) {
        let code = ''
        try {
          const failure = await response.json()
          if (typeof failure?.error === 'string') code = failure.error
          else if (failure?.error && typeof failure.error === 'object' && typeof failure.error.code === 'string') code = failure.error.code
        } catch { /* An unreadable rejection never proves parent-session revocation. */ }
        assertOwner()
        throw new PocketResourceAuthorityError(response.status, code)
      }
      const body = await response.json()
      assertOwner()
      const now = Date.now() / 1000
      if (!body || typeof body.token !== 'string' || !body.token || body.token.length > 16384 || body.user_id !== user || body.workspace_id !== workspace ||
        typeof body.tenant_id !== 'string' || !body.tenant_id || !Array.isArray(body.scopes) || body.scopes.length !== 1 || body.scopes[0] !== resource ||
        !Number.isSafeInteger(body.issued_at) || !Number.isSafeInteger(body.expires_at) || body.issued_at > now + 5 ||
        body.expires_at <= now || body.expires_at <= body.issued_at || body.expires_at - body.issued_at > 30) {
        throw new PocketResourceUnavailableError()
      }
      return { token: body.token as string, expiresAt: body.expires_at as number }
    })()
    entry = { generation: session.generation, promise }
    cache.set(key, entry)
    const original = entry
    void promise.catch(() => { if (cache.get(key) === original) cache.delete(key) })
  }
  const value = await entry.promise
  assertCurrent()
  if (value.expiresAt <= Date.now() / 1000 + 3) {
    if (cache.get(key) === entry) cache.delete(key)
    if (wasCached) return pocketResourceCredential(auth, session, resource, assertOperation)
    throw new PocketResourceUnavailableError()
  }
  const original = entry
  return Object.freeze({ token: value.token, base, assertCurrent() {
    assertCurrent()
    if (value.expiresAt <= Date.now() / 1000) throw new PocketResourceUnavailableError()
  }, invalidate() { if (cache.get(key) === original) cache.delete(key) } })
}
