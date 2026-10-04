/**
 * shell/dockCoordinator.ts — 吸顶（dock）坐标计算。
 *
 * 背景：Tab/表头的吸顶以前靠各页自己算，于是同一个顶栏在不同页面算出不同高度，
 * 表现为「切页后表头差几像素」「旋转后错位」。根因是**写死**了
 * 「顶栏 48 + Tab 40」这种常量。
 *
 * 正确算法（UI规范 07 §4）：
 *   effectiveTop = max(当前滚动视口上沿, 当前可见顶栏下沿)
 *   tabTop       = effectiveTop
 *   tableTop     = effectiveTop + 所属吸顶 Tab 的实际高度 + 区域工具栏高度
 *
 * 三条必须成立的规则：
 *   1. 坐标统一为 **viewport CSS 像素**。用 getBoundingClientRect 而不是
 *      offsetTop/滚动位置混算，否则嵌套滚动容器里必然错位。
 *   2. safe-area 已包含在顶栏**实测下沿**里就不再加第二次。
 *   3. 旋转/分屏/字体放大/键盘变化走 ResizeObserver + visualViewport 更新，
 *      不缓存旧值。
 *
 * 层级按所属交互层分配：区域正文 < 表头 < Tab/工具栏 < 覆盖层菜单 < 覆盖层确认框。
 * ⚠️ 禁止把某个 z-index 固化为全框架规则——吸顶元素不能超出父层
 * stacking context 去压住全局 modal。
 *
 * 纯计算模块，无 Vue 依赖。
 */

/** 一个吸顶候选的登记信息。 */
export interface DockRegion {
  id: string
  /** 真实 DOM 元素。用它的实测矩形，不接受调用方传入的高度。 */
  el: Element
  kind: 'tab' | 'table-header' | 'toolbar'
  /** 所属交互层，用于算层级。层级高的压低的。 */
  layer?: number
  /** 该区域是否已停靠（已停靠的才需要跟随 effectiveTop 移动）。 */
  docked: boolean
}

export interface DockMetrics {
  effectiveTop: number
  tabTop: number
  tableTop: number
}

/** 默认层级：正文 0 < 表头 10 < Tab/工具栏 20。 */
export const LAYER_BODY = 0
export const LAYER_TABLE_HEADER = 10
export const LAYER_TAB_TOOLBAR = 20

export interface DockInputs {
  /** 滚动视口上沿（通常是主内容容器的 getBoundingClientRect().top）。 */
  scrollViewportTop: number
  /** 当前可见顶栏的**实测**下沿；顶栏不可见时传 scrollViewportTop。 */
  visibleTopbarBottom: number
  /** 已停靠 Tab 的实测高度；没有则 0。 */
  dockedTabHeight?: number
  /** 区域工具栏实测高度；没有则 0。 */
  regionToolbarHeight?: number
}

/**
 * 计算吸顶坐标。
 *
 * 显式不做的事：不加 safe-area 常量。顶栏下沿是从 DOM 实测的，
 * 而状态栏 inset 已经被吸进顶栏自身的高度里；再加一次就会双重偏移。
 */
export function computeDockMetrics(input: DockInputs): DockMetrics {
  const effectiveTop = Math.max(input.scrollViewportTop, input.visibleTopbarBottom)
  const tabHeight = Math.max(0, input.dockedTabHeight ?? 0)
  const toolbarHeight = Math.max(0, input.regionToolbarHeight ?? 0)
  return {
    effectiveTop,
    tabTop: effectiveTop,
    tableTop: effectiveTop + tabHeight + toolbarHeight,
  }
}

/**
 * 吸顶元素应施加的纵向位移。
 *
 * 0 表示不动。元素自身自然位置已经在目标线上时不产生位移，
 * 避免「停靠时抖一下」——那通常是重复叠加了 offsetTop。
 */
export function dockTranslateY(el: Element, targetTop: number): number {
  const rect = el.getBoundingClientRect()
  const delta = targetTop - rect.top
  // 亚像素抖动不值得一次重排：小于 0.5px 视为已对齐。
  return Math.abs(delta) < 0.5 ? 0 : delta
}

/**
 * 判断一个 Tab 内容区是否应进入 dock。
 *
 * 条件：Tab 头到达 effectiveTop，且内容区仍穿过其下沿。
 * 向上滑后头始终可见；向下回到原位或滑出整个区域则 undock。
 */
export function shouldDockTab(headerBottom: number, contentBottom: number, targetTop: number): boolean {
  return headerBottom <= targetTop + 0.5 && contentBottom > targetTop + 0.5
}

/**
 * 是否已 undock（离开整个 Tab 区域）。
 *
 * undock 之后才允许应用定义的「左滑返回」——在 dock 状态下左滑是切 Tab。
 */
export function hasUndocked(headerTop: number, regionBottom: number, originalHeaderTop: number): boolean {
  // 头已完全回到（或高于）原始位置以下，且区域已离开视口 → undock
  return headerTop >= originalHeaderTop - 0.5 && regionBottom <= 0
}

/**
 * 层级排序：用于保证确认框压菜单、菜单压吸顶元素。
 *
 * 显式返回新数组而不是就地排序：调用方常把结果缓存下来做 diff。
 */
export function sortByLayer<T extends DockRegion>(regions: readonly T[]): T[] {
  return [...regions].sort((a, b) => (a.layer ?? 0) - (b.layer ?? 0))
}

/**
 * 焦点模式下的可滚动区域边界。
 *
 * 专注工作区里只有这个区域可滚动/横移；背景一律不响应。
 * 这里只做几何，不做事件屏蔽——屏蔽是 FocusWorkspace 的事。
 */
export function focusViewport(input: {
  containerTop: number
  containerBottom: number
  toolbarHeight: number
  safeBottom: number
}): { top: number; height: number } {
  const top = input.containerTop
  const bottom = Math.min(input.containerBottom, window.innerHeight - input.safeBottom)
  return { top, height: Math.max(0, bottom - top - input.toolbarHeight) }
}
