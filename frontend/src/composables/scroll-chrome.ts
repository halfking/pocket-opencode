import type { InjectionKey, Ref } from 'vue'
import type { ScrollHideChrome, ScrollReport } from '../composables/useScrollHideChrome'

export const SCROLL_CHROME_KEY: InjectionKey<ScrollChromeContext> = Symbol('scrollChrome')

/** 输入控件、链接、按钮等不可触发 chrome 切换的区域 */
const INPUT_LINK_SEL =
  'a,button,input,textarea,select,option,label,[contenteditable],[role="link"],[role="button"],[role="textbox"],[role="combobox"],[role="searchbox"],[role="switch"],[role="checkbox"],[role="radio"]'

/** 导航 chrome 自身及其浮层 */
const CHROME_UI_SEL = '.bottom-nav,.chrome-shell,.more-sheet,.more-panel,.refresh-indicator'

/**
 * 单点是否可切换底部导航（及联动顶栏）显示/隐藏。
 * 规则：非输入区、非链接区（含 button）即可；不限定空白/卡片。
 */
export function isChromeToggleTap(target: HTMLElement): boolean {
  if (target.isContentEditable) return false
  if (target.closest(INPUT_LINK_SEL)) return false
  if (target.closest(CHROME_UI_SEL)) return false
  return true
}

/** @deprecated 使用 isChromeToggleTap */
export function isChromeBlankTap(target: HTMLElement, _root: HTMLElement): boolean {
  return isChromeToggleTap(target)
}

export interface ScrollChromeContext extends ScrollHideChrome {
  chromeTotalHeight: Ref<number>
  bottomNavHeight: Ref<number>
  enabled: Ref<boolean>
  topHiddenPx: Ref<number>
  bottomHiddenPx: Ref<number>
}
