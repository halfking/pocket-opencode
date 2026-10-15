/**
 * meeting-ingest-notice.ts — 录后精校入库结果的用户提示（纯文案层）。
 *
 * ── 为什么要把它抽出来 ──
 *
 * 2026-10-06~07 之前这段文案是**内联**在 `meeting-recording-finalize.ts` 里的：
 *
 *     `${outcome.todosCreated} 项行动已加入待办与提醒`
 *
 * ⚠ 那句话正是随手记侧门禁 `reminder-outcome-honesty.test.ts`
 *   的**负控样本**（「不能用『已加入 N 条待办与日程提醒』这种把两者捆在一起的说法」）。
 *   随手记侧当天就改成「N 条日程提醒」+「M 条提醒未建成」分句计数，
 *   **会议侧没跟上**，所以「0 条提醒」时仍在暗示「时间点进了日程」。
 *
 * 文案必须住在**纯模块**里，判据才能直接断言**用户实际看到的那句字符串**——
 * 源码扫描分不清代码与注释里引述的同一句话（本仓已被骗过三次）。
 * 与 `meeting-reminder-note.ts`、`note-todo-notice.ts` 同一形状。
 *
 * ── 口径（与随手记链逐条对齐）──
 *
 * 1. **建了待办 ≠ 进了日程**。两者分句计数，不许捆在一起。
 * 2. **听不清 ≠ 没提**。`dueUnresolved` 单列（§66.7）。
 * 3. **建提醒失败 ≠ 没期限**。两者分句。
 * 4. 下次会议日程是**另一件事**，单独说（§56）。
 *
 * ⚠§229.1 补的第五、六条口径 —— **日历那一侧此前完全没分开**：
 *  5. **没有下次会议 ≠ 我们没听懂**。`eventUnresolved` 单列。
 *  6. **没建成 ≠ 没有**。`eventFailed` 单列。
 *     此前 `eventsCreated` 一个 0 同时表示「没定下次 / 没听懂 / 已去重 /
 *     建失败」四种结局，而这里只有 `eventsCreated > 0` 一句
 *     ⇒ 后两种都被报成「什么也没发生」。这与第 2 条对 `dueUnresolved`
 *     的处置是同一个洞：§66.7 只补了待办那一侧。
 *  7. 已去重（`duplicate`）**不上报**：日程确实在日历里，重复精校只是
 *     没再建一遍，不需要用户做任何动作。
 *     ★ created / unresolved / failed 三态互斥 ⇒ 下面三句最多命中一句。
 */

export interface IngestNoticeInput {
  /** 写入 local_todos 的条数。 */
  todosCreated: number
  /** 尝试建提醒的条数（due 解析成功的）。 */
  reminderPlanned: number
  /** 真正建成的提醒条数。 */
  reminders: number
  /** due 非空但解析不出时刻的条数 —— 用户说了期限，我们没听懂（§66.7）。 */
  dueUnresolved: number
  /** 由 next_meeting 建出来的日程条数（0/1，§56）。 */
  eventsCreated: number
  /** next_meeting 非空但解析不出时刻（0/1，§229.1）—— 用户说了下次会议时间点，我们没听懂。 */
  eventUnresolved: number
  /** 日程建失败：feed/create 抛异常（离线 / 日历未配置 / 被拒，§229.1）。 */
  eventFailed: boolean
  /** 入库整体失败。 */
  ingestFailed: boolean
}

/**
 * 拼出提示的后缀（不含外层括号与 kind 判定）。
 *
 * @returns 空串表示无需补充说明
 */
export function buildIngestNotice(input: IngestNoticeInput): string {
  const {
    todosCreated, reminderPlanned, reminders, dueUnresolved,
    eventsCreated, eventUnresolved, eventFailed, ingestFailed,
  } = input
  if (ingestFailed) return '行动项未能入库'

  const parts: string[] = []
  if (todosCreated > 0) parts.push(`${todosCreated} 项行动已加入待办`)

  // 提醒单独成句：建了待办不代表提醒建成了。
  if (reminderPlanned > 0) {
    const built = reminders > 0 ? `${reminders} 条日程提醒已建` : ''
    const failed = reminderPlanned - reminders
    if (failed > 0) {
      parts.push(built ? `${built}，${failed} 条未建成` : `${failed} 条日程提醒未能创建`)
    } else if (built) {
      parts.push(built)
    }
  }

  // 「期限没听清」单列：它与「没提期限」是两件事（随手记链 unresolved 同口径）。
  if (dueUnresolved > 0) parts.push(`${dueUnresolved} 条期限没听清`)

  // 下次会议日程是**另一件事**，得单独说 —— 「加了待办」不等于「会议进日程了」。
  // ⚠§229.1：用 else if 而不是三个独立 if，把互斥性写进代码形状 ——
  //   三个独立 if 会让人以为这三件事能同时发生，从而不敢删任何一个。
  if (eventsCreated > 0) parts.push('下次会议已加入日程')
  else if (eventUnresolved > 0) parts.push('下次会议的时间没听清，未建日程')
  else if (eventFailed) parts.push('下次会议日程未能创建')

  return parts.length > 0 ? `（${parts.join('，')}）` : ''
}
