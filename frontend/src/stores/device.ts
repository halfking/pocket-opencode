/**
 * Device tier + STT engine selection.
 *
 * Tiers map to the strategy in docs/2026-07-02-android-stt-evaluation.md
 * section 3. In production the native plugin probes device memory / CPU;
 * here we default to 'unknown' (auto) and let the user override in settings.
 */
import { defineStore } from 'pinia'

export type DeviceTier = 'high' | 'mid' | 'low' | 'unknown'

/**
 * ⚠⚠§859/D22：读盘**必须白名单校验**，不能只靠 `as` 断言。
 *
 * 同一个仓里另外两个 store 早就这么做了：
 *   · stores/theme.ts::readPreference —— `saved === 'light' | 'dark' | 'system'`
 *   · stores/locale.ts::readPersisted —— `matchLocale([saved])`
 * 而这里原来是 `(localStorage.getItem('pocket_device_tier') as DeviceTier) || 'unknown'`
 * ⇒ `as` 只对编译器说话，运行期一个字符串进来就真的成了 DeviceTier。
 *
 * 为什么仍值得改（哪怕写侧 §859/D22 已经加了白名单）：
 *   ① **存量的脏值已经躺在用户的 localStorage 里**（旧版本的下发没有校验），
 *      只补写侧清不掉它们；
 *   ② `localStorage` 是**跨边界输入**——同账号的其它设备 / config-sync 下发
 *      / 旧版本残留，任何一处出错都会落在这里。
 *
 * ⚠ 顺带记录一个**尚未拍板**的事实（属主决定接回 UI 还是摘掉，本节不代拍）：
 *   本 store 目前**全仓零消费**——`useDeviceStore` 在自己的定义文件之外
 *   没有任何引用（无 .vue、无测试、无门脚本），也就是说
 *   「设备档位 / STT 偏好」既没有设置界面，也不影响任何运行时行为，
 *   只有 apply.ts ↔ prefs.ts 在跨设备同步这两个字段。
 *   且 `recommendedModel` 返回的 'vosk-small' **不在** native/sherpa.ts 的
 *   engine 联合（'paraformer' | 'sensevoice' | 'whisper-base' | 'zipformer'）里
 *   ⇒ 这套死状态的取值集合已经与真正在跑的引擎枚举漂移。
 *   登记见 docs/design/2026-10-11-pending-decisions-brief.md（§859.6 第 1 条）。
 */
const TIERS = new Set<DeviceTier>(['high', 'mid', 'low', 'unknown'])
const STT_PREFS = new Set<'local' | 'cloud' | 'auto'>(['local', 'cloud', 'auto'])

function readEnum<T extends string>(key: string, allowed: Set<T>, fallback: T): T {
  try {
    const saved = localStorage.getItem(key)
    if (saved && allowed.has(saved as T)) return saved as T
  } catch {
    // 隐私模式等 localStorage 不可用 ⇒ 回落默认值
  }
  return fallback
}

export const useDeviceStore = defineStore('device', {
  state: () => ({
    tier: readEnum<DeviceTier>('pocket_device_tier', TIERS, 'unknown'),
    /** User override: 'local' = always try on-device first; 'cloud' = prefer cloud. */
    sttPreference: readEnum<'local' | 'cloud' | 'auto'>('pocket_stt_pref', STT_PREFS, 'auto'),
  }),
  getters: {
    shouldPreferLocal: (s) =>
      s.sttPreference === 'local' || (s.sttPreference === 'auto' && s.tier !== 'low'),
    recommendedModel: (s): 'paraformer' | 'sensevoice' | 'whisper-base' | 'vosk-small' => {
      if (s.tier === 'low') return 'vosk-small'
      return 'paraformer' // best Chinese CER on mid/high devices
    },
  },
  actions: {
    setTier(t: DeviceTier) {
      this.tier = t
      // ⚠§867/D34：原来这里是一条裸 setItem。写不进去时 `this.tier = t`
      //   **已经生效**，而存储里还是旧档位 ⇒ 界面显示新档位、STT 选型按新档位走，
      //   重启后无声回到旧值。属于「内存改了、落盘没改」的分歧，
      //   比 §866/D30 更隐蔽：没有任何一处会报错，也没有提示会消失。
      // 姿态：warn 说清「本次会话内有效、重启后回到旧值」，不阻断（档位切换不是关键动作）。
      try {
        localStorage.setItem('pocket_device_tier', t)
      } catch (err) {
        console.warn('[device] 设备档位未能写入本地存储（本次会话内有效，重启后会回到旧值）：', err)
      }
      import('../native/config-sync/prefs').then((m) => m.persistAppPrefs()).catch(() => {})
    },
    setSttPreference(p: 'local' | 'cloud' | 'auto') {
      this.sttPreference = p
      try {
        localStorage.setItem('pocket_stt_pref', p)
      } catch (err) {
        console.warn('[device] 语音识别偏好未能写入本地存储（本次会话内有效，重启后会回到旧值）：', err)
      }
      import('../native/config-sync/prefs').then((m) => m.persistAppPrefs()).catch(() => {})
    },
  },
})
