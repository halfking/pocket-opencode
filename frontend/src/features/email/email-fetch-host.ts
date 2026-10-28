import { PRODUCTION_API_BASE, resolveApiBase } from '../../config/api-base'
import { effectScope, watch } from 'vue'
import { captureAuthSession, isCurrentAuthSession, useAuthStore } from '../../stores/auth'
import { readCachedModuleMode } from '../../stores/syncPolicyStore'
import {
  decideFetchKick,
  FETCH_SUCCESS_GAP_MS,
  resolveFetchApiBase,
  type FetchKickState,
} from './email-fetch-plan'
import { cancelNativeEmailFetch, configureNativeEmailFetch, scheduleNativeEmailFetch } from './email-fetch-native'
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
  const auth = useAuthStore()
  let epoch = 0

  const kick = () => {
    // 未登录时不要发起：runDelegatedEmailFetch 会打需要 token 的接口，
    // 白跑一趟还会把节流窗口记在这次注定失败的尝试上。
    if (!useAuthStore().token) return
    if (useAuthStore() !== auth) return
    if (readCachedModuleMode('email') !== 'cloud') {
      void cancelNativeEmailFetch()
      return
    }
    const session = captureAuthSession(auth)
    const version = epoch
    const now = Date.now()
    if (decideFetchKick(state, now, { successGapMs: GAP_MS }) !== 'run') return
    // 时间戳记在「发起」这一刻，不是「成功」那一刻——否则一次跑三分钟的
    // 收信会把下一次也顺延三分钟。
    state.lastAttemptAt = now
    state.inFlight = true
    void bindNative().then(() => {
      if (version !== epoch || !isCurrentAuthSession(auth, session) || readCachedModuleMode('email') !== 'cloud') return
      return runDelegatedEmailFetch({ classify: true })
    }).then(
      () => { if (version === epoch) state.lastAttemptFailed = false },
      () => { if (version === epoch) state.lastAttemptFailed = true },
    ).finally(() => { if (version === epoch) state.inFlight = false })
  }

  const reset = () => {
    const version = ++epoch
    state.lastAttemptAt = 0
    state.lastAttemptFailed = false
    state.inFlight = false
    void cancelNativeEmailFetch().then(() => { if (version === epoch) kick() })
  }
  // Auth changes invalidate the native credential as well as the JS cache.
  effectScope(true).run(() => {
    watch(() => [auth.token, auth.user, auth.userId, auth.workspaceId], reset, { flush: 'sync' })
  })
  if (typeof window !== 'undefined') window.addEventListener('openpocket:sync-policy-changed', reset)

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
  }).catch((e) => {
    // 原生插件加载失败 ⇒ 收不到 appStateChange ⇒ 回到前台时不会补拉邮件。
    // Web 端该插件是空实现（走不到这里），原生端失败即真失效 ⇒ 留痕。
    console.warn('[email] appStateChange 监听注册失败（回前台不补拉邮件）：', e)
  })
}
