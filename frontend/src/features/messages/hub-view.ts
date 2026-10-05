/**
 * 「消息」tab 顶部两档看法的 URL 同步（纯逻辑，无 Vue 依赖）。
 *
 * ## 为什么要抽出来
 *
 * MessagesHubView.vue 里这段逻辑一开始是内联在 setView 里的：加一个 query 参数、
 * 切回去时删掉。看着只有三行，但它有真实边界情形，而组件层没有任何测试手段
 * （本仓没装 @vue/test-utils / vitest，Vue 组件行为是**不可测**的——
 * 这也是日历那边把区间判断抽成 calendar-view.ts 的同一个理由）。
 *
 * 边界情形：MessagesHubView 已经有来源 chips 与 ?source= 深链
 * （见 SourceFilterBar 注释：从 /notes?source=meeting 这类深链进来时
 * 当前 chip 可能在屏外）。于是出现一条硬要求——
 * **切到日历再切回来，不能把 source 等其他 query 参数吃掉**。
 * 内联写法 `{ ...route.query, view }` 恰好对，但这是「碰巧对」：
 * 谁把 spread 简化成 `{ view }` 就静默吃掉 source，界面上看不出任何异常，
 * 只是深链进来的用户回到时间线时筛选被重置了。
 */
import type { LocationQuery } from 'vue-router'

/** 时间线 / 日历。两档互斥，形态完全不同（见 MessagesHubView 头注释）。 */
export type HubView = 'timeline' | 'calendar'

/** 默认档。**任何解析不出来的情况都回落时间线**——宁可多显示一屏，也不空白。 */
export const DEFAULT_HUB_VIEW: HubView = 'timeline'

/**
 * 从 query 起步解析当前档。
 *
 * vue-router 的 query 值类型是 `string | null | (string | null)[]`，
 * 数组值（`?view=a&view=b`）在这里判为不合法 → 回落默认档。
 * 这是有意的：出现两个 view 时没有「取哪个」的正确答案，
 * 随便取一个会让 URL 与界面之间出现无法解释的偏差。
 */
export function parseHubView(raw: unknown): HubView {
  if (typeof raw !== 'string') return DEFAULT_HUB_VIEW
  return raw === 'calendar' ? 'calendar' : DEFAULT_HUB_VIEW
}

/**
 * 切档后 URL 应该变成什么样。**返回新对象，不改入参**：
 * 直接改 route.query 会让 vue-router 认为「值变了」而触发一次多余导航。
 *
 * 时间线档是把 `view` **删掉**而不是写成 `view=timeline`：
 * URL 是这个 tab 的默认态，参数只用来表达偏离默认值。深链分享出去的
 * `/messages` 应当是干净的一个，而不是带一个谁都不需要的 `?view=timeline`。
 */
export function hubViewQuery(target: HubView, current: LocationQuery): LocationQuery {
  if (target === 'calendar') {
    if (current.view === 'calendar') return current
    return { ...current, view: 'calendar' }
  }
  if (!current.view) return current
  const next = { ...current }
  delete next.view
  return next
}
