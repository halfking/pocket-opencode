/**
 * 原生收信桥：Android 在后台线程 POST pocketd，不走 WebView IMAP。
 */
import { Capacitor, registerPlugin as capRegisterPlugin } from '@capacitor/core'
import { reactive, readonly } from 'vue'
import { captureAuthSession, isCurrentAuthSession, useAuthStore, type AuthSession } from '../../stores/auth'
import { readCachedModuleMode } from '../../stores/syncPolicyStore'

export interface NativeEmailFetchResult {
  used: boolean
  synced: number
  newCount: number
  classified: number
}

interface EmailFetchPlugin {
  configure(opts: { apiBase: string; token: string; enabled: boolean }): Promise<void>
  runNow(): Promise<{ synced?: number; newCount?: number; classified?: number }>
  schedule(opts: { intervalMs: number }): Promise<void>
  cancel(): Promise<void>
}

// BUG-G (2026-09-30)：Capacitor 的 registerPlugin() 返回**带 .then 的 thenable
// 代理**。把它从 async 函数 return、或当作 .then() 回调的返回值，promise 决议
// 都会去调它的 .then()，未实现的原生插件直接抛
//   Error: "EmailFetch.then()" is not implemented on android
// 这会让 email-fetch-run.ts 里写好的「原生不可用就静默降级为仅服务端拉取」
// 变成未捕获异常。一律用非 thenable 的盒子 { value } 装载。
type EmailFetchBox = { value: EmailFetchPlugin | null }

let plugin: EmailFetchBox | null = null
let loading: Promise<void> | null = null
let generation = 0
let grant: AuthSession | null = null
let cancellation: Promise<boolean> | null = null
let cancellationPending = false
let retryTimer: ReturnType<typeof setTimeout> | null = null
let failedAttempts = 0
const stopState = reactive({ unconfirmed: false, retrying: false })
export const nativeEmailStopState = readonly(stopState)
const STOP_RETRY_DELAYS = [1000, 5000]

function currentGrant(session: AuthSession): boolean {
  return useAuthStore() === session.owner && Boolean(session.owner.token) && isCurrentAuthSession(session.owner, session)
    && readCachedModuleMode('email') === 'cloud'
}

/** Persist denial and remove native credentials/alarm, including after logout. */
export function cancelNativeEmailFetch(): Promise<boolean> {
  generation++
  grant = null
  // Repeated policy/auth events must not reset an exhausted retry budget.
  if (cancellationPending || stopState.unconfirmed) return cancellation ?? Promise.resolve(false)
  return attemptCancellation()
}

/** User-requested recovery after the bounded automatic attempts are exhausted. */
export function retryNativeEmailFetchCancellation(): Promise<boolean> {
  generation++
  grant = null
  if (cancellationPending) return cancellation!
  if (retryTimer) clearTimeout(retryTimer)
  retryTimer = null
  failedAttempts = 0
  return attemptCancellation()
}

function attemptCancellation(): Promise<boolean> {
  cancellationPending = true
  stopState.retrying = true
  const pending = (async () => {
    try {
      const p = (await ensurePlugin())?.value
      if (p) await p.cancel()
      else if (Capacitor.getPlatform() === 'android') throw new Error('EmailFetch plugin unavailable')
      failedAttempts = 0
      stopState.unconfirmed = false
      return true
    } catch {
      stopState.unconfirmed = true
      const delay = STOP_RETRY_DELAYS[failedAttempts++]
      console.warn('[email] Native background email stop is unconfirmed; background access may remain until a stop succeeds.')
      if (delay !== undefined) {
        retryTimer = setTimeout(() => {
          retryTimer = null
          void attemptCancellation()
        }, delay)
      }
      return false
    } finally {
      cancellationPending = false
      stopState.retrying = false
    }
  })()
  cancellation = pending
  return pending
}

if (typeof window !== 'undefined') {
  window.addEventListener('openpocket:sync-policy-changed', () => {
    // This listener is loaded before the local database unlocks/host starts.
    // A new process must not inherit an unverified native account grant.
    if (!grant || !currentGrant(grant)) void cancelNativeEmailFetch()
  })
}

async function ensurePlugin(): Promise<EmailFetchBox | null> {
  // EmailFetch is implemented only on Android; iOS has no native grant to revoke.
  if (Capacitor.getPlatform() !== 'android') return null
  if (plugin) return plugin
  if (!loading) {
    // 回调不返回值：赋值结果若被当作 promise 决议值，会再次触发 thenable 陷阱
    loading = Promise.resolve().then(() => {
      let p: EmailFetchPlugin | null = null
      try {
        p = (capRegisterPlugin as <T>(name: string) => T)('EmailFetch')
      } catch {
        p = null
      }
      plugin = { value: p }
    })
  }
  await loading
  return plugin
}

export async function configureNativeEmailFetch(apiBase: string, token: string): Promise<boolean> {
  const auth = useAuthStore()
  const session = captureAuthSession(auth)
  if (auth.token !== token) return false
  if (!currentGrant(session) || !apiBase) {
    await cancelNativeEmailFetch()
    return false
  }
  if (!grant && !cancellation) void cancelNativeEmailFetch()
  const version = generation
  if (cancellation && !await cancellation) return false
  const box = await ensurePlugin()
  const p = box?.value
  if (!p || version !== generation || !currentGrant(session) || auth.token !== token) return false
  try {
    await p.configure({ apiBase: apiBase.replace(/\/$/, ''), token, enabled: true })
    if (version !== generation || !currentGrant(session) || auth.token !== token) {
      // The native configure may have completed after revocation was requested.
      await cancelNativeEmailFetch()
      return false
    }
    grant = session
    return true
  } catch {
    await cancelNativeEmailFetch()
    return false
  }
}

export async function scheduleNativeEmailFetch(intervalMs: number): Promise<boolean> {
  const session = grant
  const version = generation
  const p = (await ensurePlugin())?.value
  if (!p || !session || version !== generation || !currentGrant(session)) {
    if (session && grant === session && !currentGrant(session)) await cancelNativeEmailFetch()
    return false
  }
  try {
    await p.schedule({ intervalMs })
    return true
  } catch {
    return false
  }
}

export async function runNativeEmailFetch(): Promise<NativeEmailFetchResult> {
  const session = grant
  const version = generation
  const p = (await ensurePlugin())?.value
  if (!p || !session || version !== generation || !currentGrant(session)) {
    if (session && grant === session && !currentGrant(session)) await cancelNativeEmailFetch()
    return { used: false, synced: 0, newCount: 0, classified: 0 }
  }
  try {
    const r = await p.runNow()
    if (version !== generation || !currentGrant(session)) return { used: false, synced: 0, newCount: 0, classified: 0 }
    return {
      used: true,
      synced: r.synced ?? 0,
      newCount: r.newCount ?? 0,
      classified: r.classified ?? 0,
    }
  } catch {
    return { used: false, synced: 0, newCount: 0, classified: 0 }
  }
}
