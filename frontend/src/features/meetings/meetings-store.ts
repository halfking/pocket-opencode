/**
 * meetings-store.ts — 🦞 龙虾钳子：会议/声纹本地存储
 */
import { localDB } from '../../native/local-db'

export type MeetingStatus = 'recording' | 'completed' | 'processing' | 'refined'

export interface LocalMeeting {
  id: string
  title: string | null
  location: string | null
  participants: string[]
  audioPath: string | null
  durationMs: number
  transcript: string | null
  summary: string | null
  liveSummary: LiveSummary | null
  refinedTranscript: string | null
  recommendations: RecommendItem[]
  noteId: string | null
  status: MeetingStatus
  startedAt: number
  createdAt: number
  deletedAt: number | null
}

export interface MeetingSegment {
  id: string
  meetingId: string
  speakerLabel: string | null
  lang: string
  confidence: number
  startMs: number
  endMs: number
  text: string
}

export interface LiveSummary {
  summary: string
  keyPoints: string[]
  actionItems: ActionItem[]
  decisions: string[]
  openQuestions: string[]
  updatedAt: number
}

export interface ActionItem {
  text: string
  assignee?: string
  due?: string
}

export interface RecommendItem {
  type: 'note' | 'email' | 'meeting' | 'contact'
  id: string
  title: string
  snippet: string
  score: number
}

export async function createMeeting(input: {
  title?: string
  location?: string
  participants?: string[]
  audioPath?: string
  durationMs?: number
  startedAt?: number
}): Promise<LocalMeeting> {
  const now = Date.now()
  const m: LocalMeeting = {
    id: `meeting-${now}-${Math.random().toString(36).slice(2, 8)}`,
    title: input.title ?? null,
    location: input.location ?? null,
    participants: input.participants ?? [],
    audioPath: input.audioPath ?? null,
    durationMs: input.durationMs ?? 0,
    transcript: null,
    summary: null,
    liveSummary: null,
    refinedTranscript: null,
    recommendations: [],
    noteId: null,
    status: 'recording',
    startedAt: input.startedAt ?? now,
    createdAt: now,
    deletedAt: null,
  }
  await localDB.run(
    `INSERT INTO local_meetings
     (id, title, location, participants, audio_path, duration_ms, transcript, summary,
      live_summary, refined_transcript, recommendations, note_id, status, started_at, created_at)
     VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
    [
      m.id, m.title, m.location, JSON.stringify(m.participants), m.audioPath,
      m.durationMs, null, null, null, null, null, null, m.status, m.startedAt, m.createdAt,
    ],
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

export async function updateSegmentSpeaker(segmentId: string, speakerLabel: string): Promise<void> {
  await localDB.run(
    'UPDATE local_meeting_segments SET speaker_label = ? WHERE id = ?',
    [speakerLabel, segmentId],
  )
}

export async function updateMeeting(
  id: string,
  patch: Partial<Pick<LocalMeeting,
    'title' | 'location' | 'participants' | 'audioPath' | 'durationMs' |
    'transcript' | 'summary' | 'liveSummary' | 'refinedTranscript' |
    'recommendations' | 'status' | 'noteId'
  >>,
): Promise<void> {
  const sets: string[] = []
  const vals: unknown[] = []
  const map: Record<string, unknown> = {
    title: patch.title,
    location: patch.location,
    participants: patch.participants ? JSON.stringify(patch.participants) : undefined,
    audio_path: patch.audioPath,
    duration_ms: patch.durationMs,
    transcript: patch.transcript,
    summary: patch.summary,
    live_summary: patch.liveSummary ? JSON.stringify(patch.liveSummary) : undefined,
    refined_transcript: patch.refinedTranscript,
    recommendations: patch.recommendations ? JSON.stringify(patch.recommendations) : undefined,
    note_id: patch.noteId,
    status: patch.status,
  }
  for (const [col, val] of Object.entries(map)) {
    if (val !== undefined) { sets.push(`${col} = ?`); vals.push(val) }
  }
  if (sets.length === 0) return
  vals.push(id)
  await localDB.run(`UPDATE local_meetings SET ${sets.join(', ')} WHERE id = ?`, vals)
}

export async function updateTranscript(id: string, transcript: string): Promise<void> {
  await updateMeeting(id, { transcript })
}

export async function updateSummary(id: string, summary: string): Promise<void> {
  await updateMeeting(id, { summary })
}

export async function deleteMeeting(id: string): Promise<void> {
  await localDB.run('UPDATE local_meetings SET deleted_at = ? WHERE id = ?', [Date.now(), id])
}

export async function saveSegment(seg: Omit<MeetingSegment, 'id'>): Promise<string> {
  const id = `seg-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  await localDB.run(
    `INSERT INTO local_meeting_segments
     (id, meeting_id, speaker_label, lang, confidence, start_ms, end_ms, text)
     VALUES (?,?,?,?,?,?,?,?)`,
    [id, seg.meetingId, seg.speakerLabel, seg.lang, seg.confidence,
      seg.startMs, seg.endMs, seg.text],
  )
  return id
}

export async function getSegments(meetingId: string): Promise<MeetingSegment[]> {
  const rows = await localDB.query<any>(
    'SELECT * FROM local_meeting_segments WHERE meeting_id = ? ORDER BY start_ms',
    [meetingId],
  )
  return rows.map(rowToSegment)
}

export async function getMeetingWithSegments(
  meetingId: string,
): Promise<{ meeting: LocalMeeting; segments: MeetingSegment[] } | null> {
  const meeting = await getMeeting(meetingId)
  if (!meeting) return null
  const segments = await getSegments(meetingId)
  return { meeting, segments }
}

function rowToMeeting(r: Record<string, unknown>): LocalMeeting {
  return {
    id: r.id as string,
    title: r.title as string | null,
    location: (r.location as string) ?? null,
    participants: parseJson(r.participants as string, []),
    audioPath: r.audio_path as string | null,
    durationMs: (r.duration_ms as number) ?? 0,
    transcript: r.transcript as string | null,
    summary: r.summary as string | null,
    liveSummary: parseJson(r.live_summary as string, null),
    refinedTranscript: (r.refined_transcript as string) ?? null,
    recommendations: parseJson(r.recommendations as string, []),
    noteId: (r.note_id as string) ?? null,
    status: (r.status as MeetingStatus) ?? 'completed',
    startedAt: r.started_at as number,
    createdAt: r.created_at as number,
    deletedAt: (r.deleted_at as number) ?? null,
  }
}

function rowToSegment(r: Record<string, unknown>): MeetingSegment {
  return {
    id: r.id as string,
    meetingId: r.meeting_id as string,
    speakerLabel: r.speaker_label as string | null,
    lang: (r.lang as string) ?? 'zh',
    confidence: (r.confidence as number) ?? 1.0,
    startMs: r.start_ms as number,
    endMs: r.end_ms as number,
    text: r.text as string,
  }
}

function parseJson<T>(raw: string | null | undefined, fallback: T): T {
  if (!raw) return fallback
  try { return JSON.parse(raw) as T } catch { return fallback }
}
