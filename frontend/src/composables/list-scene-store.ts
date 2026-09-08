/**
 * list-scene-store — 列表"现场"状态的模块级存储（纯数据，无 Vue 依赖，
 * 便于 node --test 直接覆盖）。
 *
 * 背景：App.vue 的 <router-view> 此前无 KeepAlive，列表→详情→返回会销毁
 * 重建列表组件，筛选/分类/滚动位置全部丢失。引入 KeepAlive（include 白名单）
 * 后组件实例被缓存，筛选态天然保留；本模块补齐两件事：
 *   1. dirty 登记：详情页对列表数据做过后端变更（已读/归档/删除等）时登记，
 *      列表 onActivated 时消费该标记决定是否刷新——"只有数据真的变了才刷新"；
 *   2. 滚动记忆：shell 滚动页的滚动容器是共享的 <main>，中途被其它页面
 *      改写 scrollTop，因此按 scope 在失活时保存、激活时恢复。
 */

const dirtyScopes = new Set<string>()
const scrollMemory = new Map<string, number>()

/** 详情页在成功修改列表可见数据后调用。 */
export function markListDirty(scope: string): void {
  dirtyScopes.add(scope)
}

/** 只读检查（不消费）。 */
export function peekListDirty(scope: string): boolean {
  return dirtyScopes.has(scope)
}

/** 消费脏标记：登记过则清除并返回 true，否则 false。 */
export function consumeListDirty(scope: string): boolean {
  return dirtyScopes.delete(scope)
}

export function rememberListScroll(scope: string, top: number): void {
  scrollMemory.set(scope, top)
}

/** 返回保存过的滚动位置；从未保存过返回 -1。 */
export function restoreListScroll(scope: string): number {
  const top = scrollMemory.get(scope)
  return typeof top === 'number' ? top : -1
}

/** 登出/账号切换等场景整体作废现场记忆。 */
export function clearListScenes(): void {
  dirtyScopes.clear()
  scrollMemory.clear()
}
