/**
 * shell/index.ts — Hyper 运行时的统一出口。
 *
 * 存在的理由：避免各处直接 import 具体文件后**另造全局名**。
 * 所有 Hyper 能力从这里取，版本号一致，将来加协议版本只有一个地方要改。
 *
 * ⚠️ 导入本模块**不等于**获得原生能力。`hyperMode`（形态）与
 * `capabilities`（能力）是两件事，见 capabilities.ts。
 */

export * from './types.ts'
export { NavigationContextStore, sanitizePath, validateContext } from './navigationContext.ts'
export type { Clock } from './navigationContext.ts'
export { TitleResolver } from './titleResolver.ts'
export type { ResolvedTitle, TitleDomProbe, TitleResolverOptions } from './titleResolver.ts'
export { BackDispatcher } from './backDispatcher.ts'
export type { BackAffordance, OverlayHandle, RouterAdapter } from './backDispatcher.ts'
export { createContinuousList } from './continuousList.ts'
export type {
  ContinuousListController,
  ContinuousListDeps,
  ListRow,
  ListStatus,
  RefreshPolicy,
  RefreshStatus,
  RowsMeta,
} from './continuousList.ts'
export { createHyperPages } from './hyperPages.ts'
export type { HyperPages } from './hyperPages.ts'
export {
  computeDockMetrics,
  dockTranslateY,
  hasUndocked,
  shouldDockTab,
  sortByLayer,
  focusViewport,
  LAYER_BODY,
  LAYER_TABLE_HEADER,
  LAYER_TAB_TOOLBAR,
} from './dockCoordinator.ts'
export type { DockInputs, DockMetrics, DockRegion } from './dockCoordinator.ts'
export { FocusWorkspace, shouldBlockBackgroundEvent } from './focusWorkspace.ts'
export type { FocusDomAdapter, FocusSnapshot, FocusStatus } from './focusWorkspace.ts'
export { detectCapabilities, defaultProbes, requireCapability } from './capabilities.ts'
export type { CapabilityProbes, CapabilityResult } from './capabilities.ts'
export { createShellRuntime, getShellRuntime, dispatchBack, __resetShellRuntimeForTest } from './runtime.ts'
export type { RouterLike, ShellRuntime } from './runtime.ts'

/** Hyper 运行时协议版本。与 capabilities().protocolVersion 保持一致。 */
export const HYPER_PROTOCOL_VERSION = 2
