/** /ai 首页快速提问：输入框默认关闭，点相关入口才打开。 */
export type QuickPromptState = 'hidden' | 'open'

export const QUICK_PROMPT_DEFAULT: QuickPromptState = 'hidden'

export function toggleQuickPrompt(current: QuickPromptState): QuickPromptState {
  return current === 'open' ? 'hidden' : 'open'
}

export function closeQuickPrompt(_current?: QuickPromptState): QuickPromptState {
  return 'hidden'
}

/** 隐藏时不占底部 chrome 让位。 */
export function quickPromptInset(state: QuickPromptState, height: number): number {
  return state === 'open' ? height : 0
}
