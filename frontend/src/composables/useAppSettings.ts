import { Capacitor, registerPlugin } from '@capacitor/core'
import type { PermissionStatus } from './permission-action'
import { canRequestPermissionAgain } from './permission-action'
import type { SettingsPermissionName } from './permission-settings'

export type RuntimePermissionName = 'microphone' | 'notifications' | 'camera' | 'photos'
export type { SettingsPermissionName }

export interface RuntimePermissionResult {
  name: RuntimePermissionName
  status: PermissionStatus
  canRequestAgain: boolean
}

interface AppSettingsPlugin {
  openAppDetails(options?: { name?: SettingsPermissionName }): Promise<{ opened?: boolean }>
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
  async function openPermissionSettings(name?: SettingsPermissionName): Promise<boolean> {
    try {
      if (Capacitor.getPlatform() === 'ios') {
        window.location.href = 'app-settings:'
        return true
      }
      if (Capacitor.isNativePlatform()) {
        const result = await appSettings.openAppDetails(name ? { name } : undefined)
        return result?.opened !== false
      }
    } catch (error) {
      console.warn('[settings] unable to open app settings', error)
    }
    return false
  }

  async function openAppDetails(): Promise<boolean> {
    return openPermissionSettings()
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

  return { openAppDetails, openPermissionSettings, checkPermission, requestPermission }
}
