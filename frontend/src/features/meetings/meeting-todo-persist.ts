import { localDB } from '../../native/local-db'
import { scheduledTasksApi } from '../scheduled-tasks/api'
import type { ActionItem } from './meetings-store'
import { meetingAccDispatchInput } from './meeting-page-actions'
import { accHandoffInput, draftsFromActionItems, personShareText, type MeetingTodoDraft } from './meeting-todos'
import { ensureTodoReminder, resolveTodoDue } from './meeting-due-reminder'

export async function createMeetingTodos(
  meetingId: string,
  items: ActionItem[],
  noteId: string | null = null,
  meetingTitle = '',
): Promise<number> {
  const drafts = draftsFromActionItems(items)
  let count = 0
  let reminders = 0
  const now = Date.now()
  for (const draft of drafts) {
    const id = `todo-${now}-${Math.random().toString(36).slice(2, 6)}`
    // 中文期限（「明天下午三点」）此前在这里被 Date.parse 判成 NaN → null，
    // due_at 静默丢失。现在走 resolveTodoDue：ISO 优先、中文兜底。
    const dueAt = resolveTodoDue(draft.due, now)
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
  void reminders // 计数仅供日志；调用方目前只关心 count
  return count
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
