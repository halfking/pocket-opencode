import { Capacitor, registerPlugin } from '@capacitor/core'
import type { PermissionStatus } from './permission-action'
import { canRequestPermissionAgain } from './permission-action'

export type RuntimePermissionName = 'microphone' | 'notifications'

export interface RuntimePermissionResult {
  name: RuntimePermissionName
  status: PermissionStatus
  canRequestAgain: boolean
}

interface AppSettingsPlugin {
  openAppDetails(): Promise<void>
  check(options: { name: RuntimePermissionName }): Promise<RuntimePermissionResult>
  request(options: { name: RuntimePermissionName }): Promise<RuntimePermissionResult>
}

const appSettings = registerPlugin<AppSettingsPlugin>('AppSettings')

function normalizeResult(
  name: RuntimePermissionName,
  raw: Partial<RuntimePermissionResult> | undefined,
): RuntimePermissionResult {
  const status = raw?.status || 'prompt'
  return {
    name,
    status,
    canRequestAgain: raw?.canRequestAgain ?? canRequestPermissionAgain(status),
  }
}

export function useAppSettings() {
  async function openAppDetails(): Promise<boolean> {
    try {
      if (Capacitor.getPlatform() === 'android') {
        await appSettings.openAppDetails()
        return true
      }
      if (Capacitor.getPlatform() === 'ios') {
        window.location.href = 'app-settings:'
        return true
      }
    } catch (error) {
      console.warn('[settings] unable to open app settings', error)
    }
    return false
  }

  async function checkPermission(name: RuntimePermissionName): Promise<RuntimePermissionResult | null> {
    if (!Capacitor.isNativePlatform()) return null
    try {
      return normalizeResult(name, await appSettings.check({ name }))
    } catch (error) {
      console.warn('[settings] check permission failed', error)
      return null
    }
  }

  /** 再次发起系统申请窗。永久拒绝时系统不会弹窗，返回 denied + canRequestAgain=false。 */
  async function requestPermission(name: RuntimePermissionName): Promise<RuntimePermissionResult | null> {
    if (!Capacitor.isNativePlatform()) return null
    try {
      return normalizeResult(name, await appSettings.request({ name }))
    } catch (error) {
      console.warn('[settings] request permission failed', error)
      return null
    }
  }

  return { openAppDetails, checkPermission, requestPermission }
}
