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
  todosCreated = await createLocalTodos(todos, noteId)

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

async function createLocalTodos(items: ActionItem[], noteId: string | null): Promise<number> {
  const unique = dedupeTodos(items)
  let count = 0
  const now = Date.now()
  for (const item of unique) {
    if (!item.text.trim()) continue
    const id = `todo-${now}-${Math.random().toString(36).slice(2, 6)}`
    await localDB.run(
      `INSERT INTO local_todos
       (id, note_id, title, description, status, priority, due_at, extracted_from_voice, created_at, updated_at)
       VALUES (?,?,?,?,?,?,?,?,?,?)`,
      [
        id, noteId, item.text, item.assignee ? `负责人：${item.assignee}` : null,
        'pending', mapPriority(item), parseDue(item.due), 1, now, now,
      ],
    )
    count++
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

function parseDue(due?: string): number | null {
  if (!due) return null
  const t = Date.parse(due)
  return isNaN(t) ? null : t
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
