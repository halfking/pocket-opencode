/**
 * note-todo-persist.ts — 语音笔记的行动项落库 + 期限入日程（唯一带 I/O 的部分）。
 *
 * 需求「录音时即时总结，并把一些时间点自动加入计划日程」此前在**随手记侧
 * 完全没有落点**：录音停止后只调 /api/notes/{id}/summarize 拿一个 summary
 * 字符串，笔记里说过的「明天下午三点」这类期限没有任何去处 ——
 * 不进待办、不进日程、也不在界面上出现。
 *
 * 为什么不新造一套期限解析：会议侧已经有 resolveTodoDue（ISO 优先、中文
 * 兜底）与 ensureTodoReminder。两侧对「下午3点 = 15:00」「明天无钟点 =
 * 09:00」的口径必须一致，否则同一个人在会议里说「明天下午三点」和在笔记里
 * 说同一句会得到两个不同的提醒时刻。解析与「该不该建提醒」的判定都在
 * note-todo-plan.ts（纯函数，可直接断言），本文件只负责执行。
 *
 * 与会议侧的差别：note_id 有值、meeting_id 为 null（这条待办来自一条笔记
 * 而不是一场会），source 用 'note-voice' 以便在 payload 里区分来源。
 */
import { localDB } from '../../native/local-db'
import type { NoteActionItem } from '../../api/notes'
import { ensureTodoReminder } from '../meetings/meeting-due-reminder'
import { planNoteTodos } from './note-todo-plan.ts'

export interface NoteTodoResult {
  /** 成功写入 local_todos 的条数。 */
  created: number
  /** 成功建提醒的条数（<= created）。 */
  reminders: number
  /** 期限解不出来的条数 —— 记下来是为了让调用方能如实告诉用户。 */
  unresolved: number
}

/**
 * 把一条语音笔记的行动项写进 local_todos，并为能解出时刻的条目建提醒。
 *
 * **永不抛异常**：这是录音停止后的收尾链路，笔记正文已经落库了，
 * 待办与提醒都是附加价值。localDB 失败不应该让整条草稿流程看起来失败
 * （presentVoiceDraft 的 catch 会把整个总结标成失败，用户看到的却是
 * 「总结失败」——而总结其实已经拿到了）。
 */
export async function createNoteTodos(
  noteId: string,
  items: NoteActionItem[] | null | undefined,
  noteTitle = '',
): Promise<NoteTodoResult> {
  const result: NoteTodoResult = { created: 0, reminders: 0, unresolved: 0 }
  const now = Date.now()
  const plans = planNoteTodos({
    items,
    now,
    makeId: (i) => `todo-${now}-${i}-${Math.random().toString(36).slice(2, 6)}`,
  })
  for (const plan of plans) {
    if (plan.dueText && plan.dueAt === null) result.unresolved++
    try {
      await localDB.run(
        `INSERT INTO local_todos
         (id, note_id, title, description, status, priority, due_at, extracted_from_voice, meeting_id, created_at, updated_at)
         VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
        [
          plan.id, noteId, plan.text, plan.assignee ? `负责人：${plan.assignee}` : null,
          'pending', 'medium', plan.dueAt, 1, null, now, now,
        ],
      )
      result.created++
    } catch {
      // 单条入库失败不中断后面几条：模型给 5 条行动项，第 1 条写不进去
      // 不该让 2~5 条也一起消失。
      continue
    }
    // 解出真实时刻才建提醒，不建假日程（同会议侧的处置）。
    if (plan.remind && plan.dueAt !== null) {
      const ok = await ensureTodoReminder({
        text: plan.text,
        dueText: plan.dueText,
        assignee: plan.assignee,
        meetingTitle: noteTitle,
        at: plan.dueAt,
        source: 'note-voice',
      })
      if (ok) result.reminders++
    }
  }
  return result
}
