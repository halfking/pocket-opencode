/**
 * meetings-store.ts — 🦞 龙虾钳子：会议/声纹本地存储
 */
import { localDB } from '../../native/local-db'

// 'refine-failed' = 精校**降级或被拦下**，显示的是原始转写（§17/§35）。
// 它不能并进 'completed'：那会让「正常完成」与「精校失败」共用一个桶。
export type MeetingStatus = 'recording' | 'completed' | 'processing' | 'refined' | 'refine-failed'

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
  sessionId: string | null
  status: MeetingStatus
  startedAt: number
  createdAt: number
  deletedAt: number | null
  archivedAt: number | null
  tags: string[]
  topic: string | null
  summarySkill: string
}

/**
 * §132：转写段的来源。
 *
 * 为什么必须有它：`dedupeSegments` 是一条**相邻重叠**算法（裁掉切片重叠的尾巴），
 * 它的输入里曾经没有任何字段能表达「这行是人手工输入的」。后果是用户听错人名、
 * 用「补一句」追加一句改正时，那句改正被当成同音频的重转写裁成碎片
 * （实测 `张伟下周三交对比表` → `伟`），而原错句一个字没动。
 *
 * ⚠ 刻意做成**必填**而不是可选：可选字段只会让下一个生产者忘了填，
 * 于是缺陷原样复发。仓里只有两个段生产者（ingest-speech 的 ASR 结果、
 * buildUtteranceSegment 的人工行），两处都必须显式声明自己是谁。
 */
export type SegmentOrigin = 'asr' | 'manual'

export interface MeetingSegment {
  id: string
  meetingId: string
  speakerLabel: string | null
  lang: string
  confidence: number
  startMs: number
  endMs: number
  text: string
  translation?: string | null
  /** §132：必填，见上方说明。 */
  origin: SegmentOrigin
}

/** 智能体检索到的一条参考资料（用户自己写过的一条笔记）。 */
export interface SummaryReference {
  title: string
  noteId: string
  why: string
}

export interface LiveSummary {
  summary: string
  keyPoints: string[]
  actionItems: ActionItem[]
  decisions: string[]
  openQuestions: string[]
  /** 智能体路径才有；一次性 chat 路径为 undefined。 */
  references?: SummaryReference[]
  /** §109：true = 这一轮由服务端智能体路径产出；缺失即降级。与 references 同源。 */
  agent?: boolean
  /** §220：true = 这条 summary 是**原始转写**，不是摘要。与 agent 是两个维度。 */
  summaryFallback?: boolean
  updatedAt: number
}

export interface ActionItem {
  text: string
  assignee?: string
  due?: string
}

export type RecommendType = 'note' | 'email' | 'meeting' | 'contact' | 'web' | 'knowledge'

export interface RecommendItem {
  type: RecommendType
  id: string
  title: string
  snippet: string
  score: number
  url?: string
}

