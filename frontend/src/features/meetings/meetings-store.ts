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

export async function updateMeetingRecording(
  id: string,
  patch: { audioPath?: string | null; durationMs?: number },
): Promise<void> {
  const sets: string[] = []
  const values: unknown[] = []
  if (patch.audioPath !== undefined) {
    sets.push('audio_path = ?')
    values.push(patch.audioPath)
  }
  if (patch.durationMs !== undefined) {
    sets.push('duration_ms = ?')
    values.push(patch.durationMs)
  }
  if (sets.length === 0) return
  values.push(id)
  await localDB.run(`UPDATE local_meetings SET ${sets.join(', ')} WHERE id = ?`, values)
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

// ---- 音频分片（E5-S2 崩溃安全落盘）----

export interface MeetingAudioPart {
  id: string
  meetingId: string
  seq: number
  mimeType: string
  dataBase64: string
  createdAt: number
}

/** 追加一段音频分片（seq 由调用方递增，决定恢复时的拼接顺序）。 */
export async function appendAudioPart(input: {
  meetingId: string; seq: number; mimeType: string; dataBase64: string
}): Promise<void> {
  const id = `part-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  await localDB.run(
    `INSERT INTO local_meeting_audio_parts (id, meeting_id, seq, mime_type, data_base64, created_at)
     VALUES (?,?,?,?,?,?)`,
    [id, input.meetingId, input.seq, input.mimeType, input.dataBase64, Date.now()],
  )
}

export async function listAudioParts(meetingId: string): Promise<MeetingAudioPart[]> {
  const rows = await localDB.query<any>(
    'SELECT * FROM local_meeting_audio_parts WHERE meeting_id = ? ORDER BY seq',
    [meetingId],
  )
  return rows.map((r) => ({
    id: r.id, meetingId: r.meeting_id, seq: r.seq,
    mimeType: r.mime_type, dataBase64: r.data_base64, createdAt: r.created_at,
  }))
}

/** 转写成功后清理音频分片（音频不再需要，回收空间）。 */
export async function deleteAudioParts(meetingId: string): Promise<void> {
  await localDB.run('DELETE FROM local_meeting_audio_parts WHERE meeting_id = ?', [meetingId])
}

/** 会议音频分片数（恢复判定用；避免在列表页拉全部分片数据）。 */
export async function countAudioParts(meetingId: string): Promise<number> {
  const row = await localDB.queryOne<any>(
    'SELECT COUNT(*) AS n FROM local_meeting_audio_parts WHERE meeting_id = ?',
    [meetingId],
  )
  return row?.n ?? 0
}

/** 删除会议及其分片（取消录音 / 丢弃恢复时调用）。 */
export async function discardMeeting(id: string): Promise<void> {
  await deleteAudioParts(id)
  await deleteMeeting(id)
}

/**
 * 未完成会议（录音中断、可恢复）：未删除、无转写、存在音频分片。
 * E5-S2 验收「异常退出不遗失已落盘片段」的恢复入口。
 */
export async function findUnfinishedMeetings(): Promise<LocalMeeting[]> {
  const rows = await localDB.query<any>(
    `SELECT m.* FROM local_meetings m
     WHERE m.deleted_at IS NULL
       AND (m.transcript IS NULL OR m.transcript = '')
       AND EXISTS (SELECT 1 FROM local_meeting_audio_parts p WHERE p.meeting_id = m.id)
     ORDER BY m.started_at DESC`,
  )
  return rows.map(rowToMeeting)
}

function rowToMeeting(r: any): LocalMeeting {
  return {
    id: r.id, title: r.title, audioPath: r.audio_path, durationMs: r.duration_ms,
    transcript: r.transcript, summary: r.summary, startedAt: r.started_at,
    createdAt: r.created_at, deletedAt: r.deleted_at,
  }
}
