/**
 * useApiError —— 把 catch 到的任意值转成可渲染的本地化错误文案。
 *
 * 补 BUG-O（942a379）遗留的缺失模块：那两个调用点 import 的是本文件，
 * 但模块从未被创建，main 的 `vue-tsc --noEmit` 一直报 TS2307。
 *
 * 用法（必须在 setup 顶层调用，因为内部用 useI18n）：
 *   const apiError = useApiError()
 *   error.value = apiError(e, t('flashcards.error.saveFailed'))
 */
import { useI18n } from 'vue-i18n'
import { resolveApiErrorMessage, shouldSurfaceRawMessage } from './api-error-message'

export function useApiError(): (err: unknown, fallback: string) => string {
  const { t } = useI18n()
  return (err: unknown, fallback: string) =>
    shouldSurfaceRawMessage(err) ? resolveApiErrorMessage(err, fallback) : fallback
}

export { resolveApiErrorMessage, shouldSurfaceRawMessage }
