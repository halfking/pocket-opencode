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

/**
 * ⚠⚠§869/D37/D38：以前返回 void，调用方**无从知道**远端下发有没有落地 ——
 * 而它紧接着就会 `markSettingClean`，把这条设置标记成「已是最新」
 * （`writeLocalIfNewer` 写的是 `local_user_settings` 且 `dirty = 0`）。
 * 两套存储的结果是：**DB 说干净、localStorage 投影写失败 ⇒ 永不重试**。
 * 现在返回失败字段名（成功时是空数组），让调用方至少有地方挂日志。
 */
export function applyRemoteSetting(rec: RemoteSetting): string[] {
  const payload = (rec.payload && typeof rec.payload === 'object') ? rec.payload as Record<string, unknown> : {}
  if (rec.namespace === 'app_prefs' && rec.id === 'default') {
    return applyAppPrefs(payload as AppPrefs)
  }
  if (rec.namespace === 'chat_settings' && rec.id === 'default') {
    return applyChatSettings(payload as ChatSettingsPayload) ? [] : ['AI 对话设置']
  }
  if (rec.namespace === 'connection' && rec.id === 'default') {
    const inst = payload as Partial<SelectedInstance>
    if (inst.id) writeSelectedInstance(inst as SelectedInstance)
  }
  return []
}

function applyAppPrefs(prefs: AppPrefs): string[] {
  // ⚠⚠§869/D38：这里原来是一个**大 try 包住全部字段**加一个空 catch。
  // 两个后果：
  //  ① **连坐**：第 64 行 `app_theme` 一旦抛错，第 66/67 行的两个写入
  //     **以及** 68–72 行真正把皮肤应用到 `<html data-theme>` 的那段
  //     **全部不会执行** —— 一个字段写不进去，整段远端下发静默作废。
  //  ② **零可见性**：catch 里只有一句「localStorage may be unavailable」，
  //     没有任何日志，用户与事后排查都看不到「远端设置没生效」这件事。
  // 修法：逐字段独立处理，一个失败不拖累其余；失败的字段名回报给调用方。
  const failed: string[] = []
  const theme = prefs.theme && APP_THEMES.has(prefs.theme) ? prefs.theme : ''
  const deviceTier = prefs.deviceTier && APP_DEVICE_TIERS.has(prefs.deviceTier) ? prefs.deviceTier : ''
  const sttPref = prefs.sttPref && APP_STT_PREFS.has(prefs.sttPref) ? prefs.sttPref : ''

  const persist = (key: string, value: string, label: string) => {
    try {
      localStorage.setItem(key, value)
    } catch (err) {
      failed.push(label)
      console.warn(`[config-sync] 远端下发的 ${label} 未能写入本地存储（同步状态仍会被标记为「已是最新」，不会重试）：`, err)
    }
  }

  if (theme) persist('app_theme', theme, '主题')
  if (prefs.locale) applyRemoteLocale(prefs.locale)
  if (deviceTier) persist('pocket_device_tier', deviceTier, '设备档位')
  if (sttPref) persist('pocket_stt_pref', sttPref, '语音识别偏好')

  // ⭐ DOM 更新**移出**写入路径：即使落盘失败也要把已经拿到的皮肤应用上去，
  //   否则会出现「存储与界面各说各话」的半生效状态。
  if (theme && typeof document !== 'undefined') {
    try {
      const root = document.documentElement
      if (theme === 'system') root.removeAttribute('data-theme')
      else root.setAttribute('data-theme', theme)
    } catch (err) {
      failed.push('主题(界面)')
      console.warn('[config-sync] 远端下发的主题未能应用到界面：', err)
    }
  }
  return failed
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

function applyChatSettings(settings: ChatSettingsPayload): boolean {
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
        preserveCorruptRaw('pocket:ai-chat:corrupt', 'settings', raw)
        current = {}
      }
    }
    localStorage.setItem(key, JSON.stringify({ ...current, ...settings }))
  } catch (err) {
    // ⚠⚠⚠§869/D37：这一处原来只有 `// ignore` —— 而它守着的正是
    // §859/D24 刚刚修好的那份数据。§859 的注释里写着失败后的现象：
    // 「用户只会在另一台设备改了设置、然后发现这边怎么改都没反应」。
    // ⚠ 读侧（§858/D21）已能留证，写侧却仍在静默丢弃 ⇒ 两头不对称：
    //   损坏时留证据，写不进去时连证据都没有。
    // 而且这条 key 是**双写**（aiChatStore.persist 也会写它）⇒ 静默丢弃之后，
    // 本地 store 下一次 persist 会用本地值把它覆盖，远端值彻底蒸发。
    console.warn('[config-sync] 远端下发的 AI 对话设置未能写入本地存储（该设备将保持旧设置，且不会重试）：', err)
    return false
  }
  return true
}
