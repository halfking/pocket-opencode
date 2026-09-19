/**
 * useHaptics — 触觉反馈统一封装（原生顺滑度审计 P0 #4）。
 *
 * 原生（Capacitor）走 @capacitor/haptics（iOS Taptic / Android Vibrator 组合效果）；
 * Web 降级 navigator.vibrate（Android Chrome 可用，桌面 no-op）；
 * 两者都不可用时静默 no-op，调用方无需判空。
 *
 * 惯例映射（对照 Material 3 / iOS HIG）：
 *  - light  ：tab 切换、下拉刷新过阈值、滑动返回提交、长按菜单弹出
 *  - medium ：次级确认（预留）
 *  - heavy  ：预留
 *  - error  ：操作失败（Toast error 路径）
 */
import { Capacitor } from '@capacitor/core'

export type HapticStyle = 'light' | 'medium' | 'heavy' | 'error'

let hapticsMod: Promise<typeof import('@capacitor/haptics')> | null = null

function loadNative(): Promise<typeof import('@capacitor/haptics')> | null {
  if (!Capacitor.isNativePlatform()) return null
  // 动态 import：Web 构建不把插件打进主包
  hapticsMod ??= import('@capacitor/haptics')
  return hapticsMod
}

function webVibrate(pattern: number | number[]) {
  try {
    navigator.vibrate?.(pattern)
  } catch {
    /* no-op */
  }
}

export function haptic(style: HapticStyle = 'light') {
  const mod = loadNative()
  if (!mod) {
    // Web 降级：Android Chrome navigator.vibrate 生效，其余平台静默
    if (style === 'error') webVibrate([40, 60, 40])
    else webVibrate(style === 'light' ? 8 : 16)
    return
  }
  void mod
    .then(({ Haptics, ImpactStyle, NotificationType }) => {
      if (style === 'error') return Haptics.notification({ type: NotificationType.Error })
      if (style === 'heavy') return Haptics.impact({ style: ImpactStyle.Heavy })
      if (style === 'medium') return Haptics.impact({ style: ImpactStyle.Medium })
      return Haptics.impact({ style: ImpactStyle.Light })
    })
    .catch(() => {
      /* 插件异常静默：触觉缺失不应打断交互 */
    })
}

export function useHaptics() {
  return {
    light: () => haptic('light'),
    medium: () => haptic('medium'),
    heavy: () => haptic('heavy'),
    error: () => haptic('error'),
  }
}
