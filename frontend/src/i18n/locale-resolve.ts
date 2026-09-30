/**
 * 界面语言解析（纯函数，无 JSON / vue-i18n 依赖，便于单测）。
 *
 * 缺陷背景（真机 Redmi 14R 5G / Android 14 实测）：
 *   - 设备真实语言 `getprop persist.sys.locale` = zh-CN；
 *   - 但 WebView 的 navigator.language 与 Intl.* 全部是 'en-US'，
 *     设备语言只出现在 navigator.languages 的后续项；
 *   - 旧实现只读 navigator.language，于是中文系统启动成英文；
 *   - 且 app_locale 从不参与初始语言计算，<html lang> 与 i18n runtime
 *     各写各的，出现「lang=zh-CN 但界面英文」的错配。
 */

export const SUPPORT_LOCALES = [
  'zh-CN',
  'zh-TW',
  'en-US',
  'ja-JP',
  'ko-KR',
  'de-DE',
  'fr-FR',
  'es-ES',
  'pt-BR'
] as const

export type LocaleType = (typeof SUPPORT_LOCALES)[number]

export const LOCALE_STORAGE_KEY = 'app_locale'

/** 按候选顺序取第一个受支持的语言；精确匹配优先，其次按语言前缀回退。 */
export function matchLocale(candidates: readonly string[]): LocaleType | null {
  for (const raw of candidates) {
    if (!raw) continue
    const tag = raw.trim()
    if (!tag) continue
    if ((SUPPORT_LOCALES as readonly string[]).includes(tag)) return tag as LocaleType
    const prefix = tag.split('-')[0]?.toLowerCase()
    if (!prefix) continue
    const matched = (SUPPORT_LOCALES as readonly string[]).find(
      (l) => l.split('-')[0].toLowerCase() === prefix,
    )
    if (matched) return matched as LocaleType
  }
  return null
}

/** 启动语言：用户显式选择 > 设备候选语言 > en-US。入参可注入以便单测。 */
export function resolveInitialLocale(opts?: {
  persisted?: string | null
  candidates?: readonly string[]
}): LocaleType {
  if (opts?.persisted) {
    const hit = matchLocale([opts.persisted])
    if (hit) return hit
  }
  return matchLocale(opts?.candidates ?? []) ?? 'en-US'
}
