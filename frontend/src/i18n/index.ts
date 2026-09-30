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
import {
  LOCALE_STORAGE_KEY,
  SUPPORT_LOCALES,
  matchLocale,
  resolveInitialLocale,
  type LocaleType,
} from './locale-resolve'

export {
  LOCALE_STORAGE_KEY,
  SUPPORT_LOCALES,
  matchLocale,
  resolveInitialLocale,
  type LocaleType,
}

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

function readPersistedLocale(): string | null {
  try {
    if (typeof localStorage === 'undefined') return null
    return localStorage.getItem(LOCALE_STORAGE_KEY)
  } catch {
    return null
  }
}

/**
 * 设备候选语言。
 *
 * 关键：Android WebView 的 `navigator.language` / `Intl.*` 不跟随系统语言
 * ——真机 (Redmi 14R 5G / Android 14，getprop persist.sys.locale=zh-CN) 上
 * 它们恒为 'en-US'，真实系统语言只出现在 `navigator.languages` 的后续项。
 * 必须把整个候选表一起交给 resolveInitialLocale，只看第一个会丢掉 zh-CN。
 */
function deviceLocaleCandidates(): string[] {
  const out: string[] = []
  if (typeof navigator !== 'undefined') {
    if (Array.isArray(navigator.languages)) out.push(...navigator.languages)
    if (navigator.language) out.push(navigator.language)
  }
  return out
}

/** 实际启动语言：用户显式选择 > 设备候选语言 > en-US。 */
export function resolveStartupLocale(): LocaleType {
  return resolveInitialLocale({
    persisted: readPersistedLocale(),
    candidates: deviceLocaleCandidates(),
  })
}

/** 兼容旧名：等价于 resolveStartupLocale()。 */
export function getBrowserLocale(): LocaleType {
  return resolveStartupLocale()
}

// 创建 i18n 实例
const i18n = createI18n({
  legacy: false, // 使用 Composition API 模式
  locale: resolveStartupLocale(), // 默认语言（用户选择 > 设备语言）
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

if (typeof document !== 'undefined') {
  document.documentElement.lang = i18n.global.locale.value
}

/**
 * 真正切换界面语言：写 i18n runtime + <html lang>。
 * 此前 setLocale / applyAppPrefs 只改 <html lang> 或只改 localStorage，
 * 界面语言纹丝不动——语言因此「保存了但不生效」。
 */
export function applyLocale(locale: LocaleType): void {
  i18n.global.locale.value = locale
  if (typeof document !== 'undefined') document.documentElement.lang = locale
}

export default i18n
