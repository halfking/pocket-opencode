import { watch } from 'vue'
import { captureAuthSession, isCurrentAuthSession, type useAuthStore } from './auth'
import { localDB } from '../native/local-db'
import {
  installSyncPolicyActivationHost, installSyncPolicyScopeResolver, SYNC_POLICY_LOCAL_INTENTS_KEY,
  type SyncPolicyContext, type useSyncPolicyStore,
} from './syncPolicyStore'

/** Install before mount: persisted policy is a candidate until the owner and DB barriers confirm it. */
export function startSyncPolicyRuntime(
  auth: ReturnType<typeof useAuthStore>,
  policy: ReturnType<typeof useSyncPolicyStore>,
  effects: { startRuntimes(): void; connect(): void; disconnect(): void },
): () => void {
  const scope = () => auth.user || auth.workspaceId ? JSON.stringify([auth.user, auth.workspaceId]) : ''
  const captureContext = (): SyncPolicyContext | null => {
    if (auth.activationStatus !== 'ready') return null
    const session = captureAuthSession(auth), version = auth.activationVersion, capturedScope = scope()
    return {
      scope: capturedScope,
      intentScope: auth.userId ? JSON.stringify([auth.userId, auth.workspaceId]) : '',
      workspace: auth.workspaceId,
      isCurrent: () => auth.activationVersion === version && scope() === capturedScope && isCurrentAuthSession(auth, session),
    }
  }
  installSyncPolicyScopeResolver(scope)
  installSyncPolicyActivationHost({ captureContext, activate: (modes, publish) => localDB.requestPolicySnapshotActivation(modes, publish) })
  let started = false, disposed = false, revision = 0, resumedRevision = -1
  let activation: { ticket: number; context: SyncPolicyContext } | null = null
  const resume = () => {
    const current = activation
    if (!current || disposed || current.ticket !== revision || resumedRevision === revision ||
      !current.context.isCurrent() || !policy.activationReady || policy.activating) return
    // Final ready state, rather than one bootstrap promise, owns startup: a
    // settings load/storage event can legitimately supersede that first ticket.
    if (!started) { started = true; effects.startRuntimes() }
    if (disposed || current.ticket !== revision || resumedRevision === revision ||
      !current.context.isCurrent() || !policy.activationReady || policy.activating) return
    resumedRevision = revision
    if (auth.isAuthenticated) { void policy.load(); effects.connect() }
  }
  const stopPolicy = watch(() => [policy.activationReady, policy.activating], resume, { flush: 'sync' })
  const stop = watch(() => [auth.activationStatus, auth.activationVersion, auth.user, auth.workspaceId], () => {
    const ticket = ++revision
    activation = null
    // Owner-pending callbacks only revoke readers; no policy request can run
    // inside the owner's publisher. Ready restoration starts a new full barrier.
    policy.suspend()
    effects.disconnect()
    const context = captureContext()
    if (!context) return
    activation = { ticket, context }
    void policy.restoreFromCache().then(resume).catch(() => { /* The store keeps closed admission and its activation error. */ })
  }, { immediate: true, flush: 'sync' })
  const online = () => { if (!disposed && auth.isAuthenticated) void policy.load() }
  const storageChanged = (event: StorageEvent) => {
    if (!disposed && auth.activationStatus === 'ready' &&
      (event.key === null || event.key === SYNC_POLICY_LOCAL_INTENTS_KEY || event.key.startsWith(`${SYNC_POLICY_LOCAL_INTENTS_KEY}:`))) {
      // The action synchronously prepares the full snapshot and cancellation
      // event. A clear only republishes the effective modes; it cannot open cloud.
      void policy.refreshLocalIntents().catch(() => {})
    }
  }
  if (typeof window !== 'undefined') {
    window.addEventListener('online', online)
    window.addEventListener('storage', storageChanged)
  }
  return () => {
    disposed = true; revision++
    stop(); stopPolicy()
    if (typeof window !== 'undefined') {
      window.removeEventListener('online', online)
      window.removeEventListener('storage', storageChanged)
    }
    policy.suspend()
    effects.disconnect()
  }
}
