/**
 * 把捕获到的异常转成给用户看的文案（组件侧入口）。
 *
 * 为什么要这一层：`api/error-message.ts` 是纯函数、不依赖 vue-i18n，
 * 而调用方散落在 30+ 个视图/Store 里。让每个文件都写
 * `const { t } = useI18n()` + `toUserMessage(e, t, t('...'))` 太啰嗦，
 * 这里包一层，调用方只需两行：
 *
 *   const apiError = useApiError()
 *   loadError.value = apiError(e, 'errors.loadEmailFailed')
 *
 * 用法约束：只能在 setup 顶层调用（内部用了 useI18n）。
 * 非组件上下文（Store / 纯 composable）请直接用 api/error-message.ts 的
 * toUserMessage，并把 t 从调用方传进来。
 */
import { useI18n } from 'vue-i18n'
import { toUserMessage } from '../api/error-message'

export function useApiError() {
  const { t, te } = useI18n()

  /**
   * @param err        捕获到的异常（ApiError / Error / 任意值）
   * @param fallback   该场景的兜底文案。支持两种写法：
   *                   - i18n key（推荐）：'errors.loadEmailFailed'
   *                   - 已翻译好的文案：t('flashcards.error.saveFailed')
   *                   两种都要支持——flashcards 那批调用点传的是后者，
   *                   若一律当 key 翻译，t() 找不到会回显原文，正好可用；
   *                   但显式判一次更稳，避免文案里带 '.' 时被误当路径解析。
   * @returns 可直接展示给用户的文案；无法归类时退回 fallback
   */
  return function apiError(err: unknown, fallback: string): string {
    const fallbackText = te(fallback) ? t(fallback) : fallback
    return toUserMessage(err, t, fallbackText)
  }
}
