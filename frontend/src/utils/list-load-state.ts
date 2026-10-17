/**
 * list-load-state.ts — 列表页「**一行都没显出来**的时候该显示什么」的判定与文案（纯函数）。
 *
 * ── 为什么它是缺陷（§840）──
 *   会议列表 / 笔记列表都用 `useContinuousList` 喂数据。两处视图的模板分支是：
 *
 *       <div v-if="loading"><Skeleton/></div>
 *       <EmptyState v-else-if="meetings.length === 0" title="还没有会议记录"
 *                   hint="点击右下角麦克风开始第一次会议" />
 *       <div v-else> …列表… <div v-if="meetings.length > 0"> …重试按钮… </div> </div>
 *
 *   而 `loading` 是 `status==='idle' && 行数===0`。首屏读取失败时 `status` 变成
 *   `'failed'` ⇒ `loading` 为 false、行数 0 ⇒ **正好落进那个真空态**：
 *
 *       · 「还没有会议记录」+「点击右下角麦克风开始第一次会议」
 *       · 而那颗「加载失败，点击重试」按钮在 `行数 > 0` 的分支里 ⇒ 永不渲染
 *
 *   ⇒ 用户已有 10 场会议，一次首屏读失败后看到的是「一场都没有，请开始第一次」，
 *     **并被引导去新建一场**。原记录完好，却看不见、也无处重试。
 *
 * ── D1 之深：区分「本地库未就绪」的那一层其实不可达 ──
 *   `local-db.ts` 会在锁屏后回来 / 启动早于 `init(dbSecret)` / 切换工作区时抛
 *   `LocalDB 未初始化：…`，两处视图都为此写了 `dbNotReady → DbLockedState`（带「重新登录」）。
 *   **但那个判断写在 `load()` 的 try/catch 里，而 `resetQuery()` 返回 `void` 并不抛**
 *   （内部是 `void loadPage(1,'refresh')`），promise 链上自带 `.catch(() => setStatus('failed'))`
 *   把错误吃掉了 ⇒ **`e.message` 永远到不了视图层** ⇒ `dbNotReady` 恒为 false
 *   ⇒ 「本地库未就绪，请重新登录」与「磁盘忙，重试」在界面上**完全同形**。
 *   取证读数见 `.artifacts/s840-probe/probe.mjs`（两种失败 status/行数/dbNotReady 三项全同）。
 *
 *   ⇒ 结论：**错误只能在视图自己的 `fetchPage` 内部拦截**，那里才看得见 `e.message`。
 *
 * ── 同仓已有正确做法，本节沿用（不是新发明）──
 *   `EmailInboxView.load()` 直接 `await showLocal(true)` ⇒ 它的 catch 可达，
 *   `dbNotReady` 与 `loadError` 都真的生效。**三个列表页里，唯一走控制器的两处是坏的。**
 *
 * ── 刻意不做的那件事 ──
 *   不把 `status==='failed'` 一律当成错误态：有行时读失败必须**保留行**并降级为行内重试
 *   （`list-error-failpath` 那道门就是管这件事的）。本模块只在**无行**时接管整页。
 */

import type { ListStatus } from '../lib/shell/continuousList'

/**
 * 「本地库未就绪」判据。
 *
 * ⚠ SSOT 放在这里而不是 §837 的 `features/meetings/meeting-load-notice.ts`：
 *   本节的消费方跨特性（会议 + 笔记），而那个模块在 features 下；`src/utils` 才是
 *   两个特性都能引的位置。`meeting-load-notice.ts` 已改为从这里再导出，**只有一份实现**。
 */
export function isLocalDbUnavailable(error: unknown): boolean {
  const msg = error instanceof Error ? error.message : String(error ?? '')
  return msg.includes('LocalDB 未初始化')
}

/**
 * 无行 + 读取失败 ⇒ 该显示失败态（而不是真空态）。
 *
 * `rowCount` 必须参与判定：控制器在失败时**保留已加载行**，
 * 有行时失败是「行内重试」，不是整页错误态。
 *
 * ⚠ 命名刻意含 `Error` 一词：仓库那道门禁（`list-error-failpath`）靠
 *   `/error/i` 认出「错误分支」再检查它的行数守卫 ⇒ 叫 `readFailed` 的话
 *   这条分支会被判据整体跳过（`continue` 在 /error/i 那一步），
 *   守卫被拿掉也没人喊。判据的识别词是契约的一部分。
 */
export function isListReadError(status: ListStatus, rowCount: number): boolean {
  return status === 'failed' && rowCount === 0
}

/**
 * 该显示骨架屏吗。
 *
 * ⚠ 为什么要把 `refreshing` / `loadingNext` 也算进去：原来只有 `idle`，
 *   而首屏一 `load()` 就同步翻成 `refreshing` ⇒ `loading` 变 false、行数仍 0
 *   ⇒ **骨架屏在请求发出前就塌成「还没有会议记录」**，闪一下再变回列表。
 *   「正在读」和「读完了但是空的」必须分开，否则用户在等结果时看到的是结论。
 */
export function isListSkeleton(status: ListStatus, rowCount: number, dbNotReady: boolean): boolean {
  if (dbNotReady) return false
  if (rowCount > 0) return false
  return status === 'idle' || status === 'refreshing' || status === 'loadingNext'
}

export interface ListReadFailureNotice {
  title: string
  hint: string
  action: string
}

/**
 * 读取失败的文案。
 *
 * 两条硬规则（来自 §838 的教训）：
 *  ① **不得断言数据状态** —— 读失败时我们并不知道记录还在不在，
 *     所以不能说「记录没有丢」；也不能说「没有记录」——那正是被修掉的那个谎报。
 *  ② 必须给可执行动作，且**不把技术串漏给用户**。
 *
 * `subject` 是页面自己的名词（'会议记录' / '笔记' / '邮件'），不是技术串。
 *
 * ⚠ `hasRows` 必须分两句（§841）：**有行时**用户看到的列表还在屏幕上，
 *   真正要说的是「这些可能是旧的」；**无行时**用户什么也没看到，
 *   要说的是「没读出来、可以重试」。混成一句会让有行场景的用户以为数据是全的。
 */
export function listReadFailureNotice(
  subject: string,
  opts: { hasRows?: boolean } = {},
): ListReadFailureNotice {
  if (opts.hasRows === true) {
    return {
      title: `${subject}没有刷新出来`,
      hint: `没能读到最新的${subject}，下面这些是上次读到的，可能不是最新的。点重试再试一次。`,
      action: '重试',
    }
  }
  return {
    title: `${subject}读取失败`,
    hint: `没能加载出${subject}，可能是本地数据暂时不可用。点下面重试；若一直失败，请重新登录。`,
    action: '重试',
  }
}

/** 诊断前缀。两处视图的 `fetchPage` 失败时用它落日志。 */
export const LIST_READ_FAIL_TAG = '[list] 读取失败：'
