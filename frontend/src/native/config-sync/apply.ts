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
    if (prefs.locale) applyRemoteLocale(prefs.locale)
    if (prefs.deviceTier) localStorage.setItem('pocket_device_tier', prefs.deviceTier)
    if (prefs.sttPref) localStorage.setItem('pocket_stt_pref', prefs.sttPref)
    if (prefs.theme && typeof document !== 'undefined') {
      const root = document.documentElement
      if (prefs.theme === 'system') root.removeAttribute('data-theme')
      else root.setAttribute('data-theme', prefs.theme)
    }
  } catch {
    // localStorage may be unavailable
  }
}

/**
 * 下发语言要真正作用到 i18n runtime。
 * 此前这里只写 documentElement.lang，界面语言纹丝不动，
 * 出现「<html lang=zh-CN> 但界面是英文」的错配（真机实测）。
 *
 * 另一处缺陷：未规范化的字符串被直接写进 app_locale 与 <html lang>。
 * 老版本上报的 'zh' 这类裸语言码既不是受支持 locale，也不是合法 BCP-47 标签，
 * 写进去后 <html lang> 与 i18n runtime 再次错配，且会污染后续启动解析。
 * 现在：只在能匹配到受支持语言时才落盘并切换，匹配不到则完全忽略。
 * 用动态 import 避免 stores → native → stores 的静态环。
 */
function applyRemoteLocale(rawLocale: string): void {
  void import('../../i18n')
    .then(async ({ matchLocale, applyLocale }) => {
      const matched = matchLocale([rawLocale])
      if (!matched) return
      const { useLocaleStore } = await import('../../stores/locale')
      try {
        useLocaleStore().setLocale(matched)
      } catch {
        // pinia 尚未就绪时至少让 i18n runtime 生效
        applyLocale(matched)
      }
    })
    .catch(() => {
      // i18n 模块不可用时不动 <html lang>，避免写入非法语言标签
    })
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
