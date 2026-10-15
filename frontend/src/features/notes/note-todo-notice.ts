/**
 * note-todo-notice.ts — 语音笔记「总结收尾做了哪些事」提示的**纯文案层**。
 *
 * 存在理由（与 note-todo-plan.ts 同型，但这次是被自己的缺陷逼出来的）：
 * 2026-10-06 发现 `createNoteTodos` 明明算出了 `reminders`，调用方却只解构
 * `{ created, unresolved }`，于是**只要没到期**就显示「已加入 N 条待办与日程
 * 提醒」—— 提醒一条都没建成时也这么说。需求「把一些时间点自动加入计划日程」
 * 就断在这里，而且断得毫无声响。
 *
 * 第一版修法把三分情况写回 NoteListView.vue，判据只能是源码文本断言。
 * 但文本断言守不住**用户实际看到的那句话**：把 `parts.push(...)` 换成一句
 * 写在注释里的「提醒未建成时会提示」，门禁照样全绿 —— 本会话已被注释骗过
 * 三次（§13.5 found.Snippet、§17 注释引述「精翻完成」、§22 注释里的「相同
 * 格式」）。把文案收进纯函数后，判据落在返回值上：
 * 「reminderPlanned=2 / reminders=0 时，文案里不许出现肯定语」。
 *
 * §127 在同一个早退上又挖出一个洞：
 * 首行 `if (result.created <= 0) return ''` 把**记账**提示一并吃掉了 ——
 * 而服务端每次 summarize 都会无条件记账（server_assistant.go:704-747），
 * 「中午吃饭花了 38 块」这种纯记账笔记因此一条待办都不建、提示一个字都不说，
 * 用户账上凭空多一笔。⇒ 早退的判据必须从「没建待办」改成「**什么都没做**」。
 *
 * 无 value import ⇒ 裸 `node --test` 能直接 import（见 note-todo-persist.test.ts
 * 顶部关于「无扩展名 import 导致 import 不了」的记录）。
 */
import type { NoteTodoResult } from './note-todo-persist.ts'

/**
 * 自动记账的结果（§127）。字段与 `NoteSummarizeBookkeeping` 同名同义，
 * 但这里是**结构化子集**而非整个响应类型 —— 这个模块只关心「有没有动账、
 * 动的是哪一笔」，不关心那笔记录的 id（列表页不进记账页逐条跳转）。
 *
 * ⚠ 全部可选：调用方可以只传它已经有的字段，而**没传/空数组一律按「没入账」处理** ——
 *   保守方向是「不说」，宁可不说也不能谎报入账（谎报会让用户去记账页找一笔不存在的账）。
 */
export interface NoteBookkeepingNotice {
  /** 入账记录（服务端恒返回数组，可能为空）。 */
  transactions?: Array<{ type?: string; amount?: number; category?: string }>
  /** '' = 没入账；created = 本次新建；existing = 此前已记、未重复。 */
  bookkeeping?: string
  /** 本次识别与已入账记录不一致 ⇒ 金额/方向以记账页为准。 */
  bookkeepingMismatch?: boolean
}

/** 金额取两位小数；拿不到就退回「已自动入账」这句不带数额的话。 */
function amountText(amount: number | undefined): string | null {
  if (typeof amount !== 'number' || !Number.isFinite(amount)) return null
  return amount.toFixed(2)
}

/**
 * 记账那一段的文案。返回空串 = 这一段没什么可说的。
 *
 * ⚠ `existing` **不能说「已入账」**：那一刻服务端什么都没创建
 *   （server_assistant.go 的 `else if existing != nil` 分支只回读既有记录），
 *   说「已入账」等于谎报一次写入。同理 `bookkeepingMismatch` 必须带出
 *   「以记账页为准」——服务端的注释就是这么写的（金额/方向可能对不上）。
 */
function bookkeepingClause(bk: NoteBookkeepingNotice | undefined): string {
  if (!bk) return ''
  const txs = Array.isArray(bk.transactions) ? bk.transactions : []
  if (txs.length === 0) return ''
  const tx = txs[0]
  const amount = amountText(tx?.amount)
  const dir = tx?.type === 'income' ? '收入' : '支出'
  const cat = tx?.category ? `（${tx.category}）` : ''
  const lead = bk.bookkeeping === 'existing'
    ? `此前已记这笔${amount ? ` ${dir} ¥${amount}` : ''}${cat}，未重复入账`
    : `已自动入账${amount ? ` ${dir} ¥${amount}` : ''}${cat}`
  if (bk.bookkeepingMismatch) return `${lead}，但笔记内容与已入账记录不一致，请到记账页核对`
  return lead
}

/**
 * 生成总结收尾后的提示文案。
 *
 * ⚠ 绝不谎报，三条硬约束：
 *   1. 只要**尝试过**建提醒却一条都没建成，文案里必须出现失败措辞；
 *   2. 记账发生过就必须说 —— 哪怕一条待办都没建（那正是纯记账笔记）；
 *   3. `existing` 不许说成「已入账」。
 *
 * @returns 空串表示**什么都没发生**（没建待办、没入账）—— 调用方据此不显示提示。
 *   ⚠ 这个早退不能只看 `created`：那正是 §127 的缺陷
 *   （首行 `if (created <= 0) return ''` 把记账提示一并吃掉）。
 */
export function buildNoteTodoNotice(
  result: NoteTodoResult,
  bookkeeping?: NoteBookkeepingNotice,
): string {
  const bk = bookkeepingClause(bookkeeping)
  if (result.created <= 0) {
    // 只建成了提醒但没建成待办，那条待办根本不存在，仍然只说记账。
    return bk
  }
  // ⚠ 这里原本还包了一层 Math.max(0, ·)，2026-10-07 变异验证时删掉了：
  // failed 只在 `failed > 0` 的守卫里被读取，而那个守卫已经排除了负值 ——
  // Math.max 是**恒等冗余的死代码**。变异「去掉 Math.max」实测门禁全绿，
  // 原因不是判据失明，而是这段逻辑压根没被走到。
  // 计数写反时不出负数的保证来自下面那两处 `> 0` 判断，
  // 由测试「负控：计数写反时不得报出负数」钉住。
  const failed = result.reminderPlanned - result.reminders
  const parts = [`已加入 ${result.created} 条待办`]
  if (result.reminders > 0) {
    parts.push(`${result.reminders} 条日程提醒`)
    if (failed > 0) parts.push(`${failed} 条提醒未建成`)
  } else if (result.reminderPlanned > 0) {
    // 尝试过、全失败。这一支就是 2026-10-06 那个缺陷的现场。
    parts.push(`${result.reminderPlanned} 条日程提醒未能创建`)
  } else {
    // 一次都没尝试：行动项本来就没有可解析的期限。与「建失败」是两回事，必须分开说。
    parts.push('没有可解析的期限，未建提醒')
  }
  if (result.unresolved > 0) parts.push(`${result.unresolved} 条期限没听清`)
  if (bk) parts.push(bk)
  return parts.join('，')
}
