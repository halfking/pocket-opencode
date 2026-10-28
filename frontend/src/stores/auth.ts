/**
 * Auth store — replaces the hardcoded admin/admin login.
 *
 * Phase 7 enhancement: Added syncFromStorage() to support runtime localStorage updates
 * and automatic synchronization when storage changes in other tabs/windows.
 *
 * Phase 1 RedClaw switch (2026-09):
 *  - Token is now issued by RedClaw Admin (passed through openpocket backend)
 *  - logout() calls backend /api/auth/logout to revoke RedClaw session,
 *    then clears localStorage (idempotent if token already invalid)
 *  - authMethod records the login path (password / sso / dev-bypass / legacy)
 */
import { defineStore } from 'pinia'
import { effectScope, watch } from 'vue'
import { logoutRemote, refreshAuth } from '../api/auth.ts'
import { localDB } from '../native/local-db'

const TOKEN_KEY = 'pocket_token'
const USER_KEY = 'pocket_user'
const WS_KEY = 'pocket_workspace_id'
const METHOD_KEY = 'pocket_auth_method'
const SNAPSHOT_KEY = 'pocket_auth_snapshot_v1'

interface AuthSnapshot { token: string; user: string; userId: string; workspaceId: string; authMethod: string }
const emptySnapshot = (): AuthSnapshot => ({ token: '', user: '', userId: '', workspaceId: '', authMethod: '' })
function readStoredSession(): AuthSnapshot {
  const raw = localStorage.getItem(SNAPSHOT_KEY)
  if (raw !== null) {
    const value = JSON.parse(raw)
    if (value?.version !== 1 || !['token', 'user', 'workspaceId', 'authMethod'].every(key => typeof value[key] === 'string')) {
      throw new Error('Stored session is invalid; please sign in again.')
    }
    // Persisted metadata is not a verified owner assertion.
    return { token: value.token, user: value.user, userId: '', workspaceId: value.workspaceId, authMethod: value.authMethod }
  }
  // Compatibility recovery only: legacy independent keys cannot establish ownership.
  return { token: localStorage.getItem(TOKEN_KEY) || '', user: localStorage.getItem(USER_KEY) || '', userId: '',
    workspaceId: localStorage.getItem(WS_KEY) || '', authMethod: localStorage.getItem(METHOD_KEY) || '' }
}
function sameStoredSession(a: AuthSnapshot, b: AuthSnapshot): boolean {
  return a.token === b.token && a.user === b.user && a.workspaceId === b.workspaceId && a.authMethod === b.authMethod
}

/** token 剩余寿命低于该阈值（ms）时主动续期。 */
const REFRESH_AHEAD_MS = 5 * 60 * 1000

/**
 * Session handles remain outside serializable Pinia state. Each store and
 * login generation owns its refresh flight; a late response cannot replace
 * another login, including A→B→A with identical credentials.
 */
interface AuthIdentity {
  token: string
  user: string
  userId: string
  workspaceId: string
  authMethod: string
  maybeRefresh: () => Promise<void>
  refreshSession: () => Promise<boolean>
  activationStatus: 'ready' | 'pending' | 'error'
  clearLocal: () => Promise<void>
  $dispose: () => void
}

export interface AuthSession {
  readonly owner: AuthIdentity
  readonly generation: object
}

interface SessionState {
  generation: object
  applyingRefresh: boolean
  disposed: boolean
  refresh?: { session: AuthSession; promise: Promise<boolean> }
  activation?: { ticket: object; snapshot: AuthSnapshot; promise: Promise<void> }
}

const sessions = new WeakMap<AuthIdentity, SessionState>()

function sessionState(auth: AuthIdentity): SessionState {
  let state = sessions.get(auth)
  if (!state) {
    state = { generation: {}, applyingRefresh: false, disposed: false }
    sessions.set(auth, state)
    const current = state
    // The first caller may be a component: its unmount must not stop this watch.
    const scope = effectScope(true)
    scope.run(() => {
      // Synchronous observation also catches storage sync, $patch and A→B→A.
      watch(() => [auth.token, auth.user, auth.userId, auth.workspaceId, auth.authMethod], () => {
        if (!current.applyingRefresh) current.generation = {}
      }, { flush: 'sync' })
    })
    const dispose = auth.$dispose.bind(auth)
    auth.$dispose = () => {
      current.disposed = true
      current.generation = {}
      scope.stop()
      dispose()
    }
  }
  return state
}

