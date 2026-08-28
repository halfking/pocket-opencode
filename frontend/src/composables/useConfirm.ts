/**
 * useConfirm — 全局确认对话框 composable。
 *
 * 在 App.vue 顶层挂一次 <ConfirmDialog ref="confirmDialogRef" /> 后，
 * 任何视图都可以 `const { confirm } = useConfirm()` 然后
 * `if (await confirm({ message: '删除该任务？', danger: true })) { ... }`。
 *
 * 实现方式：注册表模式（模块级单例）。App.vue 挂载时调用
 * registerConfirmResolver()，ConfirmDialog 自己把 ask() 注册进来。
 * 若组件尚未挂载（极端时序），fallback 到原生 confirm，保证功能不间断。
 */
import type { Ref } from 'vue'
import type ConfirmDialog from '../components/base/ConfirmDialog.vue'

export interface ConfirmOptions {
  title?: string
  message: string
  confirmText?: string
  cancelText?: string
  danger?: boolean
  loading?: boolean
}

type AskFn = (options: ConfirmOptions) => Promise<boolean>

let registeredAsk: AskFn | null = null

/** 由 ConfirmDialog 在 onMounted 时调用：把自身 ask 注册到单例。 */
export function registerConfirmHandler(ask: AskFn) {
  registeredAsk = ask
}

/** 由 ConfirmDialog 在 onUnmounted 时调用，防止悬挂引用。 */
export function unregisterConfirmHandler(ask: AskFn) {
  if (registeredAsk === ask) registeredAsk = null
}

export function useConfirm() {
  /**
   * 弹出全局确认框并等待用户选择。
   * - 确认 → resolve(true)
   * - 取消 / 关闭 / 组件卸载 → resolve(false)
   */
  async function confirm(options: ConfirmOptions): Promise<boolean> {
    if (registeredAsk) return registeredAsk(options)
    // 组件尚未挂载的兜底（例如启动期的同步代码路径）
    return window.confirm(options.message)
  }

  return { confirm }
}
