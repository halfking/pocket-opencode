import { PRODUCTION_API_BASE, resolveApiBase } from '../../config/api-base'
import { useAuthStore } from '../../stores/auth'
import {
  decideFetchKick,
  FETCH_SUCCESS_GAP_MS,
  resolveFetchApiBase,
  type FetchKickState,
} from './email-fetch-plan'
import { configureNativeEmailFetch, scheduleNativeEmailFetch } from './email-fetch-native'
import { runDelegatedEmailFetch } from './email-fetch-run'

const GAP_MS = FETCH_SUCCESS_GAP_MS

const state: FetchKickState = {
  lastAttemptAt: 0,
  lastAttemptFailed: false,
  inFlight: false,
}
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

  const kick = () => {
    // 未登录时不要发起：runDelegatedEmailFetch 会打需要 token 的接口，
    // 白跑一趟还会把节流窗口记在这次注定失败的尝试上。
    if (!useAuthStore().token) return
    const now = Date.now()
    if (decideFetchKick(state, now, { successGapMs: GAP_MS }) !== 'run') return
    // 时间戳记在「发起」这一刻，不是「成功」那一刻——否则一次跑三分钟的
    // 收信会把下一次也顺延三分钟。
    state.lastAttemptAt = now
    state.inFlight = true
    void bindNative()
    void runDelegatedEmailFetch({ classify: true }).then(
      () => { state.lastAttemptFailed = false },
      () => { state.lastAttemptFailed = true },
    ).finally(() => { state.inFlight = false })
  }

  // 2026-10-03：这里原先只调 bindNative()，**没有 kick**。
  // 而 kick 只挂在 visibilitychange / appStateChange 上——这两个事件在冷启动
  // 时都不会触发（visibilitychange 只在可见性**变化**时触发，appStateChange
  // 只在状态**变化**时触发）。于是冷启动的路径是：用户打开应用 → 收件箱显示
  // 本地库里的旧数据 → 服务端一次都不拉 → 直到他把应用切到后台再切回来。
  //
  // 表现为「邮件不刷新」，而且没有任何报错。原生侧 scheduleNativeEmailFetch
  // 只是设了周期任务，不能替代首次拉取。
  void kick()

  if (typeof document !== 'undefined') {
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') kick()
    })
  }
  void import('@capacitor/app').then(({ App }) => {
    void App.addListener('appStateChange', (s: { isActive: boolean }) => {
      if (s.isActive) kick()
    })
  }).catch(() => {})
}
