import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { LocaleType } from '../i18n'
import { applyLocale, matchLocale } from '../i18n'

const LOCALE_KEY = 'app_locale'

function readPersisted(): LocaleType | null {
  try {
    const saved = localStorage.getItem(LOCALE_KEY)
    return saved ? matchLocale([saved]) : null
  } catch {
    return null
  }
}

export const useLocaleStore = defineStore('locale', () => {
  // 以 i18n 实例的启动语言为准（用户选择 > 设备候选语言），store 与 runtime 保持同一真值
  const currentLocale = ref<LocaleType>(readPersisted() ?? (document.documentElement.lang as LocaleType) ?? 'en-US')

  /** 切换语言：写入 runtime + <html lang> + localStorage，三者必须同时生效。 */
  const setLocale = (locale: LocaleType) => {
    currentLocale.value = locale
    try {
      localStorage.setItem(LOCALE_KEY, locale)
    } catch {
      // localStorage 不可用时至少让界面语言生效
    }
    applyLocale(locale)
    import('../native/config-sync/prefs').then((m) => m.persistAppPrefs()).catch((e) => {
      // 语言偏好没能落进本地库 ⇒ 本次会话内有效，重启后回到旧值。
      // 跨设备同步靠的才是这条持久化路径 ⇒ 失败时别处看不到 ⇒ 留痕。
      console.warn('[locale] 语言偏好未能持久化：', e)
    })
  }

  return {
    currentLocale,
    setLocale
  }
})
