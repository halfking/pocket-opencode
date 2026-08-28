/**
 * useNotificationPermission — 通知权限探测与申请（Android 13+ POST_NOTIFICATIONS 必需）。
 *
 * Capacitor 原生：走 @capacitor/local-notifications 的 checkPermissions / requestPermissions。
 * Web 平台：fallback 到浏览器 Notification API（用于开发调试或 PWA 场景）。
 *
 * 用法：
 *   const notif = useNotificationPermission()
 *   await notif.recheck()  // 首次进入页面 / 从系统设置回来时调用
 *   // 或
 *   const ok = await notif.ensure()
 */
import { ref } from 'vue'
import { Capacitor } from '@capacitor/core'
import { LocalNotifications } from '@capacitor/local-notifications'

export type NotificationPermissionState = 'unknown' | 'granted' | 'denied' | 'unavailable'

const state = ref<NotificationPermissionState>('unknown')
const label = ref('未检测')

function mapNative(display: string): NotificationPermissionState {
  if (display === 'granted') return 'granted'
  // prompt / prompt-with-rationale / denied 都视为"未授权"，UI 据此决定下一步
  return 'denied'
}

function mapWeb(p: NotificationPermission): NotificationPermissionState {
  if (p === 'granted') return 'granted'
  if (p === 'denied') return 'denied'
  return 'unknown'
}

function setLabel() {
  label.value =
    state.value === 'granted' ? '已授权'
    : state.value === 'denied' ? '未授权'
    : state.value === 'unavailable' ? '不支持'
    : '未检测'
}

async function check(): Promise<NotificationPermissionState> {
  try {
    if (Capacitor.isNativePlatform()) {
      const r = await LocalNotifications.checkPermissions()
      state.value = mapNative(r.display)
    } else if (typeof Notification !== 'undefined') {
      state.value = mapWeb(Notification.permission)
    } else {
      state.value = 'unavailable'
    }
  } catch (err) {
    console.warn('[notification-permission] check failed:', err)
    state.value = 'unavailable'
  }
  setLabel()
  return state.value
}

async function request(): Promise<NotificationPermissionState> {
  try {
    if (Capacitor.isNativePlatform()) {
      const r = await LocalNotifications.requestPermissions()
      state.value = mapNative(r.display)
    } else if (typeof Notification !== 'undefined') {
      const r = await Notification.requestPermission()
      state.value = mapWeb(r)
    } else {
      state.value = 'unavailable'
    }
  } catch (err) {
    console.warn('[notification-permission] request failed:', err)
    state.value = 'unavailable'
  }
  setLabel()
  return state.value
}

export function useNotificationPermission() {
  /** 仅查询当前状态，不触发系统弹窗。 */
  async function recheck(): Promise<NotificationPermissionState> {
    return check()
  }

  /** 已授权直接返回 granted；否则触发系统授权弹窗并返回结果。 */
  async function ensure(): Promise<NotificationPermissionState> {
    if (state.value === 'granted') return 'granted'
    const current = await check()
    if (current === 'granted') return 'granted'
    return request()
  }

  return { state, label, recheck, ensure }
}