/** Public sign-in attempts have a cancellation generation but grant no session admission. */
export function captureAuthAttempt(auth: AuthIdentity): AuthSession {
  return { owner: auth, generation: sessionState(auth).generation }
}
export function assertAuthAttempt(auth: AuthIdentity, session: AuthSession): void {
  const state = sessionState(auth)
  if (session.owner === auth && !state.disposed && state.generation === session.generation) return
  throw Object.assign(new Error('Sign-in attempt was superseded.'), { name: 'AbortError' })
}
export function captureAuthSession(auth: AuthIdentity): AuthSession {
  if (auth.activationStatus !== 'ready') throw Object.assign(new Error('Session activation is not ready.'), { name: 'AbortError' })
  return captureAuthAttempt(auth)
}

export function isCurrentAuthSession(auth: AuthIdentity, session: AuthSession): boolean {
  const state = sessionState(auth)
  return auth.activationStatus === 'ready' && session.owner === auth && !state.disposed && state.generation === session.generation
}

export function assertAuthSession(auth: AuthIdentity, session: AuthSession): void {
  if (isCurrentAuthSession(auth, session)) return
  // Existing request consumers already treat AbortError as cancellation.
  const error = new Error('The session changed before the request completed.')
  error.name = 'AbortError'
  throw error
}

function invalidateSession(auth: AuthIdentity): void {
  sessionState(auth).generation = {}
}

/** 解析 JWT payload 的 exp（秒→ms）；非 JWT / 无 exp 返回 null。 */
export function tokenExpiresAtMs(token: string): number | null {
  try {
    const payload = token.split('.')[1]
    if (!payload) return null
    const json = atob(payload.replace(/-/g, '+').replace(/_/g, '/'))
    const exp = JSON.parse(json)?.exp
    return typeof exp === 'number' ? exp * 1000 : null
  } catch {
    return null
  }
}

