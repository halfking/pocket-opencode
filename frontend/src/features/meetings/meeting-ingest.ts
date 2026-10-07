/**
 * meeting-ingest.ts — 精翻后本地笔记 + 待办入库，并云同步元数据
 */
import { createNote } from '../notes/notes-store'
import { localDB } from '../../native/local-db'
import { meetingsApi } from '../../api/meetings'
import {
  updateMeeting, type ActionItem, type LocalMeeting,
} from './meetings-store'
import type { RefineResult } from '../../api/meetings'
import { ensureTodoReminder, resolveTodoDue } from './meeting-due-reminder'

export interface IngestResult {
  noteId: string | null
  todosCreated: number
  cloudSynced: boolean
}

/** 精翻结果写入本地笔记/待办，并尝试云同步 */
export async function ingestMeetingArtifacts(
  meeting: LocalMeeting,
  refine: RefineResult,
): Promise<IngestResult> {
  let noteId = refine.noteId ?? meeting.noteId
  let todosCreated = 0

  const content = refine.refinedTranscript || meeting.transcript || ''
  if (content && !noteId) {
    const note = await createNote({
      title: meeting.title ?? '会议纪要',
      content,
      domain: 'work',
      contentType: 'voice',
      tags: ['meeting'],
      audioPath: meeting.audioPath ?? undefined,
      audioDurationMs: meeting.durationMs,
    })
    noteId = note.id
  }

  const todos = [
    ...refine.todos,
    ...(meeting.liveSummary?.actionItems ?? []),
  ]
  todosCreated = await createLocalTodos(todos, noteId, meeting.id, meeting.title ?? '')

  await updateMeeting(meeting.id, {
    refinedTranscript: refine.refinedTranscript,
    summary: refine.refinedTranscript.slice(0, 500) || meeting.summary,
    noteId,
    status: 'refined',
  })

  let cloudSynced = false
  try {
    await meetingsApi.syncMeeting({
      id: meeting.id,
      title: meeting.title ?? undefined,
      location: meeting.location ?? undefined,
      participants: meeting.participants,
      startedAt: meeting.startedAt,
      durationMs: meeting.durationMs,
      summary: meeting.summary ?? undefined,
      refinedTranscript: refine.refinedTranscript,
      noteId: noteId ?? undefined,
      status: 'refined',
    })
    cloudSynced = true
  } catch {
    // 离线或未配置 PG，本地已入库
  }

  return { noteId, todosCreated, cloudSynced }
}

/**
 * 这条行动项是否已经作为「本场会议的语音待办」落过库。
 *
 * 逐字对应随手记侧的 `alreadyExists`（`features/notes/note-todo-persist.ts`）
 * —— 两侧同形，改一处必须改另一处。
 *
 * 用 `IS ?` 而不是 `= ?`：`meeting_id` 在随手记那条链上是 NULL，
 * 而 SQLite 的 `=` 对 NULL 恒不成立 ⇒ 用 `=` 会让「没有会议 id」的那批永远查不到重复。
 */
async function alreadyIngestedTodo(meetingId: string | null, title: string): Promise<boolean> {
  const hit = await localDB.queryOne(
    `SELECT id FROM local_todos
     WHERE meeting_id IS ? AND title = ? AND extracted_from_voice = 1`,
    [meetingId, title],
  )
  return hit !== null && hit !== undefined
}

async function createLocalTodos(
  items: ActionItem[],
  noteId: string | null,
  meetingId?: string,
  meetingTitle = '',
): Promise<number> {
  const unique = dedupeTodos(items)
  let count = 0
  const now = Date.now()
  for (const item of unique) {
    if (!item.text.trim()) continue
    // 幂等：这个收尾编排可以被重复触发（旧实现每次新 id + 裸 INSERT
    // ⇒ 同一条行动项与同一个时间点会被建两遍）。
    // 查重失败照常写入：查重是防重复，不是准入门槛（随手记侧同款处置）。
    try {
      if (await alreadyIngestedTodo(meetingId ?? null, item.text)) continue
    } catch {
      // 把它当门槛 ⇒ 一次 DB 抖动就让「时间点自动进日程」静默失效。
    }
    const id = `todo-${now}-${Math.random().toString(36).slice(2, 6)}`
    // 与会中总结路径同一套期限解析口径（ISO 优先、中文兜底）。
    const dueAt = resolveTodoDue(item.due, now)
    await localDB.run(
      `INSERT INTO local_todos
       (id, note_id, title, description, status, priority, due_at, extracted_from_voice, meeting_id, created_at, updated_at)
       VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
      [
        id, noteId, item.text, item.assignee ? `负责人：${item.assignee}` : null,
        'pending', mapPriority(item), dueAt ? dueAt.at : null, 1, meetingId ?? null, now, now,
      ],
    )
    count++
    // 录后精校同样要把时间点送进计划日程（需求原文：自动加入计划日程）。
    if (dueAt) {
      await ensureTodoReminder({
        text: item.text,
        dueText: item.due,
        assignee: item.assignee,
        meetingTitle,
        at: dueAt.at,
        source: 'meeting-ingest',
      })
    }
  }
  return count
}

function dedupeTodos(items: ActionItem[]): ActionItem[] {
  const seen = new Set<string>()
  return items.filter((i) => {
    const key = i.text.trim()
    if (!key || seen.has(key)) return false
    seen.add(key)
    return true
  })
}

function mapPriority(item: ActionItem): string {
  const p = (item as ActionItem & { priority?: string }).priority
  if (p === 'urgent' || p === 'high') return 'high'
  if (p === 'low') return 'low'
  return 'medium'
}

/** 录音结束后同步元数据到云端（不含音频/转写全文） */
export async function syncMeetingMetadata(meeting: LocalMeeting): Promise<boolean> {
  try {
    await meetingsApi.syncMeeting({
      id: meeting.id,
      title: meeting.title ?? undefined,
      location: meeting.location ?? undefined,
      participants: meeting.participants,
      startedAt: meeting.startedAt,
      durationMs: meeting.durationMs,
      summary: meeting.summary ?? undefined,
      status: meeting.status,
      noteId: meeting.noteId ?? undefined,
    })
    return true
  } catch {
    return false
  }
}
