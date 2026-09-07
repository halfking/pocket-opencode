/**
 * useMicPermission — 麦克风权限探测与申请（会议/语音输入共用）。
 *
 * 真机上 WebView 的 getUserMedia 需要 app 已持有 RECORD_AUDIO 运行时权限。
 * 禁止后仍可再次申请：原生走 AppSettings.request；只有永久拒绝才引导去系统设置。
 */
import { ref } from 'vue'
import { Capacitor } from '@capacitor/core'
import { useAppSettings } from './useAppSettings'
import { canRequestPermissionAgain, type PermissionStatus } from './permission-action'

export type MicState = 'unknown' | 'granted' | 'denied' | 'unavailable'

const state = ref<MicState>('unknown')
const deniedLabel = ref('')
const canRequestAgain = ref(true)

function applyNativeStatus(status: PermissionStatus) {
  canRequestAgain.value = canRequestPermissionAgain(status)
  if (status === 'granted') {
    state.value = 'granted'
    deniedLabel.value = ''
    return
  }
  if (status === 'unavailable') {
    state.value = 'unavailable'
    deniedLabel.value = '当前环境不支持录音'
    return
  }
  state.value = 'denied'
  deniedLabel.value = canRequestAgain.value
    ? '麦克风权限被拒绝，点击可重新申请'
    : '麦克风权限被拒绝，请在系统设置中授权后重试'
}

/** 探测麦克风权限：调用 getUserMedia 并立即释放流，仅用于触发/检测授权。 */
async function probe(): Promise<boolean> {
  if (!navigator.mediaDevices || typeof navigator.mediaDevices.getUserMedia !== 'function') {
    state.value = 'unavailable'
    canRequestAgain.value = false
    deniedLabel.value = '当前环境不支持录音（getUserMedia 不可用）'
    return false
  }
  try {
    const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
    stream.getTracks().forEach((t) => t.stop())
    state.value = 'granted'
    canRequestAgain.value = false
    deniedLabel.value = ''
    return true
  } catch (e: any) {
    const name = e?.name || ''
    if (name === 'NotAllowedError' || name === 'SecurityError') {
      state.value = 'denied'
      canRequestAgain.value = !Capacitor.isNativePlatform()
      deniedLabel.value = Capacitor.isNativePlatform()
        ? '麦克风权限被拒绝，点击可重新申请'
        : '麦克风权限被拒绝，请在浏览器站点设置中允许后重试'
    } else if (name === 'NotFoundError' || name === 'OverconstrainedError') {
      state.value = 'unavailable'
      canRequestAgain.value = false
      deniedLabel.value = '未找到可用的麦克风设备'
    } else {
      state.value = 'unavailable'
      canRequestAgain.value = false
      deniedLabel.value = '麦克风不可用：' + (e?.message || name || '未知错误')
    }
    return false
  }
}

export function useMicPermission() {
  const appSettings = useAppSettings()

  async function ensure(): Promise<boolean> {
    if (state.value === 'granted') return true
    if (Capacitor.isNativePlatform()) {
      const result = await appSettings.requestPermission('microphone')
      if (result) {
        applyNativeStatus(result.status)
        return result.status === 'granted'
      }
    }
    return probe()
  }

  async function recheck(): Promise<boolean> {
    if (Capacitor.isNativePlatform()) {
      const result = await appSettings.checkPermission('microphone')
      if (result) {
        applyNativeStatus(result.status)
        return result.status === 'granted'
      }
    }
    return probe()
  }

  return {
    state,
    deniedLabel,
    canRequestAgain,
    ensure,
    recheck,
  }
}
