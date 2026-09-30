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
  const { t } = useI18n()

  /**
   * @param err        捕获到的异常（ApiError / Error / 任意值）
   * @param fallbackKey 该场景的通用文案 key（errors.* 命名空间）
   * @returns 可直接展示给用户的文案；无法归类时退回 fallbackKey 的翻译
   */
  return function apiError(err: unknown, fallbackKey: string): string {
    return toUserMessage(err, t, t(fallbackKey))
  }
}
