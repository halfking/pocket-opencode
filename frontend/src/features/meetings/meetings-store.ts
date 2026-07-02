/**
 * meetings-store.ts — 🦞 龙虾钳子：会议/声纹本地存储
 *
 * 录音本地存，转写走片段，声纹向量本地。会议纪要发片段给 LLM 生成。
 * Phase 6A 完整实现；本文件提供数据层骨架。
 */
import { localDB } from '../../native/local-db'

export interface LocalMeeting {
  id: string
  title: string | null
  audioPath: string | null
  durationMs: number
  transcript: string | null
  summary: string | null
  startedAt: number
  createdAt: number
  deletedAt: number | null
}

export interface MeetingSegment {
  id: string
  meetingId: string
  speakerLabel: string | null
  startMs: number
  endMs: number
  text: string
}

export async function createMeeting(input: {
  title?: string; audioPath?: string; durationMs?: number; startedAt?: number
}): Promise<LocalMeeting> {
  const now = Date.now()
  const m: LocalMeeting = {
    id: `meeting-${now}-${Math.random().toString(36).slice(2, 8)}`,
    title: input.title ?? null,
    audioPath: input.audioPath ?? null,
    durationMs: input.durationMs ?? 0,
    transcript: null,
    summary: null,
    startedAt: input.startedAt ?? now,
    createdAt: now,
    deletedAt: null,
  }
  await localDB.run(
    `INSERT INTO local_meetings (id, title, audio_path, duration_ms, transcript, summary, started_at, created_at)
     VALUES (?,?,?,?,?,?,?,?)`,
    [m.id, m.title, m.audioPath, m.durationMs, null, null, m.startedAt, m.createdAt],
  )
  return m
}

export async function listMeetings(limit = 50): Promise<LocalMeeting[]> {
  const rows = await localDB.query<any>(
    `SELECT * FROM local_meetings WHERE deleted_at IS NULL ORDER BY started_at DESC LIMIT ?`,
    [limit],
  )
  return rows.map(rowToMeeting)
}

export async function getMeeting(id: string): Promise<LocalMeeting | null> {
  const row = await localDB.queryOne<any>(
    'SELECT * FROM local_meetings WHERE id = ? AND deleted_at IS NULL', [id],
  )
  return row ? rowToMeeting(row) : null
}

export async function updateTranscript(id: string, transcript: string): Promise<void> {
  await localDB.run('UPDATE local_meetings SET transcript = ? WHERE id = ?', [transcript, id])
}

export async function updateSummary(id: string, summary: string): Promise<void> {
  await localDB.run('UPDATE local_meetings SET summary = ? WHERE id = ?', [summary, id])
}

export async function deleteMeeting(id: string): Promise<void> {
  await localDB.run('UPDATE local_meetings SET deleted_at = ? WHERE id = ?', [Date.now(), id])
}

// ---- 分段（声纹聚类后）----

export async function saveSegment(seg: Omit<MeetingSegment, 'id'>): Promise<string> {
  const id = `seg-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  await localDB.run(
    `INSERT INTO local_meeting_segments (id, meeting_id, speaker_label, start_ms, end_ms, text)
     VALUES (?,?,?,?,?,?)`,
    [id, seg.meetingId, seg.speakerLabel, seg.startMs, seg.endMs, seg.text],
  )
  return id
}

export async function getSegments(meetingId: string): Promise<MeetingSegment[]> {
  const rows = await localDB.query<any>(
    'SELECT * FROM local_meeting_segments WHERE meeting_id = ? ORDER BY start_ms',
    [meetingId],
  )
  return rows.map((r) => ({
    id: r.id, meetingId: r.meeting_id, speakerLabel: r.speaker_label,
    startMs: r.start_ms, endMs: r.end_ms, text: r.text,
  }))
}

/**
 * 一次拉取会议 + 全部分段（MeetingRecordView 详情页依赖）。
 * meeting 不存在时返回 null；存在则 segments 可能是空数组。
 */
export async function getMeetingWithSegments(
  meetingId: string,
): Promise<{ meeting: LocalMeeting; segments: MeetingSegment[] } | null> {
  const meeting = await getMeeting(meetingId)
  if (!meeting) return null
  const segments = await getSegments(meetingId)
  return { meeting, segments }
}

function rowToMeeting(r: any): LocalMeeting {
  return {
    id: r.id, title: r.title, audioPath: r.audio_path, durationMs: r.duration_ms,
    transcript: r.transcript, summary: r.summary, startedAt: r.started_at,
    createdAt: r.created_at, deletedAt: r.deleted_at,
  }
}
