import { localDB } from '../../native/local-db'
import type { LocalMeeting, MeetingSegment } from './meetings-store'

function parseJson<T>(raw: string | null | undefined, fallback: T): T {
  if (!raw) return fallback
  try { return JSON.parse(raw) as T } catch { return fallback }
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
    status: (r.status as LocalMeeting['status']) ?? 'completed',
    startedAt: r.started_at as number,
    createdAt: r.created_at as number,
    deletedAt: (r.deleted_at as number) ?? null,
    sessionId: (r.session_id as string) ?? null,
  }
}

/** 同一会话未完成的录音（杀进程后提示恢复，不自动重开采集）。 */
export async function getRecordingBySession(sessionId: string): Promise<LocalMeeting | null> {
  const row = await localDB.queryOne<Record<string, unknown>>(
    `SELECT * FROM local_meetings
     WHERE session_id = ? AND deleted_at IS NULL AND status = 'recording'
     ORDER BY started_at DESC LIMIT 1`,
    [sessionId],
  )
  return row ? rowToMeeting(row) : null
}

export async function updateSegmentTranslation(segmentId: string, translation: string): Promise<void> {
  await localDB.run(
    'UPDATE local_meeting_segments SET translation = ? WHERE id = ?',
    [translation, segmentId],
  )
}

export type { MeetingSegment }