export async function createMeeting(input: {
  title?: string
  location?: string
  participants?: string[]
  audioPath?: string
  durationMs?: number
  startedAt?: number
  sessionId?: string
  topic?: string
  tags?: string[]
  summarySkill?: string
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
    sessionId: input.sessionId ?? null,
    status: 'recording',
    startedAt: input.startedAt ?? now,
    createdAt: now,
    deletedAt: null,
    archivedAt: null,
    tags: input.tags ?? [],
    topic: input.topic ?? null,
    summarySkill: input.summarySkill ?? 'meeting-minutes',
  }
  await localDB.run(
    `INSERT INTO local_meetings
     (id, title, location, participants, audio_path, duration_ms, transcript, summary,
      live_summary, refined_transcript, recommendations, note_id, status, started_at, created_at, session_id,
      archived_at, tags, topic, summary_skill)
     VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
    [
      m.id, m.title, m.location, JSON.stringify(m.participants), m.audioPath,
      m.durationMs, null, null, null, null, null, null, m.status, m.startedAt, m.createdAt, m.sessionId,
      null, JSON.stringify(m.tags), m.topic, m.summarySkill,
    ],
  )
  return m
}

export async function listMeetings(
  limit = 30,
  opts?: { archived?: boolean; offset?: number },
): Promise<LocalMeeting[]> {
  const archived = opts?.archived === true
  const offset = opts?.offset ?? 0
  const rows = await localDB.query<any>(
    archived
      ? `SELECT * FROM local_meetings WHERE deleted_at IS NULL AND IFNULL(archived_at, 0) > 0 ORDER BY started_at DESC LIMIT ? OFFSET ?`
      : `SELECT * FROM local_meetings WHERE deleted_at IS NULL AND IFNULL(archived_at, 0) = 0 ORDER BY started_at DESC LIMIT ? OFFSET ?`,
    [limit, offset],
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

/**
 * §131：更正一段转写的正文（交互式校对）。
 *
 * ★ `origin` 在**同一条 UPDATE** 里翻成 'manual'，不是可选参数、不是第二次写：
 *   用户的更正若留在 'asr'，`dedupeSegments` 会把它当「同音频的重转写」
 *   裁掉尾巴（§132 实测 `张伟下周三交对比表` → `伟`），
 *   而这一列的翻转点只有这里 —— 漏了就是「改完看着对、刷新又碎」。
 *
 * ⚠ 不碰 `local_meetings.transcript`：那一列是 runtime 持有的**派生列**
 *   （三处写入全是 `renderTranscript(原始段)`，见 recordingRuntime.ts:478/566
 *   与 ingest-speech.ts:76），而本函数拿不到「原始段」这个输入 ——
 *   页面上只有去重后的 displaySegments，拿它去渲染会与 runtime 的口径分叉。
 *   真要同步派生列，正确入口是 runtime（见 `editSegmentInMemory`）。
 */
export async function updateSegmentText(segmentId: string, text: string): Promise<void> {
  await localDB.run(
    `UPDATE local_meeting_segments SET text = ?, origin = 'manual' WHERE id = ?`,
    [text, segmentId],
  )
}

export async function updateMeeting(
  id: string,
  patch: Partial<Pick<LocalMeeting,
    'title' | 'location' | 'participants' | 'audioPath' | 'durationMs' |
    'transcript' | 'summary' | 'liveSummary' | 'refinedTranscript' |
    'recommendations' | 'status' | 'noteId' | 'archivedAt' | 'tags' | 'topic' | 'summarySkill'
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
    archived_at: patch.archivedAt,
    tags: patch.tags !== undefined ? JSON.stringify(patch.tags) : undefined,
    topic: patch.topic,
    summary_skill: patch.summarySkill,
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

export async function archiveMeeting(id: string): Promise<void> {
  await updateMeeting(id, { archivedAt: Date.now() })
}

export async function unarchiveMeeting(id: string): Promise<void> {
  await updateMeeting(id, { archivedAt: 0 })
}

export async function saveSegment(seg: Omit<MeetingSegment, 'id'>): Promise<string> {
  const id = `seg-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  await localDB.run(
    `INSERT INTO local_meeting_segments
     (id, meeting_id, speaker_label, lang, confidence, start_ms, end_ms, text, translation, origin)
     VALUES (?,?,?,?,?,?,?,?,?,?)`,
    [id, seg.meetingId, seg.speakerLabel, seg.lang, seg.confidence,
      seg.startMs, seg.endMs, seg.text, seg.translation ?? null, seg.origin],
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
    sessionId: (r.session_id as string) ?? null,
    status: (r.status as MeetingStatus) ?? 'completed',
    startedAt: r.started_at as number,
    createdAt: r.created_at as number,
    deletedAt: (r.deleted_at as number) ?? null,
    archivedAt: (r.archived_at as number) ?? null,
    tags: parseJson(r.tags as string, []),
    topic: (r.topic as string) ?? null,
    summarySkill: (r.summary_skill as string) || 'meeting-minutes',
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
    translation: (r.translation as string) ?? null,
    // §132：存量为 NULL / 未来出现未知取值时一律按 'asr' 处理 ——
    // 那是**保守**方向（去重照常生效），反过来把未知当 'manual' 会让去重静默失效。
    origin: r.origin === 'manual' ? 'manual' : 'asr',
  }
}

function parseJson<T>(raw: string | null | undefined, fallback: T): T {
  if (!raw) return fallback
  try { return JSON.parse(raw) as T } catch { return fallback }
}
