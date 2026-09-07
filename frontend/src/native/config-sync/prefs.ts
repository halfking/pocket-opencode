import { writeSelectedInstance, readSelectedInstance, type SelectedInstance } from '../../config/selected-instance'
import { saveSettingLocalFirst } from './runtime'

export function currentAppPrefs(): Record<string, string> {
  return {
    theme: safeGet('app_theme') || 'system',
    locale: safeGet('app_locale') || 'zh',
    deviceTier: safeGet('pocket_device_tier') || 'unknown',
    sttPref: safeGet('pocket_stt_pref') || 'auto',
  }
}

export function persistAppPrefs(): void {
  void saveSettingLocalFirst('app_prefs', 'default', currentAppPrefs())
}

export function persistChatSettings(settings: unknown): void {
  void saveSettingLocalFirst('chat_settings', 'default', settings)
}

export function persistSelectedInstance(instance: SelectedInstance): void {
  writeSelectedInstance(instance)
  void saveSettingLocalFirst('connection', 'default', instance)
}

export function persistCurrentInstance(): void {
  const inst = readSelectedInstance()
  if (inst) void saveSettingLocalFirst('connection', 'default', inst)
}

function safeGet(key: string): string {
  try {
    return localStorage.getItem(key) || ''
  } catch {
    return ''
  }
}
