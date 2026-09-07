import { PRODUCTION_API_BASE, resolveApiBase } from '../../config/api-base'
import { useAuthStore } from '../../stores/auth'
import { resolveFetchApiBase, shouldRunBackgroundFetch } from './email-fetch-plan'
import { configureNativeEmailFetch, scheduleNativeEmailFetch } from './email-fetch-native'
import { runDelegatedEmailFetch } from './email-fetch-run'

const GAP_MS = 15 * 60_000
let lastAt = 0
let started = false

async function bindNative(): Promise<void> {
  const auth = useAuthStore()
  if (!auth.token) return
  const ok = await configureNativeEmailFetch(
    resolveFetchApiBase(resolveApiBase(), PRODUCTION_API_BASE),
    auth.token,
  )
  if (ok) await scheduleNativeEmailFetch(GAP_MS)
}

export function startEmailFetchHost(): void {
  if (started) return
  started = true
  void bindNative()
  const kick = () => {
    const now = Date.now()
    if (!shouldRunBackgroundFetch(now, lastAt, GAP_MS)) return
    lastAt = now
    void bindNative()
    void runDelegatedEmailFetch({ classify: true }).catch(() => {})
  }
  if (typeof document !== 'undefined') {
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') kick()
    })
  }
  void import('@capacitor/app').then(({ App }) => {
    void App.addListener('appStateChange', (state: { isActive: boolean }) => {
      if (state.isActive) kick()
    })
  }).catch(() => {})
}
