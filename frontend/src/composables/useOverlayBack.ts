// useOverlayBack.ts — 把一个覆盖层登记到 `BackDispatcher`。
//
// ⚠️ 2026-10-06 新增。这条链路原本**整段不存在**，后果是可见的：
//   · `BackDispatcher.registerOverlay()` 定义完整、12 条单测覆盖「覆盖层优先」，
//     却**全仓零调用**（连测试都没调过它）。
//   · `Dialog` / `BottomSheet` 都是 `<Teleport to="body">`，所以弹窗**不是**页面
//     组件的子节点。
//   ⇒ Android 硬件返回键走 `BackDispatcher` 时 overlay 栈恒空，
//     「覆盖层优先」分支永不触发 ⇒ **路由后退，而弹窗留在屏幕上**。
//     `AppLayout.vue:222` 的注释正好写着它想避免这个（「避免…关了弹窗又跳路由」）。
//
// 为什么必须 provide/inject 而不是弹窗自己 `getShellRuntime(router)`：
//   `getShellRuntime` 是进程内**单例**且**必须传 router**（路由守卫靠它安装）。
//   弹窗自己调会造出第二个没有 router 的实例（守卫全失效），或依赖调用顺序。
//   ⇒ 复用 AppLayout 已经建好的那个实例。见 `SHELL_RUNTIME_KEY`。
//
// 注销时机覆盖三种（少一种就会留下「幽灵拦截」）：
//   ① visible 变 false（正常关闭）
//   ② 组件卸载（父组件直接被路由换掉）
//   ③ 重新注册前先注销旧的（`visible` 快速反复时不会堆叠）

import { inject, onBeforeUnmount, watch, type Ref } from 'vue'
import { SHELL_RUNTIME_KEY, peekShellRuntime, type ShellRuntime } from '../lib/shell/runtime.ts'
import type { Presentation } from '../lib/shell/types.ts'

export interface OverlayBackOptions {
  /** 是否处于打开态。 */
  visible: Ref<boolean>
  /** 展示形态。决定返回键的语义（关弹窗 / 退出专注）与 `affordance` 的文案。 */
  presentation: Exclude<Presentation, 'page'>
  /** 真正关闭的动作。通常是组件自己的 close（它要负责 emit + 解滚动锁）。 */
  close: () => void | Promise<void>
  /** 有未保存数据时拒绝关闭（`false` 即拒绝）。 */
  beforeClose?: () => boolean | Promise<boolean>
  /** 稳定的实例 id。同一 id 重复注册时后注册的接管（嵌套同名弹窗）。 */
  id: string
  /**
   * 运行时实例。**只给单测用**——产品路径一律走 `inject`，
   * 否则弹窗会各自造实例，等于没有单例。
   */
  runtime?: ShellRuntime
}

/**
 * @returns 实际登记到的运行时（没注入时为 null）。
 *   调用方**不需要**用返回值；暴露它只是为了单测能断言。
 */
export function useOverlayBack(opts: OverlayBackOptions): ShellRuntime | null {
  // ⚠️ 两级取用，缺一不可（2026-10-06 设备实跑踩出来的）：
  //   ① inject：树内传递，正常路径。
  //   ② peekShellRuntime：**只读**兜底。必须有它 ——
  //      全局弹窗挂在 `App.vue`，是 `<AppLayout>` 的**兄弟节点**，
  //      根本不在 AppLayout 的 provide 作用域内，inject 必然返回 null。
  //      第一版只有 inject，于是设备上「按返回键弹窗不关」。
  //   不能用 `getShellRuntime(router)` 兜底：它会**建**一个没有路由守卫的实例。
  const runtime = opts.runtime ?? inject(SHELL_RUNTIME_KEY, null) ?? peekShellRuntime()
  let unregister: (() => void) | null = null

  const release = () => {
    if (!unregister) return
    unregister()
    unregister = null
  }

  const register = () => {
    // ③ 先清旧的，避免 visible 抖动时同一个弹窗叠出多条拦截
    release()
    if (!runtime) return
    unregister = runtime.back.registerOverlay({
      id: opts.id,
      presentation: opts.presentation,
      close: opts.close,
      ...(opts.beforeClose ? { beforeClose: opts.beforeClose } : {}),
    })
  }

  watch(
    () => opts.visible.value,
    (on) => {
      if (on) register()
      else release()
    },
    { immediate: true },
  )

  // ② 父组件被路由换掉时也要注销：Teleport 的内容不在父组件的卸载路径上，
  //    不注销就会留下一条永远拦返回键的幽灵记录。
  onBeforeUnmount(release)

  return runtime
}
