import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

/**
 * 皮肤偏好：亮色 / 暗色 / 跟随系统。
 * tokens.css 通过 html[data-theme] 消费强制偏好：
 *   data-theme="dark"  → 强制暗色（无视系统）
 *   data-theme="light" → 强制亮色（无视系统）
 *   无属性             → 跟随系统 prefers-color-scheme
 */
export type ThemePreference = 'light' | 'dark' | 'system'

const THEME_KEY = 'app_theme'

const THEME_COLOR_LIGHT = '#667eea'
const THEME_COLOR_DARK = '#0f0f14'

function readPreference(): ThemePreference {
  try {
    const saved = localStorage.getItem(THEME_KEY)
    if (saved === 'light' || saved === 'dark' || saved === 'system') return saved
  } catch {
    // 隐私模式等 localStorage 不可用时回落跟随系统
  }
  return 'system'
}

export const useThemeStore = defineStore('theme', () => {
  const preference = ref<ThemePreference>(readPreference())
  const systemDark = ref(
    typeof window !== 'undefined' &&
      !!window.matchMedia?.('(prefers-color-scheme: dark)').matches
  )

  /** 生效主题（考虑手动覆盖后的最终结果），驱动状态栏 / theme-color meta */
  const isDark = computed(
    () => preference.value === 'dark' || (preference.value === 'system' && systemDark.value)
  )

  function applyTheme() {
    if (typeof document === 'undefined') return
    const root = document.documentElement
    if (preference.value === 'system') {
      root.removeAttribute('data-theme')
    } else {
      root.setAttribute('data-theme', preference.value)
    }
    // color-scheme 联动表单控件 / 滚动条的 UA 深浅渲染
    root.style.colorScheme = isDark.value ? 'dark' : 'light'
    // 浏览器地址栏 / WebView 顶栏颜色跟随生效主题（meta 带 media 属性，
    // 浏览器按系统挑一条，这里把两条都写成生效色即可覆盖手动选择）
    document
      .querySelectorAll('meta[name="theme-color"]')
      .forEach(m => m.setAttribute('content', isDark.value ? THEME_COLOR_DARK : THEME_COLOR_LIGHT))
  }

  function setPreference(p: ThemePreference) {
    preference.value = p
    try {
      localStorage.setItem(THEME_KEY, p)
    } catch {
      // 存不进去也照常生效（本次会话内）
    }
    applyTheme()
  }

  // 跟随系统模式下监听系统深浅变化（store 为应用级单例，监听随应用存活）
  if (typeof window !== 'undefined' && window.matchMedia) {
    const mql = window.matchMedia('(prefers-color-scheme: dark)')
    mql.addEventListener?.('change', e => {
      systemDark.value = e.matches
      applyTheme()
    })
  }

  applyTheme()

  return { preference, systemDark, isDark, setPreference }
})
