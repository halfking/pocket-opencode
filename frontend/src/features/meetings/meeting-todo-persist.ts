import { localDB } from '../../native/local-db'
import { scheduledTasksApi } from '../scheduled-tasks/api'
import type { ActionItem } from './meetings-store'
import { meetingAccDispatchInput } from './meeting-page-actions'
import { accHandoffInput, draftsFromActionItems, personShareText, type MeetingTodoDraft } from './meeting-todos'
import { ensureTodoReminder, resolveTodoDue } from './meeting-due-reminder'

/**
 * 写入会议行动项。
 *
 * ⚠ 2026-10-06 返回值从 `number` 改成 `{ count, reminders }`。
 * 此前 `void reminders // 计数仅供日志`：提醒创建失败（后端不可达/网络/权限）
 * 时调用方**无从得知**，于是照实弹「已生成当前总结」，
 * 而「下周三下午三点」那条提醒其实一条都没建成。
 */
/**
 * 这条行动项是否已经作为「本场会议的语音待办」落过库。
 *
 * ★§153：逐字对应 `meeting-ingest.ts` 的 `alreadyIngestedTodo`，
 *   与随手记侧的 `alreadyExists`（`note-todo-persist.ts`）同为三份同形实现。
 *   全仓一共三处 `INSERT INTO local_todos`（普查得来，不是手查链得来的），
 *   **三处都要改**，彼此的注释互为交叉引用。
 *
 * 用 `IS ?` 而不是 `= ?`：SQLite 的 `=` 对 NULL 恒不成立
 * ⇒ 用 `=` 会让「没有会议 id」的那批永远查不到重复。
 */
async function alreadyCreatedTodo(meetingId: string, title: string): Promise<boolean> {
  const hit = await localDB.queryOne(
    `SELECT id FROM local_todos
     WHERE meeting_id IS ? AND title = ? AND extracted_from_voice = 1`,
    [meetingId, title],
  )
  return hit !== null && hit !== undefined
}

export async function createMeetingTodos(
  meetingId: string,
  items: ActionItem[],
  noteId: string | null = null,
  meetingTitle = '',
/** 2026-10-07（§66.7）：unresolved 与随手记链同名同义，见下方累加处的说明。 */
): Promise<{ count: number; reminders: number; reminderPlanned: number; unresolved: number }> {
  const drafts = draftsFromActionItems(items)
  let count = 0
  let reminders = 0
  let reminderPlanned = 0
  // ⚠ 2026-10-07（§66.7）：这一格此前**不存在**，于是会议侧把
  //   「用户压根没提期限」与「用户提了但我们没听懂」混成同一句话。
  //   随手记链早就有它（note-todo-persist.ts:62），口径照搬：
  //   **due 非空但解析不出时刻** ⇒ 用户说了期限，是我们没听懂。
  //   §65 实测 glm-5.2 会把 due 填成「那之前」⇒ 解析失败 ⇒ 正好命中这一格。
  let unresolved = 0
  const now = Date.now()
  for (const draft of drafts) {
    // ★§153 幂等：这个按钮**可以重复点**（会议详情页的「总结」），
    //   而旧实现每次新 id + 裸 INSERT ⇒ 同一条行动项与同一个时间点会被建两遍。
    //   查重失败照常写入（查重是防重复，不是准入门槛）。
    try {
      if (await alreadyCreatedTodo(meetingId, draft.text)) continue
    } catch {
      // 同款处置，见上方注释与另两处同形实现。
    }
    const id = `todo-${now}-${Math.random().toString(36).slice(2, 6)}`
    // 中文期限（「明天下午三点」）此前在这里被 Date.parse 判成 NaN → null，
    // due_at 静默丢失。现在走 resolveTodoDue：ISO 优先、中文兜底。
    const dueAt = resolveTodoDue(draft.due, now)
    // 判据与随手记链**逐字同形**：`due` 有内容、却解不出时刻。
    // 顺序在 INSERT 之前、且**不看** INSERT 是否成功 —— 用户听不清期限
    // 与这条待办能不能写进库是两件事，混起来会互相掩盖。
    if (draft.due && draft.due.trim() !== '' && dueAt === null) unresolved++
    await localDB.run(
      `INSERT INTO local_todos
       (id, note_id, title, description, status, priority, due_at, extracted_from_voice, meeting_id, created_at, updated_at)
       VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
      [
        id, noteId, draft.text, draft.assignee ? `负责人：${draft.assignee}` : null,
        'pending', 'medium', dueAt ? dueAt.at : null, 1, meetingId, now, now,
      ],
    )
    count++
    // 需求「时间点自动加入计划日程」：解出真实时刻才建提醒，不建假日程。
    if (dueAt) {
      reminderPlanned++
      const ok = await ensureTodoReminder({
        text: draft.text,
        dueText: draft.due,
        assignee: draft.assignee,
        meetingTitle,
        at: dueAt.at,
        source: 'meeting-summary',
      })
      if (ok) reminders++
    }
  }
  return { count, reminders, reminderPlanned, unresolved }
}

export async function listMeetingTodos(meetingId: string): Promise<MeetingTodoDraft[]> {
  const rows = await localDB.query<{ title: string; description: string | null; due_at: number | null }>(
    `SELECT title, description, due_at FROM local_todos WHERE meeting_id = ? ORDER BY created_at DESC`,
    [meetingId],
  )
  return rows.map((r) => ({
    text: r.title,
    assignee: r.description?.replace(/^负责人：/, '') || undefined,
    due: r.due_at ? new Date(r.due_at).toISOString().slice(0, 10) : undefined,
  }))
}

export async function handoffTodoToAcc(draft: MeetingTodoDraft, meetingTitle: string) {
  return scheduledTasksApi.create(accHandoffInput(draft, meetingTitle))
}

export async function handoffMeetingToAcc(meeting: Parameters<typeof meetingAccDispatchInput>[0]) {
  return scheduledTasksApi.create(meetingAccDispatchInput(meeting))
}

export async function shareTodoWithPerson(draft: MeetingTodoDraft, meetingTitle: string): Promise<void> {
  const text = personShareText(draft, meetingTitle)
  const nav = typeof navigator !== 'undefined' ? navigator : null
  if (nav && 'share' in nav && typeof nav.share === 'function') {
    await nav.share({ title: draft.text, text })
    return
  }
  if (nav?.clipboard?.writeText) {
    await nav.clipboard.writeText(text)
  }
}
