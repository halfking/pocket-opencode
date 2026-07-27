import { Capacitor, registerPlugin } from '@capacitor/core'

interface AppSettingsPlugin {
  openAppDetails(): Promise<void>
}

const appSettings = registerPlugin<AppSettingsPlugin>('AppSettings')


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

  return { openAppDetails }
}
