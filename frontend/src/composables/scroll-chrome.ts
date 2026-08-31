import type { InjectionKey, Ref } from 'vue'
import type { ScrollHideChrome, ScrollReport } from '../composables/useScrollHideChrome'

/**
 * 滚动联动 chrome（底部 tabbar + 视图输入区）的 provide/inject 契约。
 * 壳层 AppLayout 持有引擎并 provide；滚动容器把增量喂给 reportScroll：
 * - scrollMode:'shell' 页面由 AppLayout 绑定 <main>；
 * - 自管滚动页面：PullToRefresh（内置上报）、AIChatView .msg-area 等。
 * 消费方统一读 --bottom-chrome-hide CSS 变量做位移（BottomNav / composer /
 * voice-bar / TasksView voice-bar）。
 */
export const SCROLL_CHROME_KEY: InjectionKey<ScrollChromeContext> = Symbol('scrollChrome')

/** 输入控件、链接、按钮等不可触发 chrome 唤出的区域 */
const INPUT_LINK_SEL =
  'a,button,input,textarea,select,option,label,[contenteditable],[role="link"],[role="button"],[role="textbox"],[role="combobox"],[role="searchbox"],[role="switch"],[role="checkbox"],[role="radio"]'

/** 导航 chrome 自身、底部输入区及其浮层 */
const CHROME_UI_SEL =
  '.bottom-nav,.chrome-shell,.more-sheet,.more-panel,.refresh-indicator,.composer,.voice-bar,.uc'

/**
 * 单点是否可唤出底部 chrome（点击内容区触发）。
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
  /** 底部 chrome 总高（导航 + 视图输入区），即完全隐藏的位移量 */
  chromeTotalHeight: Ref<number>
  bottomNavHeight: Ref<number>
  /** 视图自有的底部输入区高度（/ai-chat composer、/ai voice-bar）；无输入区视图为 0 */
  bottomInsetHeight: Ref<number>
  enabled: Ref<boolean>
  /** 预留：顶栏联动隐藏量（当前仅 SettingsLLMGateway 局部使用，未接入全局） */
  topHiddenPx: Ref<number>
  bottomHiddenPx: Ref<number>
}

export type { ScrollReport }
