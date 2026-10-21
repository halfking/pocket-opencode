import { writeSelectedInstance, readSelectedInstance, type SelectedInstance } from '../../config/selected-instance'
import { matchLocale, resolveInitialLocale, type LocaleType } from '../../i18n/locale-resolve'
import { saveSettingLocalFirst } from './runtime'

export function currentAppPrefs(): Record<string, string> {
  return {
    theme: safeGet('app_theme') || 'system',
    // 缺陷：此处原先上报 'zh'，不是受支持 locale，也不是合法 BCP-47 标签。
    // 远端同步会把它写回 app_locale 并设置 <html lang>，造成 runtime 与 lang 错配。
    locale: currentLocale(),
    deviceTier: safeGet('pocket_device_tier') || 'unknown',
    sttPref: safeGet('pocket_stt_pref') || 'auto',
  }
}

/** 上报语言必须规范化：用户已存的值优先，否则与界面实际生效的语言保持一致。 */
function currentLocale(): LocaleType {
  const stored = safeGet('app_locale')
  if (stored) {
    const hit = matchLocale([stored])
    if (hit) return hit
  }
  // 界面实际生效的语言（applyLocale 会把 i18n runtime 与 <html lang> 同步写齐）
  if (typeof document !== 'undefined') {
    const hit = matchLocale([document.documentElement.lang || ''])
    if (hit) return hit
  }
  return resolveInitialLocale({ candidates: deviceLocaleCandidates() })
}

function deviceLocaleCandidates(): string[] {
  if (typeof navigator === 'undefined') return []
  const out: string[] = []
  if (Array.isArray(navigator.languages)) out.push(...navigator.languages)
  if (navigator.language) out.push(navigator.language)
  return out
}

/**
 * §948：这两个函数原来返回 `void`（内部 `void saveSettingLocalFirst(...)`），
 * 于是调用点写 `import(...).then((m) => m.persistAppPrefs()).catch(() => {})` 时，
 * **那个 catch 根本接不到东西** —— 被丢弃的是 `saveSettingLocalFirst` 内部的
 * 那个 promise，它的拒绝会变成**未处理 rejection**，而外层的 catch 看着像有保护。
 * ⇒ 改成把 promise 返回出去：调用点的 catch 这才真的接得住（§948 已把那里改成留痕）。
 */
export function persistAppPrefs(): Promise<void> {
  return saveSettingLocalFirst('app_prefs', 'default', currentAppPrefs())
}

export function persistChatSettings(settings: unknown): Promise<void> {
  return saveSettingLocalFirst('chat_settings', 'default', settings)
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
