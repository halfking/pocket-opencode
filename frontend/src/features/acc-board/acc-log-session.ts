/** Process-owned authority observation, independent of the first component's scope. */
import { effectScope, shallowRef, watch } from 'vue'
import { captureAuthAttempt, captureAuthSession, assertAuthSession, useAuthStore } from '../../stores/auth.ts'
import { ACC_BASE_KEY, ACC_TOKEN_KEY, getAccBase, getAccToken, onAccConfigChange } from '../../api/accApi.ts'
import { AccLogRuntime, type AccLogAuthority } from './acc-log-runtime.ts'
import { mockGetRunEvents, mockStartEventFeed, mockStopEventFeed } from './mock.ts'

type Auth = ReturnType<typeof useAuthStore>
const sessions = new WeakMap<Auth, ReturnType<typeof createSession>>()

function createSession(auth: Auth) {
  const runtime = new AccLogRuntime({ mock: {
    start: mockStartEventFeed, stop: mockStopEventFeed, read: mockGetRunEvents,
  } })
  const authority = shallowRef<object | null>(null)
  const stopAuthority = runtime.onAuthorityChange(owner => { authority.value = owner })
  // Register auth's synchronous generation observer before our own watcher (A→B→A).
  captureAuthAttempt(auth)
  const sync = () => {
    let owner: AccLogAuthority | null = null
    if (auth.isAuthenticated && auth.userId && auth.workspaceId) {
      const session = captureAuthSession(auth)
      const workspaceId = auth.workspaceId
      const base = getAccBase()
      const credential = getAccToken()
      owner = { session: session.generation, workspaceId, base, token: credential,
        assertCurrent() {
          assertAuthSession(auth, session)
          if (auth.workspaceId !== workspaceId || getAccBase() !== base || getAccToken() !== credential) {
            throw new Error('ACC log authority changed')
          }
        },
      }
    }
    runtime.setAuthority(owner)
  }
  const scope = effectScope(true)
  scope.run(() => watch(() => [auth.token, auth.user, auth.userId, auth.workspaceId,
    auth.authMethod, auth.activationStatus, auth.activationVersion], sync, { flush: 'sync', immediate: true }))
  const stopConfig = onAccConfigChange(sync)
  const onStorage = (event: StorageEvent) => {
    if (event.key === null || event.key === ACC_BASE_KEY || event.key === ACC_TOKEN_KEY) sync()
  }
  if (typeof window !== 'undefined') window.addEventListener('storage', onStorage)
  const disposeAuth = auth.$dispose.bind(auth)
  auth.$dispose = () => {
    scope.stop()
    stopConfig()
    if (typeof window !== 'undefined') window.removeEventListener('storage', onStorage)
    runtime.dispose()
    stopAuthority()
    sessions.delete(auth)
    disposeAuth()
  }
  return { runtime, authority }
}

export function getAccLogSession(auth: Auth = useAuthStore()) {
  let session = sessions.get(auth)
  if (!session) { session = createSession(auth); sessions.set(auth, session) }
  return session
}
