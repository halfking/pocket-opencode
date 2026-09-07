/**
 * useNotificationPermission — 通知权限探测与申请（Android 13+ POST_NOTIFICATIONS 必需）。
 *
 * Capacitor 原生优先走 AppSettings.request，以便禁止后再次发起系统申请窗。
 * 插件不可用时 fallback 到 @capacitor/local-notifications / 浏览器 Notification API。
 */
import { ref } from 'vue'
import { Capacitor } from '@capacitor/core'
import { LocalNotifications } from '@capacitor/local-notifications'
import { useAppSettings } from './useAppSettings'
import {
  canRequestPermissionAgain,
  type PermissionStatus,
} from './permission-action'

export type NotificationPermissionState = PermissionStatus | 'unknown'

const state = ref<NotificationPermissionState>('unknown')
const label = ref('未检测')
const canRequestAgain = ref(true)

function setFromStatus(status: PermissionStatus) {
  state.value = status
  canRequestAgain.value = canRequestPermissionAgain(status)
  label.value =
    status === 'granted' ? '已授权'
    : status === 'unavailable' ? '不支持'
    : canRequestAgain.value ? '未授权'
    : '已拒绝'
}

function mapWeb(p: NotificationPermission): PermissionStatus {
  if (p === 'granted') return 'granted'
  if (p === 'denied') return 'denied'
  return 'prompt'
}

function mapLegacyDisplay(display: string): PermissionStatus {
  if (display === 'granted') return 'granted'
  if (display === 'denied') return 'denied'
  if (display === 'prompt-with-rationale') return 'prompt-with-rationale'
  if (display === 'prompt') return 'prompt'
  return 'prompt'
}

export function useNotificationPermission() {
  const appSettings = useAppSettings()

  async function recheck(): Promise<NotificationPermissionState> {
    try {
      if (Capacitor.isNativePlatform()) {
        const native = await appSettings.checkPermission('notifications')
        if (native) {
          setFromStatus(native.status)
          return state.value
        }
        const r = await LocalNotifications.checkPermissions()
        setFromStatus(mapLegacyDisplay(r.display))
      } else if (typeof Notification !== 'undefined') {
        setFromStatus(mapWeb(Notification.permission))
      } else {
        setFromStatus('unavailable')
      }
    } catch (err) {
      console.warn('[notification-permission] check failed:', err)
      setFromStatus('unavailable')
    }
    return state.value
  }

  async function ensure(): Promise<NotificationPermissionState> {
    if (state.value === 'granted') return 'granted'
    try {
      if (Capacitor.isNativePlatform()) {
        const native = await appSettings.requestPermission('notifications')
        if (native) {
          setFromStatus(native.status)
          return state.value
        }
        const r = await LocalNotifications.requestPermissions()
        setFromStatus(mapLegacyDisplay(r.display))
      } else if (typeof Notification !== 'undefined') {
        const r = await Notification.requestPermission()
        setFromStatus(mapWeb(r))
      } else {
        setFromStatus('unavailable')
      }
    } catch (err) {
      console.warn('[notification-permission] request failed:', err)
      setFromStatus('unavailable')
    }
    return state.value
  }

  return { state, label, canRequestAgain, recheck, ensure }
}
