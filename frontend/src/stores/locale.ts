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
    import('../native/config-sync/prefs').then((m) => m.persistAppPrefs()).catch(() => {})
  }

  return {
    currentLocale,
    setLocale
  }
})
