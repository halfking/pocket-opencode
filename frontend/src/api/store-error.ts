/**
 * store / 非组件上下文用的错误文案入口。
 *
 * 组件侧用 composables/useApiError，它内部 `useI18n()`，只能在 setup 顶层调用；
 * store 里没有组件实例，调用它不可靠。因此这里走 `i18n.global.t`，
 * 但**归一逻辑仍是同一个** toUserMessage，错误文案与组件侧保持一致。
 *
 * 这不是新发明：aiChatStore.ts 里原本就有一份等价的 `storeApiError`，
 * 注释记着真机证据——「真机曾直接显示 "Failed to fetch" / 英文错误码，
 * 就是没走这一步」。本文件把它提出来，避免每个 store 各写一份。
 */
import { toUserMessage } from './error-message'
import i18n from '../i18n'

/**
 * @param err         捕获到的异常
 * @param fallbackKey 该场景的兜底文案。支持两种写法，与 useApiError 一致：
 *                    - i18n key（推荐）：'errors.loadGatewayFailed'
 *                    - 已翻译好的文案：'模型列表加载失败'
 *                    两种都要支持——i18n 缺词时 t() 会**回显 key 本身**，
 *                    若一律当 key 翻译，用户屏幕上就会看到 "errors.xxx"。
 *                    用 te() 先判一次，既避免回显，也避免文案里带 '.' 时被误当路径。
 */
export function storeApiError(err: unknown, fallbackKey: string): string {
  const i18nGlobal = i18n.global as unknown as { t: (k: string) => string; te?: (k: string) => boolean }
  const translate = (k: string) => i18nGlobal.t(k)
  const fallbackText = i18nGlobal.te ? (i18nGlobal.te(fallbackKey) ? translate(fallbackKey) : fallbackKey) : translate(fallbackKey)
  return toUserMessage(err, translate, fallbackText)
}
