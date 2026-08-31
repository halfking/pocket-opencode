/**
 * useStatusBar — Capacitor StatusBar 桥。
 *
 * 设计：模块级无副作用；调用方在 setup/onMounted 拿到的 start()/stop() 是
 * 真正的执行单元，绑定到组件生命周期。这样多个调用方（App.vue / UpdateChecker
 * 等）独立 teardown，深浅主题切换监听也能在组件 unmount 时彻底清理。
 *
 * 深浅判定不再直接读 prefers-color-scheme，而是订阅 theme store 的生效主题
 * （settings.theme 手动覆盖优先），保证状态栏与页面皮肤一致。
 *
 * Web / dev 环境全部 no-op。
 */
import { watch, type WatchStopHandle } from 'vue'
import { Capacitor } from '@capacitor/core'
import { StatusBar, Style } from '@capacitor/status-bar'
import { useThemeStore } from '../stores/theme'

async function applyStyleForTheme(dark: boolean) {
  if (!Capacitor.isNativePlatform()) return
  try {
    await StatusBar.setStyle({ style: dark ? Style.Dark : Style.Light })
  } catch (err) {
    console.warn('[StatusBar] setStyle failed', err)
  }
}

export interface StatusBarController {
  /** 启动主题监听 + 应用初始样式 */
  start(): void
  /** 移除监听（幂等） */
  stop(): void
}

export function useStatusBar(): StatusBarController {
  let started = false
  let stopWatch: WatchStopHandle | null = null
  let navPosture: Navigator['devicePosture'] | null = null
  let navPostureOnChange: ((ev: Event) => void) | null = null

  function start() {
    if (started || typeof window === 'undefined') return
    started = true

    if (!Capacitor.isNativePlatform()) return

    // start() 在组件生命周期内调用，此时 pinia 已激活
    const theme = useThemeStore()
    stopWatch = watch(
      () => theme.isDark,
      dark => { void applyStyleForTheme(dark) }
    )

    // devicePosture.onchange 在折叠屏切换时也常伴随系统栏高度变化，
    // 触发一次再校准以应对某些 OEM 在折叠后调整 inset-top。
    navPosture = (navigator as Navigator).devicePosture ?? null
    if (navPosture && 'onchange' in navPosture) {
      navPostureOnChange = () => { void applyStyleForTheme(theme.isDark) }
      ;(navPosture as unknown as { onchange: unknown }).onchange = navPostureOnChange
    }

    void applyStyleForTheme(theme.isDark)
  }

  function stop() {
    if (!started) return
    started = false

    stopWatch?.()
    stopWatch = null

    if (navPosture && navPostureOnChange && 'onchange' in navPosture) {
      ;(navPosture as unknown as { onchange: unknown }).onchange = null
    }
    navPosture = null
    navPostureOnChange = null
  }

  return { start, stop }
}
