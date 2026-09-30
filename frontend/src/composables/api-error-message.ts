/**
 * 纯错误文案归一函数（BUG-U，2026-09-30）。
 *
 * 背景：BUG-O 的提交（942a379）在 FlashcardListView / FlashcardEditView 里
 * `import { useApiError } from '../../composables/useApiError'`，但这个模块
 * 从未存在，`vue-tsc --noEmit` 直接 TS2307，main 分支的 typecheck 是断的。
 * 同一个提交还引用了两个同样不存在的 i18n 键 `errors.loadFlashcardsFailed` /
 * `errors.saveFailed`（全部 locale 都没有 `errors` 这个顶层命名空间）。
 *
 * 这里补上契约：把任意 catch 到的值转成一句能直接渲染的文案，
 * 拿不到有意义的消息时退回调用方给的已本地化 fallback。
 *
 * 纯函数、不 import vue —— node --test 可直接加载。
 */

/** ApiError 形状（与 api/http.ts 的 class 鸭子类型一致，避免在此 import 以免循环依赖）。 */
interface MaybeApiError {
  message?: unknown
  status?: unknown
}

function isBlank(value: string): boolean {
  return value.trim().length === 0
}

/**
 * @param err       catch 到的任意值（Error / ApiError / 字符串 / undefined）
 * @param fallback  已经本地化好的兜底文案（不要传 i18n 键，键由调用方 t() 好再传进来）
 */
export function resolveApiErrorMessage(err: unknown, fallback: string): string {
  if (typeof err === 'string' && !isBlank(err)) return err
  if (err instanceof Error && !isBlank(err.message)) return err.message
  // 非 Error 对象（例如后端返回裸 { error: "..." } 之类）也尝试取 message
  if (err && typeof err === 'object') {
    const m = (err as MaybeApiError).message
    if (typeof m === 'string' && !isBlank(m)) return m
  }
  return fallback
}

/**
 * 带 HTTP 状态码的错误是否值得展示原始消息。
 *
 * 5xx 的消息经常是后端内部细节或 HTML 片段，直接糊到界面上对用户没有意义，
 * 这种情况退回兜底文案更合适。4xx 是用户可纠正的（如 409 冲突），保留原消息。
 */
export function shouldSurfaceRawMessage(err: unknown): boolean {
  if (!err || typeof err !== 'object') return true
  const status = (err as MaybeApiError).status
  if (typeof status !== 'number') return true
  return status < 500
}
