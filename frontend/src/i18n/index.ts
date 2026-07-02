import { createI18n } from 'vue-i18n'
import zhCN from '../locales/zh-CN.json'
import zhTW from '../locales/zh-TW.json'
import enUS from '../locales/en-US.json'
import jaJP from '../locales/ja-JP.json'
import koKR from '../locales/ko-KR.json'
import deDE from '../locales/de-DE.json'
import frFR from '../locales/fr-FR.json'
import esES from '../locales/es-ES.json'
import ptBR from '../locales/pt-BR.json'

// 语言代码类型
export type LocaleType = 'zh-CN' | 'zh-TW' | 'en-US' | 'ja-JP' | 'ko-KR' | 'de-DE' | 'fr-FR' | 'es-ES' | 'pt-BR'

// 支持的语言列表
export const SUPPORT_LOCALES: LocaleType[] = [
  'zh-CN',
  'zh-TW',
  'en-US',
  'ja-JP',
  'ko-KR',
  'de-DE',
  'fr-FR',
  'es-ES',
  'pt-BR'
]

// 语言名称映射
export const LOCALE_NAMES: Record<LocaleType, string> = {
  'zh-CN': '简体中文',
  'zh-TW': '繁體中文',
  'en-US': 'English',
  'ja-JP': '日本語',
  'ko-KR': '한국어',
  'de-DE': 'Deutsch',
  'fr-FR': 'Français',
  'es-ES': 'Español',
  'pt-BR': 'Português'
}

// 获取浏览器语言
export function getBrowserLocale(): LocaleType {
  const browserLang = navigator.language
  
  // 精确匹配
  if (SUPPORT_LOCALES.includes(browserLang as LocaleType)) {
    return browserLang as LocaleType
  }
  
  // 语言前缀匹配（如 en 匹配 en-US）
  const langPrefix = browserLang.split('-')[0]
  const matchedLocale = SUPPORT_LOCALES.find(locale => locale.startsWith(langPrefix))
  
  return matchedLocale || 'en-US' // 默认英文
}

// 创建 i18n 实例
const i18n = createI18n({
  legacy: false, // 使用 Composition API 模式
  locale: getBrowserLocale(), // 默认语言
  fallbackLocale: 'en-US', // 回退语言
  messages: {
    'zh-CN': zhCN,
    'zh-TW': zhTW,
    'en-US': enUS,
    'ja-JP': jaJP,
    'ko-KR': koKR,
    'de-DE': deDE,
    'fr-FR': frFR,
    'es-ES': esES,
    'pt-BR': ptBR
  }
})

export default i18n
