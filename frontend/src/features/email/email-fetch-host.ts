import { shouldRunBackgroundFetch } from './email-fetch-plan'
import { runDelegatedEmailFetch } from './email-fetch-run'

const GAP_MS = 15 * 60_000
let lastAt = 0
let started = false

export function startEmailFetchHost(): void {
  if (started) return
  started = true
  const kick = () => {
    const now = Date.now()
    if (!shouldRunBackgroundFetch(now, lastAt, GAP_MS)) return
    lastAt = now
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
