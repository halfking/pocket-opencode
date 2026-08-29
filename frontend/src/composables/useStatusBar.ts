/**
 * useStatusBar — Capacitor StatusBar 桥。
 *
 * 设计：模块级无副作用；调用方在 setup/onMounted 拿到的 start()/stop() 是
 * 真正的执行单元，绑定到组件生命周期。这样多个调用方（App.vue / UpdateChecker
 * 等）独立 teardown，深浅主题切换监听也能在组件 unmount 时彻底清理。
 *
 * Web / dev 环境全部 no-op。
 */
import { Capacitor } from '@capacitor/core'
import { StatusBar, Style } from '@capacitor/status-bar'

function detectDark(): boolean {
  return typeof window !== 'undefined'
    && window.matchMedia?.('(prefers-color-scheme: dark)').matches
}

async function applyStyleForTheme() {
  if (!Capacitor.isNativePlatform()) return
  try {
    await StatusBar.setStyle({ style: detectDark() ? Style.Dark : Style.Light })
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
  let mql: MediaQueryList | null = null
  let navPosture: Navigator['devicePosture'] | null = null
  let navPostureOnChange: ((ev: Event) => void) | null = null
  let mqOnChange: ((ev: MediaQueryListEvent) => void) | null = null

  function start() {
    if (started || typeof window === 'undefined') return
    started = true

    if (!Capacitor.isNativePlatform()) return

    mql = window.matchMedia('(prefers-color-scheme: dark)')
    mqOnChange = () => { void applyStyleForTheme() }
    mql.addEventListener?.('change', mqOnChange)

    // devicePosture.onchange 在折叠屏切换时也常伴随系统栏高度变化，
    // 触发一次再校准以应对某些 OEM 在折叠后调整 inset-top。
    navPosture = (navigator as Navigator).devicePosture ?? null
    if (navPosture && 'onchange' in navPosture) {
      navPostureOnChange = () => { void applyStyleForTheme() }
      ;(navPosture as unknown as { onchange: unknown }).onchange = navPostureOnChange
    }

    void applyStyleForTheme()
  }

  function stop() {
    if (!started) return
    started = false

    if (mql && mqOnChange) {
      mql.removeEventListener?.('change', mqOnChange)
    }
    mql = null
    mqOnChange = null

    if (navPosture && navPostureOnChange && 'onchange' in navPosture) {
      ;(navPosture as unknown as { onchange: unknown }).onchange = null
    }
    navPosture = null
    navPostureOnChange = null
  }

  return { start, stop }
}