export const useAuthStore = defineStore('auth', {
  state: () => ({
    token: '',
    user: '',
    /** Restored workspace metadata does not assert historical data ownership. */
    workspaceId: '',
    /** S0-A: 服务端 user_id（JWT claim）。 */
    userId: '',
    /** Phase 1: 登录方式（password / sso / dev-bypass / legacy），仅用于埋点。 */
    authMethod: '',
    /**
     * ⚠§867/D33：会话**没能**写入本地存储。
     * 为 true 时，内存里是登录态而存储里没有 ⇒ 刷新/重启后会被静默登出。
     * 只置位 + warn，界面出口属产品口径（简报第 86 条）。
     */
    activationStatus: 'ready' as 'ready' | 'pending' | 'error',
    activationError: '',
    activationVersion: 0,
    sessionPersistFailed: false,
    /**
     * ⚠§870/D39：**删除**失败与写入失败同样要可见。
     * 为 true 时，「退出登录」没有真的把会话从本地存储里删掉。
     */
    sessionClearFailed: false,
    /** 未删掉的键名（可诊断用；界面出口属产品口径，简报第 89 条）。 */
    sessionClearMissing: [] as string[],
  }),
  getters: {
    isAuthenticated: (s) => s.activationStatus === 'ready' && Boolean(s.token) && Boolean(s.user),
  },
  actions: {
    /** Revoke admission immediately; publish only after the old DB work has drained. */
    activateSession(next: AuthSnapshot, persist: boolean, verifiedOwner = false, clear = false, failure?: unknown): Promise<void> {
      const state = sessionState(this)
      next = Object.freeze({ ...next })
      const ticket = {}
      invalidateSession(this)
      const activation = { ticket, snapshot: next, promise: Promise.resolve() }
      state.activation = activation
      activation.promise = localDB.requestOwnerActivation(
        verifiedOwner && next.userId && next.workspaceId ? { userId: next.userId, workspaceId: next.workspaceId } : null,
        () => {
          if (state.activation !== activation) throw Object.assign(new Error('Session activation superseded.'), { name: 'AbortError' })
          if (failure) throw failure
          if (persist && !this.writeSession([[TOKEN_KEY, next.token], [USER_KEY, next.user],
            [METHOD_KEY, next.authMethod], [WS_KEY, next.workspaceId]])) throw new Error('Session could not be saved. Please retry.')
          if (clear) {
            const failed: string[] = []
            for (const key of [TOKEN_KEY, USER_KEY, WS_KEY, METHOD_KEY]) {
              try { localStorage.removeItem(key) } catch (error) {
                failed.push(key)
                console.warn(`[auth] 本地会话键 ${key} 未能删除：`, error)
              }
            }
            this.sessionClearFailed = failed.length > 0
            this.sessionClearMissing = failed
            if (failed.length) throw new Error('Session cleanup failed. Please retry.')
          }
          state.applyingRefresh = true
          try {
            this.token = next.token
            this.user = next.user
            this.userId = next.userId
            this.workspaceId = next.workspaceId
            this.authMethod = next.authMethod
          } finally { state.applyingRefresh = false }
          return undefined
        },
      ).then(() => {
        if (state.activation !== activation) throw Object.assign(new Error('Session activation superseded.'), { name: 'AbortError' })
        this.activationVersion++
        if (state.activation !== activation || state.disposed) throw Object.assign(new Error('Session activation superseded.'), { name: 'AbortError' })
        this.activationStatus = 'ready'
        if (state.activation !== activation || state.disposed) throw Object.assign(new Error('Session activation superseded.'), { name: 'AbortError' })
        state.activation = undefined
      }).catch(error => {
        if (state.activation === activation) {
          this.activationError = error instanceof Error ? error.message : 'Session activation failed.'
          if (state.activation === activation) this.activationStatus = 'error'
          if (state.activation === activation) state.activation = undefined
        }
        throw error
      })
      // The DB is revoked before reactive pending/error watchers can re-enter.
      this.activationError = ''
      if (state.activation === activation) this.activationStatus = 'pending'
      return activation.promise
    },
    setAuth(token: string, user: string, method = 'password', extra: [string, string][] = []) {
      return this.activateSession({ token, user, userId: '', workspaceId: extra.find(([key]) => key === WS_KEY)?.[1] ?? '', authMethod: method }, true)
    },
    /** Publish the complete authoritative record before legacy compatibility keys.
     * A failed compatibility write remains visible, but cannot create a mixed snapshot.
     */
    writeSession(entries: [string, string][]) {
      try {
        const values = new Map(entries)
        localStorage.setItem(SNAPSHOT_KEY, JSON.stringify({ version: 1, revision: crypto.randomUUID(),
          token: values.get(TOKEN_KEY) ?? this.token, user: values.get(USER_KEY) ?? this.user,
          workspaceId: values.get(WS_KEY) ?? this.workspaceId, authMethod: values.get(METHOD_KEY) ?? this.authMethod }))
        // The atomic record is authoritative before compatibility keys can become torn.
        for (const [k, v] of entries) localStorage.setItem(k, v)
        this.sessionPersistFailed = false
        return true
      } catch (err) {
        this.sessionPersistFailed = true
        console.warn('[auth] 会话未能写入本地存储：', err)
        return false
      }
    },
    /** Metadata comes from the successful login response, never a decoded client JWT.
     * This lease identifies new operations; it does not assign historical database rows.
     */
    setAuthWithWorkspace(
      token: string,
      user: string,
      userId: string,
      workspaceId: string,
      method = 'password',
    ) {
      return this.activateSession({ token, user, userId, workspaceId, authMethod: method }, true, true)
    },
    /** Display-only changes preserve the verified owner and in-flight session. */
    updateDisplayUser(user: string) {
      const state = sessionState(this)
      if (this.activationStatus !== 'ready') throw new Error('Session activation is not ready.')
      state.applyingRefresh = true
      try { this.user = user; this.writeSession([[USER_KEY, user]]) }
      finally { state.applyingRefresh = false }
    },
    setLegacyUser(user: string) {
      return this.activateSession({ token: this.token, user, userId: '', workspaceId: '', authMethod: 'legacy' }, true)
    },
    /** Restore one committed snapshot and wait for activation. Storage events force
     * a new epoch even when queued A→B→A events already show identical final values.
     */
    syncFromStorage(force = false): Promise<void> {
      const state = sessionState(this)
      if (state.activation && !force) return state.activation.promise
      let next: AuthSnapshot
      try { next = readStoredSession() }
      catch (error) {
        return this.activateSession(emptySnapshot(), false, false, false, error)
      }
      if (!force && this.activationStatus === 'ready' && sameStoredSession(this, next)) return Promise.resolve()
      return this.activateSession(next, false)
    },
    /**
     * JWT 滑动续期（runbook §15.2）：单飞调 /api/auth/refresh 换新 token。
     * 成功返回 true 并更新 store + localStorage；失败返回 false（由调用方
     * 决定是否登出），不抛异常。
     */
    async refreshSession(): Promise<boolean> {
      if (!this.token || this.activationStatus !== 'ready') return false
      const state = sessionState(this)
      const session = captureAuthSession(this)
      if (state.refresh && isCurrentAuthSession(this, state.refresh.session)) return state.refresh.promise
      const flight = { session, promise: Promise.resolve(false) }
      flight.promise = (async () => {
        try {
          const res = await refreshAuth()
          if (!isCurrentAuthSession(this, session) || !res?.token) return false
          if ((this.userId && res.user_id && this.userId !== res.user_id)
            || (this.workspaceId && res.workspace_id && this.workspaceId !== res.workspace_id)) return false
          // 显示名保持不变：refresh 端点 legacy 路径只能从 claims 反推
          // user 字段（如 user-admin），覆盖会漂移 App 内的登录显示名。
          const displayUser = this.user || res.user || ''
          state.applyingRefresh = true
          try {
            this.token = res.token
            this.user = displayUser
            // Renewal does not upgrade unverified restored metadata into an owner assertion.
            // Missing identity/workspace requires an explicit activation, not a token refresh.
            this.writeSession([[TOKEN_KEY, this.token], [USER_KEY, this.user],
              [METHOD_KEY, this.authMethod], [WS_KEY, this.workspaceId]])
          } finally {
            state.applyingRefresh = false
          }
          return true
        } catch (e) {
          if (!isCurrentAuthSession(this, session)) return false
          console.debug('auth refresh failed:', e)
          return false
        } finally {
          if (state.refresh === flight) state.refresh = undefined
        }
      })()
      state.refresh = flight
      return flight.promise
    },
    /**
     * 临期主动续期：token 剩余寿命 <5min 时触发 refreshSession。
     * 不临期 / 无法解析 exp 时立即返回，绝不抛异常——可放在每个请求前调用。
     */
    async maybeRefresh(): Promise<void> {
      if (!this.token) return
      const exp = tokenExpiresAtMs(this.token)
      if (exp === null) return
      if (exp - Date.now() > REFRESH_AHEAD_MS) return
      await this.refreshSession()
    },
    /**
     * 只清本地登录态，不打后端。
     *
     * BUG-I (2026-09-30)：供 api/http.ts 在「401 且 refresh 失败」时调用。
     * 这种场景下后端必然拒绝 /api/auth/logout（token 已不可用），
     * 调它只会白等一轮网络超时，把用户卡在死胡同里。
     */
    clearLocal(): Promise<void> {
      // Keep an atomic anonymous snapshot: stale compatibility keys cannot resurrect a login.
      return this.activateSession(emptySnapshot(), true, false, true)
    },

    /**
     * Revoke local admission immediately while remote revocation uses its captured token.
     * A remote failure cannot restore the old login; a local activation failure stays visible.
     */
    async logout() {
      const token = this.token
      const remote = token ? logoutRemote(token).catch(error => { console.debug('logoutRemote failed (ignored):', error) }) : Promise.resolve()
      const cleared = this.clearLocal()
      try { await cleared } catch (error) {
        if (!(error instanceof Error) || error.name !== 'AbortError') throw error
      }
      await remote
    },
  },
})

/** Snapshot events revoke this runtime immediately. This does not coordinate
 * independent SQLite snapshots or make legacy multi-key writers atomic.
 */
if (typeof window !== 'undefined') {
  window.addEventListener('storage', (event) => {
    if (event.key === null || event.key === SNAPSHOT_KEY ||
      ((event.key === TOKEN_KEY || event.key === USER_KEY || event.key === WS_KEY || event.key === METHOD_KEY)
        && localStorage.getItem(SNAPSHOT_KEY) === null)) {
      try {
        const authStore = useAuthStore()
        void authStore.syncFromStorage(true).catch(error => console.warn('[auth] 会话恢复失败：', error))
      } catch (error) {
        console.debug('Auth store not initialized, skipping storage sync')
      }
    }
  })
}
