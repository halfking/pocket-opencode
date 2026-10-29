/**
 * meetings-store.ts — 🦞 龙虾钳子：会议/声纹本地存储
 */
import { localDB } from '../../native/local-db'
import { captureRecordingDb, type RecordingDb } from './recording-operation'
import { utf8ByteLength } from '../../utils/text'
import { shouldPersistTranscript, meetingListSql } from './meeting-transcript-cache.ts'
import { captureMeetingOwnership, ownerColumnValues, ownerOf, ownerScopeSql } from '../../native/meeting-ownership'
import { captureModuleEpoch } from '../../native/portal-sync/epochs.ts'
import { meetingPayload, meetingPortalStore } from './meeting-sync-adapter.ts'

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
  updatedAt: number
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
  /**
   * ⚠⚠⚠ **这个数在不同来源之间不可比**（§913/D81 实测，全仓**零个消费者**）。
   *
   * 四个生产者，三种量纲：
   *   · 笔记（`searchRelatedNotes` → `searchHybrid` 的 RRF）⇒ **[0.0152, 0.0323]**
   *   · 会议（`searchRelatedMeetings`）⇒ `1/(i+1)`，而 `i` 来自
   *     `ORDER BY started_at DESC` ⇒ 它量的是**新旧**，不是相关度
   *   · 维基（`parseWikipediaOpenSearch`）⇒ `1/(i+1)` 位次；
   *     Wikipedia OpenSearch **不返回任何相关度**
   *   · kxmemory 服务端（`backend/internal/kxmemory/client.go` 的 `Score`）⇒ 同名字段
   *
   * ⇒ **笔记的最高分只有会议/维基最低分的 0.10×** ⇒ 任何
   *   「按 score 降序」的实现都会把**全部笔记沉到会议/维基之后**。
   * ⇒ 而唯一可能用到它的 `mergeRecommendations` **明确不排序**
   *   （它只做「拼接 → 去重 → 截断」），本仓没有任何地方读这个字段。
   * ⇒ **要按相关度排序，先统一口径再改排序**，不要顺手加一句
   *   `.sort((a, b) => b.score - a.score)`（那是简报 137 第 ① 问）。
   */
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
}, db: RecordingDb = captureRecordingDb()): Promise<LocalMeeting> {
  const now = Date.now()
  // ⚠ 归属取自**同一个**操作租约 ⇒ 这一行属于哪一代会话是原子的。
  //   没有已验证会话（恢复出来的会话）⇒ 归属未知 ⇒ 这一行保持未认领：
  //   任何账号都读不到，也永不上传（用户已确认的边界）。
  const owner = ownerOf(db)
  const epoch = captureModuleEpoch(owner, 'meetings')
  const [ownerUserId, ownerWorkspaceId] = ownerColumnValues(owner)
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
    updatedAt: now,
    deletedAt: null,
    archivedAt: null,
    tags: input.tags ?? [],
    topic: input.topic ?? null,
    summarySkill: input.summarySkill ?? 'meeting-minutes',
  }
  const statement = `INSERT INTO local_meetings
     (id, title, location, participants, audio_path, duration_ms, transcript, summary,
      live_summary, refined_transcript, recommendations, note_id, status, started_at, created_at, session_id,
      archived_at, tags, topic, summary_skill, owner_user_id, owner_workspace_id, updated_at)
     VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
  const values = [
      m.id, m.title, m.location, JSON.stringify(m.participants), m.audioPath,
      m.durationMs, null, null, null, null, null, null, m.status, m.startedAt, m.createdAt, m.sessionId,
      null, JSON.stringify(m.tags), m.topic, m.summarySkill, ownerUserId, ownerWorkspaceId, now,
    ]
  await meetingPortalStore(db).commit([{statement, values}], {
    id:m.id,extId:`meeting:${m.id}`,title:m.title??'',payload:meetingPayload(m),modifiedAt:now,epoch,
  }, true)
  return m
}

export async function listMeetings(
  limit = 30,
  opts?: { archived?: boolean; offset?: number },
): Promise<LocalMeeting[]> {
  const archived = opts?.archived === true
  const offset = opts?.offset ?? 0
  const { db, owner } = captureMeetingOwnership(localDB)
  const scope = ownerScopeSql(owner)
  // ⚠ §925：此前是 `SELECT *` ⇒ 每次列表都把每场会的**整场转写 + 整场精校稿**读出来
  //   （本地 SQLite 的一行本来就可能几百 KB ⇒ 30 场会议 = 1.45 MB，
  //     而这两列的唯一消费者只要 120 字的预览 ⇒ 界面要 ≈10.5 KB）。
  //   ⇒ 现在只取**有界片段**（`meetingListSql`），预览逐字不变。
  //   ⚠ 详情页走 `getMeeting`（`SELECT *`）⇒ 那里拿到的**仍是全文** ⇒ 不受影响。
  // ⚠ 参数顺序：作用域条件排在 WHERE 里、LIMIT/OFFSET 之前 ⇒ 归属参数在前。
  const rows = await db.query<any>(meetingListSql(archived, scope.clause), [...scope.params, limit, offset])
  return rows.map(rowToMeeting)
}

export async function getMeeting(id: string, db: RecordingDb = captureRecordingDb()): Promise<LocalMeeting | null> {
  const scope = ownerScopeSql(ownerOf(db))
  const row = await db.queryOne<any>(
    'SELECT * FROM local_meetings WHERE id = ? AND deleted_at IS NULL AND ' + scope.clause,
    [id, ...scope.params],
  )
  return row ? rowToMeeting(row) : null
}

export async function updateSegmentSpeaker(segmentId: string, speakerLabel: string, db: RecordingDb = captureRecordingDb()): Promise<void> {
  const scope = ownerScopeSql(ownerOf(db))
  await db.run(
    'UPDATE local_meeting_segments SET speaker_label = ? WHERE id = ? AND ' + scope.clause,
    [speakerLabel, segmentId, ...scope.params],
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
  const { db, owner } = captureMeetingOwnership(localDB)
  const scope = ownerScopeSql(owner)
  await db.run(
    `UPDATE local_meeting_segments SET text = ?, origin = 'manual' WHERE id = ? AND ` + scope.clause,
    [text, segmentId, ...scope.params],
  )
}

export async function updateMeeting(
  id: string,
  patch: Partial<Pick<LocalMeeting,
    'title' | 'location' | 'participants' | 'audioPath' | 'durationMs' |
    'transcript' | 'summary' | 'liveSummary' | 'refinedTranscript' |
    'recommendations' | 'status' | 'noteId' | 'archivedAt' | 'tags' | 'topic' | 'summarySkill'
  >>,
  db: RecordingDb = captureRecordingDb(),
): Promise<void> {
  const modifiedAt = Date.now()
  const epoch = captureModuleEpoch(ownerOf(db), 'meetings')
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
  const scope = ownerScopeSql(ownerOf(db))
  const portable = patch.title !== undefined || patch.participants !== undefined || patch.summary !== undefined
  if (portable) {
    const existing = await getMeeting(id, db)
    if (!existing) return
    const next = {...existing,...patch,updatedAt:modifiedAt}
    const guard = {
      clause:`EXISTS(SELECT 1 FROM local_meetings WHERE id=? AND ${scope.clause} AND updated_at=? AND title IS ? AND COALESCE(participants,'[]')=? AND summary IS ? AND deleted_at IS NULL)`,
      values:[id,...scope.params,modifiedAt,next.title,JSON.stringify(next.participants),next.summary],
    }
    await meetingPortalStore(db).commit([{statement:`UPDATE local_meetings SET ${sets.join(', ')},updated_at=? WHERE id=? AND ${scope.clause} AND updated_at=? AND deleted_at IS NULL`,values:[...vals,modifiedAt,id,...scope.params,existing.updatedAt]}],
      {id,title:next.title??'',payload:meetingPayload(next),modifiedAt,epoch,guard},false)
    if (!await db.queryOne(`SELECT 1 WHERE ${guard.clause}`,guard.values)) throw new Error('Meeting changed during this edit; local original retained, retry the edit')
    return
  }
  vals.push(id)
  await db.run(`UPDATE local_meetings SET ${sets.join(', ')} WHERE id = ? AND ${scope.clause}`, [...vals, ...scope.params])
}

/**
 * §925：每场会议**上一次落库**时转写的字节数。
 *
 * ⚠ 这是**节流状态**，不是事实源：事实源永远是 `local_meeting_segments`。
 * ⚠ 进程重启后会丢 ⇒ 丢了的代价是「重启后第一次多落一次库」（可接受）。
 * ⚠ 为什么不在 DB 里存：为一个缓存的节流状态建一列/一张表，
 *   代价大于收益（而且它重启后本来就可以重算）。
 */
const lastPersistedTranscriptBytes = new Map<string, number>()

/**
 * §925：把「到目前为止的整场转写」落进 `local_meetings.transcript`。
 *
 * ⚠⚠ 此前这里是**每新增一段就整段重写** ⇒ 一场会的写入量 **O(时长²)**：
 *   实测（真 SQLite，真实 ASR 语料，1952 段 = 8 小时）落库 **376.3 MB**、
 *   中位纯写 **17.3 秒**（5 轮交错；Mac 直连 SSD，手机上更贵）。
 *
 * ⚠ `throttled` 是**显式 opt-in**（见 `meeting-transcript-cache.ts` 的文件头）：
 *   默认 = 立刻落库（安全的一侧）；只有「逐段追加」那条路径声明节流。
 *   ⇒ 将来新增的写入点忘了声明 ⇒ 只是多写几次，**不会丢内容**。
 *   ⇒ 逐段追加那条路径声明它 ⇒ 8 小时会议 376.3 MB → **2.4 MB**、17.3 s → **1.44 s**。
 *
 * ⚠ 为什么这一列可以节流：它在本地**只有一个消费者** ——
 *   `NotesHubView` 的预览（`preview()` 只取 120 字），
 *   而**头部**始终是最新的 ⇒ 尾部滞后在预览里看不出来。
 *   ⇒ 详情页 / 精校链 / 同音纠正全部走分段，不走这一列 ⇒ 不受影响。
 * ⚠ **尾部最多落后 32 KB ≈ 41 分钟**（按真实语料折算），
 *   **但只在录音期间** —— `recordingRuntime.stop()` 收尾会**整篇补齐**一次
 *   ⇒ 会议结束后这一列是**完整**的。
 */
/**
 * §931：转写正文的来源 —— 字符串，**或**一个「字节数已知、正文惰性」的源。
 *
 * ⚠ 为什么要后者：节流判定需要「这一轮多少字节」，而**渲染整场正文是 O(n)**。
 *   逐段追加那条路径（`recordingRuntime.appendText` / `ingest-speech.ts`）
 *   每来一段都要走一次 ⇒ 若正文是**当场就算出来**的，那次 O(n) 的渲染在
 *   **绝大多数情况下会被节流直接丢弃** ⇒ 纯浪费（实测 8 小时 ~100 s 纯 CPU）。
 *   ⇒ 把「字节数」（O(1) 增量维护）与「正文」（惰性）分开给：
 *   **只有真的要落库时才渲染**。
 */
export type TranscriptSource =
  | string
  | {
      /** 正文的 UTF-8 **字节数**（必须与 `text()` 的字节数相等）。 */
      byteLength: number
      /** 惰性正文（O(总长) ⇒ 只在真要落库时调）。 */
      text: () => string
    }

/**
 * §925：把「到目前为止的整场转写」落进 `local_meetings.transcript`。
 *
 * ⚠⚠ 此前这里是**每新增一段就整段重写** ⇒ 一场会的写入量 **O(时长²)**：
 *   实测（真 SQLite，真实 ASR 语料，1952 段 = 8 小时）落库 **376.3 MB**、
 *   中位纯写 **17.3 秒**（5 轮交错；Mac 直连 SSD，手机上更贵）。
 *
 * ⚠ `throttled` 是**显式 opt-in**（见 `meeting-transcript-cache.ts` 的文件头）：
 *   默认 = 立刻落库（安全的那一侧）；只有「逐段追加」那条路径声明节流。
 *   ⇒ 将来新增的写入点忘了声明 ⇒ 只是多写几次，**不会丢内容**。
 *   ⇒ 逐段追加那条路径声明它 ⇒ 8 小时会议 376.3 MB → **2.4 MB**、17.3 s → **1.44 s**。
 *
 * ⚠ 为什么这一列可以节流：它在本地**只有一个消费者** ——
 *   `NotesHubView` 的预览（`preview()` 只取 120 字），
 *   而**头部**始终是最新的 ⇒ 尾部滞后在预览里看不出来。
 *   ⇒ 详情页 / 精校链 / 同音纠正全部走分段，不走这一列 ⇒ 不受影响。
 * ⚠ **尾部最多落后 32 KB ≈ 41 分钟**（按真实语料折算），
 *   **但只在录音期间** —— `recordingRuntime.stop()` 收尾会**整篇补齐**一次
 *   ⇒ 会议结束后这一列是**完整**的。
 *
 * ⚠⚠ §931：`source` 传惰性源时，**正文只在真要落库时才渲染** ——
 *   这条路径以前每次调用都要 O(n) 渲染一次，而结果绝大多数被节流丢弃。
 */
export async function updateTranscript(
  id: string,
  source: TranscriptSource,
  opts?: { throttled?: boolean; db?: RecordingDb },
): Promise<void> {
  const db = opts?.db ?? captureRecordingDb()
  db.assertCurrent()
  const bytes = typeof source === 'string' ? utf8ByteLength(source) : source.byteLength
  const last = lastPersistedTranscriptBytes.get(id) ?? 0
  if (!shouldPersistTranscript({ lastPersistedBytes: last, nextBytes: bytes, throttled: opts?.throttled })) {
    return
  }
  lastPersistedTranscriptBytes.set(id, bytes)
  // ⚠ 惰性源：**到这里才渲染** ⇒ 节流说不写时，那次 O(n) 渲染根本不会发生。
  const transcript = typeof source === 'string' ? source : source.text()
  await updateMeeting(id, { transcript }, db)
}

export async function updateSummary(id: string, summary: string): Promise<void> {
  await updateMeeting(id, { summary })
}

export async function deleteMeeting(id: string, db: RecordingDb = captureRecordingDb()): Promise<void> {
  const now = Date.now()
  const owner = ownerOf(db)
  const epoch = captureModuleEpoch(owner, 'meetings')
  const scope = ownerScopeSql(owner)
  const existing = await getMeeting(id,db)
  if(!existing)return
  const guard={clause:`EXISTS(SELECT 1 FROM local_meetings WHERE id=? AND ${scope.clause} AND updated_at=? AND deleted_at=?)`,values:[id,...scope.params,now,now]}
  await meetingPortalStore(db).commit([{statement:`UPDATE local_meetings SET deleted_at=?,updated_at=? WHERE id=? AND ${scope.clause} AND updated_at=? AND deleted_at IS NULL`,values:[now,now,id,...scope.params,existing.updatedAt]}],
    {id,title:existing.title??'',payload:{},modifiedAt:now,epoch,deleted:true,guard},false)
  if(!await db.queryOne(`SELECT 1 WHERE ${guard.clause}`,guard.values))throw new Error('Meeting changed during deletion; local original retained, retry deletion')
}

export async function archiveMeeting(id: string, db: RecordingDb = captureRecordingDb()): Promise<void> {
  await updateMeeting(id, { archivedAt: Date.now() }, db)
}

export async function unarchiveMeeting(id: string, db: RecordingDb = captureRecordingDb()): Promise<void> {
  await updateMeeting(id, { archivedAt: 0 }, db)
}

export async function saveSegment(seg: Omit<MeetingSegment, 'id'>, db: RecordingDb = captureRecordingDb()): Promise<string> {
  const id = `seg-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`
  const [ownerUserId, ownerWorkspaceId] = ownerColumnValues(ownerOf(db))
  await db.run(
    `INSERT INTO local_meeting_segments
     (id, meeting_id, speaker_label, lang, confidence, start_ms, end_ms, text, translation, origin, owner_user_id, owner_workspace_id)
     VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
    [id, seg.meetingId, seg.speakerLabel, seg.lang, seg.confidence,
      seg.startMs, seg.endMs, seg.text, seg.translation ?? null, seg.origin, ownerUserId, ownerWorkspaceId],
  )
  return id
}

export async function getSegments(meetingId: string, db: RecordingDb = captureRecordingDb()): Promise<MeetingSegment[]> {
  const scope = ownerScopeSql(ownerOf(db))
  const rows = await db.query<any>(
    'SELECT * FROM local_meeting_segments WHERE meeting_id = ? AND ' + scope.clause + ' ORDER BY start_ms',
    [meetingId, ...scope.params],
  )
  return rows.map(rowToSegment)
}

export async function getMeetingWithSegments(
  meetingId: string,
  db: RecordingDb = captureRecordingDb(),
): Promise<{ meeting: LocalMeeting; segments: MeetingSegment[] } | null> {
  const meeting = await getMeeting(meetingId, db)
  if (!meeting) return null
  const segments = await getSegments(meetingId, db)
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
    updatedAt: (r.updated_at as number) ?? 0,
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
