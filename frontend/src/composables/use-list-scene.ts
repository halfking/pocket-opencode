/**
 * useListScene — 列表页"回到原现场"生命周期接线（KeepAlive 配套）。
 *
 * 用法（列表页 <script setup> 内）：
 *   defineOptions({ name: 'EmailInboxView' })   // 与 App.vue 的 include 名单一致
 *   useListScene('email', load)                  // load = 该列表的刷新函数
 *
 * 行为：
 *   - 失活（进入详情/切 tab）时保存 shell 滚动位置（#main）；
 *   - 激活（返回列表）时：若期间有详情页 markListDirty(scope) 登记过数据
 *     变更则调用 refresh 刷新列表，否则不动数据（筛选/分类/状态零变化）；
 *   - 随后在 nextTick 恢复滚动位置。
 * 自管滚动（scrollMode: 'self'）的列表 DOM 在 KeepAlive 内整棵保留，
 * 滚动天然不丢，#main 的保存/恢复对其无副作用（写回 0）。
 */
import { nextTick, onActivated, onDeactivated } from 'vue'
import {
  consumeListDirty,
  rememberListScroll,
  restoreListScroll,
} from './list-scene-store'

export function useListScene(scope: string, refresh?: () => Promise<void> | void): void {
  onDeactivated(() => {
    if (typeof document === 'undefined') return
    const el = document.getElementById('main')
    if (el) rememberListScroll(scope, el.scrollTop)
  })

  onActivated(() => {
    if (consumeListDirty(scope) && refresh) {
      void refresh()
    }
    const top = restoreListScroll(scope)
    if (top < 0) return
    void nextTick(() => {
      if (typeof document === 'undefined') return
      const el = document.getElementById('main')
      if (el) el.scrollTop = top
    })
  })
}

/**
 * KeepAlive include 白名单：与各列表视图 defineOptions({ name }) 一一对应。
 * 只缓存列表页；详情/编辑页不在名单内，每次进入都重新挂载拉最新数据。
 */
export const LIST_CACHE_NAMES = [
  'EmailInboxView',
  'InvoiceListView',
  'EmailSummaryView',
  'NoteListView',
  'MeetingListView',
  'ScheduledTaskListView',
  'ContactListView',
  'PkmTodayView',
  'VaultListView',
  'FinanceView',
  'InstanceListView',
  'SessionWorkspaceView',
  'TasksView',
  'AgentLibraryView',
]
