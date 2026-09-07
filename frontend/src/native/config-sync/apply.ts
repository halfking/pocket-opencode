import type { RemoteSetting } from '../../api/user-settings'
import { writeSelectedInstance, type SelectedInstance } from '../../config/selected-instance'

type AppPrefs = {
  theme?: 'light' | 'dark' | 'system'
  locale?: string
  deviceTier?: string
  sttPref?: string
}

type ChatSettingsPayload = {
  temperature?: number
  maxTokens?: number
  systemPrompt?: string
  defaultModel?: string
  modelByModality?: Record<string, string>
}

export function applyRemoteSetting(rec: RemoteSetting): void {
  const payload = (rec.payload && typeof rec.payload === 'object') ? rec.payload as Record<string, unknown> : {}
  if (rec.namespace === 'app_prefs' && rec.id === 'default') {
    applyAppPrefs(payload as AppPrefs)
    return
  }
  if (rec.namespace === 'chat_settings' && rec.id === 'default') {
    applyChatSettings(payload as ChatSettingsPayload)
    return
  }
  if (rec.namespace === 'connection' && rec.id === 'default') {
    const inst = payload as Partial<SelectedInstance>
    if (inst.id) writeSelectedInstance(inst as SelectedInstance)
  }
}

function applyAppPrefs(prefs: AppPrefs): void {
  try {
    if (prefs.theme) localStorage.setItem('app_theme', prefs.theme)
    if (prefs.locale) localStorage.setItem('app_locale', prefs.locale)
    if (prefs.deviceTier) localStorage.setItem('pocket_device_tier', prefs.deviceTier)
    if (prefs.sttPref) localStorage.setItem('pocket_stt_pref', prefs.sttPref)
    if (prefs.theme && typeof document !== 'undefined') {
      const root = document.documentElement
      if (prefs.theme === 'system') root.removeAttribute('data-theme')
      else root.setAttribute('data-theme', prefs.theme)
    }
    if (prefs.locale && typeof document !== 'undefined') {
      document.documentElement.lang = prefs.locale
    }
  } catch {
    // localStorage may be unavailable
  }
}

function applyChatSettings(settings: ChatSettingsPayload): void {
  try {
    const workspace = localStorage.getItem('pocket_workspace_id') || ''
    const user = localStorage.getItem('pocket_user') || ''
    const scope = encodeURIComponent(workspace || user || 'local')
    const key = `pocket:ai-chat:settings:v2:${scope}`
    const current = JSON.parse(localStorage.getItem(key) || '{}') as Record<string, unknown>
    localStorage.setItem(key, JSON.stringify({ ...current, ...settings }))
  } catch {
    // ignore
  }
}
