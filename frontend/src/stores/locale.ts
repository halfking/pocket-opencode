import { defineStore } from 'pinia'
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { LocaleType } from '../i18n'
import { getBrowserLocale } from '../i18n'

const LOCALE_KEY = 'app_locale'

export const useLocaleStore = defineStore('locale', () => {
  // 从 localStorage 读取或使用浏览器语言
  const savedLocale = localStorage.getItem(LOCALE_KEY) as LocaleType | null
  const currentLocale = ref<LocaleType>(savedLocale || getBrowserLocale())

  // 切换语言
  const setLocale = (locale: LocaleType) => {
    currentLocale.value = locale
    localStorage.setItem(LOCALE_KEY, locale)
    
    // 更新 html lang 属性
    document.documentElement.lang = locale
  }

  // 初始化时设置 html lang
  document.documentElement.lang = currentLocale.value

  return {
    currentLocale,
    setLocale
  }
})
