/**
 * meeting-load-notice.ts — 会议详情页**读库失败**的判定与文案（纯函数，无副作用）。
 *
 * ── 为什么它是缺陷（§837）──
 *   `MeetingDetailView.load()` 此前是「线性、无保护」的：
 *
 *       async function load() {
 *         loading.value = true
 *         const data = await getMeetingWithSegments(meetingId.value)   // ← 抛错
 *         meeting.value = data?.meeting ?? null
 *         storedSegments.value = data?.segments ?? []
 *         loading.value = false                                        // ← 永远到不了
 *         …
 *       }
 *
 *   ⇒ 一次读取失败的后果**不是**「报错」，而是：
 *     ① `loading` 永久为 true ⇒ 模板第一层 `<div v-if="loading"><Skeleton/></div>`
 *        **永久骨架屏**，整页没有任何内容；
 *     ② 一个未处理的 rejection；
 *     ③ **零反馈** —— 没有错误行、没有重试入口，用户只看到「一直在加载」。
 *
 *   ⚠ 而这不是罕见分支：`local-db.ts` 明确会在两种常见情形抛
 *     `LocalDB 未初始化：连接不存在` / `LocalDB 未初始化，请先调用 init(dbSecret)`
 *     —— 锁屏后回来、应用启动早于 `init(dbSecret)`、切换工作区/账号导致密钥不可用。
 *   ⇒ 这是**有明确预期的状态**，不是「数据库坏了」。
 *
 * ── 同仓已有正确做法，本节沿用（不是新发明）──
 *   `MeetingListView` 对同一个失败的处理：`msg.includes('LocalDB 未初始化')`
 *   ⇒ `dbNotReady = true` ⇒ 渲染 `DbLockedState`（带「重新登录」动作）。
 *   ⇒ 也就是说：**同一个仓库里，列表页把它当「有出口的状态」，详情页把它当「永久加载中」。**
 *   本节把详情页对齐到列表页那套语义。
 *
 * ── ⚠ 刻意不做的那件事 ──
 *   「在 catch 里写 `loading.value = false`」是最容易想到的改法，而它是**错的**：
 *   那样 `meeting` 仍是 null ⇒ 模板第二层 `<EmptyState v-else-if="!meeting">`
 *   会显示**「会议不存在」** ⇒ 把一次**读取失败**谎报成**会议没了**。
 *   ⇒ 必须有自己的错误分支，且它的位置必须在 `!meeting` **之前**。
 */

/**
 * 与 `MeetingListView.dbNotReady` 同一判据（同一句错误文本）。
 *
 * ⚠ §840：实现已上移到 `src/utils/list-load-state.ts`（跨特性，两个列表页都要用；
 *   留在 features/meetings 下会让 utils 反向依赖 features）。这里只做再导出，
 *   **保持只有一份实现** —— 两份同义判据必然漂移，而漂移的后果是
 *   「列表能进、详情进不去」这类只在真机上才看得见的分叉。
 */
// ⚠ 用相对路径而不是 `@/utils/...`：本模块被 §837/§840 的判据直接以
//   `node --test --experimental-strip-types` 加载，而那条路径**不解析 `@` 别名**
//   （gates 里也是裸 node）。写成别名会让判据在第一步就 ERR_MODULE_NOT_FOUND。
import { isLocalDbUnavailable } from '../../utils/list-load-state.ts'

export { isLocalDbUnavailable }

export interface MeetingLoadFailureInput {
  error?: unknown
}

/**
 * 读库失败的文案。
 *
 * 两种形态必须分开：本地库不可用 ⇒ 要去**重新登录**；
 * 其它读取失败 ⇒ 重试可能有用。混成一句会让用户做错动作。
 */
export function describeMeetingLoadFailure(input: MeetingLoadFailureInput): string {
  if (isLocalDbUnavailable(input.error)) {
    return '本地加密库还没就绪（或已被锁），读不到这场会议。'
      + '请返回列表重新登录后再打开它 —— 会议数据没有丢，只是这次没读到。'
  }
  return '会议读取失败，暂时看不到这场会议的内容。请重试；'
    + '若反复失败，请检查本地存储空间后重新登录。'
}

/** D1 诊断前缀。 */
export const MEETING_LOAD_FAIL_TAG = '[meeting-detail] 读取会议失败：'
