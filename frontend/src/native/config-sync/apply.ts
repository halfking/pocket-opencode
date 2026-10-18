import type { RemoteSetting } from '../../api/user-settings'
import { writeSelectedInstance, type SelectedInstance } from '../../config/selected-instance'
import { preserveCorruptRaw } from '../../utils/corrupt-preserve'

type AppPrefs = {
  theme?: 'light' | 'dark' | 'system'
  locale?: string
  deviceTier?: string
  sttPref?: string
}

/**
 * ⚠⚠§859/D22：远端 app_prefs 里的三个枚举字段此前**未校验就落盘**。
 *
 * 这是**同一个函数里的同族漏网**：`applyRemoteLocale` 的注释明写着
 * 「另一处缺陷：未规范化的字符串被直接写进 app_locale 与 <html lang>……
 *   现在：只在能匹配到受支持语言时才落盘并切换，匹配不到则完全忽略」
 * —— 也就是说，locale 已经因为完全相同的原因修过，
 * 而同一函数里的 theme / deviceTier / sttPref 被留在了坏的那一边。
 *
 * 危害不是「多写了一个字符串」，而是这三个值都被下游当成**领域枚举**使用：
 *   · theme：stores/theme.ts 读盘时做白名单校验，非法值下次启动会被回落成
 *     'system' ⇒ **跨设备同步过来的皮肤在重启后静默失效**；
 *     而本次 applyRemoteSetting 又会把非法值写进 <html data-theme>。
 *   · deviceTier / sttPref：stores/device.ts 读盘只做 `as` 断言不校验，
 *     且这两个值随后会被 feeds 回上报（config-sync/prefs.ts）⇒ 一次非法值
 *     会**跨设备扩散**。
 * ⇒ 修法与 applyRemoteLocale 同构：不在白名单里就**完全忽略**，不落盘不生效。
 */
const APP_THEMES = new Set(['light', 'dark', 'system'])
const APP_DEVICE_TIERS = new Set(['high', 'mid', 'low', 'unknown'])
const APP_STT_PREFS = new Set(['local', 'cloud', 'auto'])

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
    // §859/D22：匹配不到受支持取值就完全忽略（与 applyRemoteLocale 同构）。
    const theme = prefs.theme && APP_THEMES.has(prefs.theme) ? prefs.theme : ''
    const deviceTier = prefs.deviceTier && APP_DEVICE_TIERS.has(prefs.deviceTier) ? prefs.deviceTier : ''
    const sttPref = prefs.sttPref && APP_STT_PREFS.has(prefs.sttPref) ? prefs.sttPref : ''
    if (theme) localStorage.setItem('app_theme', theme)
    if (prefs.locale) applyRemoteLocale(prefs.locale)
    if (deviceTier) localStorage.setItem('pocket_device_tier', deviceTier)
    if (sttPref) localStorage.setItem('pocket_stt_pref', sttPref)
    if (theme && typeof document !== 'undefined') {
      const root = document.documentElement
      if (theme === 'system') root.removeAttribute('data-theme')
      else root.setAttribute('data-theme', theme)
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
    const raw = localStorage.getItem(key)
    // ⚠⚠§859/D24：此前这里是 `JSON.parse(localStorage.getItem(key) || '{}')`，
    // 解析失败被下面那个 `catch { /* ignore */ }` 整个吞掉 ⇒
    // **整份远端聊天设置被静默丢弃**，用户毫无察觉（他只会在另一台设备改了设置、
    // 然后发现这边怎么改都没反应）。
    // ⚠ 解析失败**不能**反过来让远端更新也一起丢掉：远端才是这次的数据源，
    //   本地这份损坏的只是合并基底 ⇒ 以 `{}` 为基底继续应用，并把原始串留证
    //   （读侧 §858/D21 已经这么处理同一个 key，这里补上写侧）。
    let current: Record<string, unknown> = {}
    if (raw) {
      try {
        const parsed = JSON.parse(raw)
        current = parsed && typeof parsed === 'object' ? (parsed as Record<string, unknown>) : {}
      } catch {
        preserveCorruptRaw('settings', raw)
        current = {}
      }
    }
    localStorage.setItem(key, JSON.stringify({ ...current, ...settings }))
  } catch {
    // ignore
  }
}
