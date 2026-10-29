/**
 * meeting-ingest.ts — 精翻后本地笔记 + 待办入库，并云同步元数据
 */
import { refineStatusFor } from '../sessions/refine-outcome-notice'
import { createNote } from '../notes/notes-store'
import { meetingsApi } from '../../api/meetings'
import { captureRecordingOperation, type RecordingOperation } from './recording-operation'
import { isAbortError } from '../../api/http'
import { calendarApi } from '../calendar/api'
import {
  updateMeeting, type ActionItem, type LocalMeeting,
} from './meetings-store'
import type { RefineResult } from '../../api/meetings'
import { ensureTodoReminder, resolveTodoDue } from './meeting-due-reminder'
import {
  buildNextMeetingEvent,
  classifyNextMeeting,
  dedupeWindowSeconds,
  isDuplicateNextMeetingEvent,
  type NextMeetingGate,
} from './meeting-next-event'
import { resolveIngestWriteback } from './refined-ingest-writeback'
// 存储与同步策略（storage-sync）：云端上行前的 shouldSync 判定与结果上报。
import { readCachedModuleMode, recordSyncReportEntry } from '../../stores/syncPolicyStore'

export interface IngestResult {
  noteId: string | null
  todosCreated: number
  /** 尝试建提醒的条数（due 解析成功才计）。 */
  reminderPlanned: number
  /** 真正建成的提醒条数。 */
  reminders: number
  /**
   * §66.7：due 非空但解析不出时刻的条数。
   *
   * ⚠ 这一格此前**不存在** ⇒ 用户明明说了「那之前」这种期限，
   *   产品侧解析不出来，却一个字都不说（提醒被 `if (dueAt)` 静默跳过）。
   *   随手记链早就有同名同义的 `unresolved`，口径照搬。
   */
  dueUnresolved: number
  /** §56：本次由 `next_meeting` 建出来的日程条数（0/1）。 */
  eventsCreated: number
  /**
   * §229.1：`next_meeting` 非空但解析不出时刻（0/1）——
   * 用户说了下次会议的时间点，**我们没听懂**。
   * 与 `dueUnresolved`（待办侧）同口径，那一格是 §66.7 补的，
   * 日历这一侧当时漏了，于是「下次会议进日程」整条静默断掉。
   */
  eventUnresolved: number
  /**
   * §229.1：日程**建失败**（feed/create 抛异常：离线 / 日历未配置 / 被拒）。
   * 此前它与「没有下次会议」「已去重」共用 `eventsCreated === 0`，
   * 一个数字装四种结局 ⇒ 用户永远看不到失败。
   */
  eventFailed: boolean
  cloudSynced: boolean
}

/** 精翻结果写入本地笔记/待办，并尝试云同步 */
export async function ingestMeetingArtifacts(
  meeting: LocalMeeting,
  refine: RefineResult,
  operation: RecordingOperation = captureRecordingOperation('meetings'),
): Promise<IngestResult> {
  const db = operation.cloudDb ?? operation.localDb
  const assertCurrent = operation.cloudLease ? operation.assertCloud : operation.assertLocal
  assertCurrent()
  let noteId = refine.noteId ?? meeting.noteId
  let todosCreated = 0
  let reminderPlanned = 0
  let reminders = 0
  let dueUnresolved = 0
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
      // 本地库按 workspace_id 分区。漏传会静默落进 notes-persist 的字面量
      // 默认值 'default'，而用户自己的笔记在 auth.workspaceId（真机实测
      // ws_user-admin）——两边不 intersect，会议自动生成的手记在笔记列表里
      // 永远看不见，且不报任何错。
      workspaceId: operation.workspaceId,
    }, { lease: operation.cloudLease ?? operation.localLease, backgroundCloud: false })
    assertCurrent()
    noteId = note.id
  }

  const todos = [
    ...refine.todos,
    ...(meeting.liveSummary?.actionItems ?? []),
  ]
  const created = await createLocalTodos(todos, noteId, meeting.id, meeting.title ?? '', operation)
  todosCreated = created.count
  reminderPlanned = created.reminderPlanned
  reminders = created.reminders
  dueUnresolved = created.dueUnresolved

  // §56：把「下次会议」建成**日程**。失败不影响上面已经落库的东西 ——
  //   纪要、待办、提醒都已经写下去了，为一条日程把整个入库判失败更糟。
  //
  // ⚠§229.1：try/catch 从这里**移进了** ensureNextMeetingEvent 内部。
  //   原来它挂在调用点，catch 之后 `eventsCreated` 保持 0 且**不留任何痕迹**
  //   —— 「离线导致没建成日程」与「这场会没定下次会议」在返回值上完全同形。
  const eventOutcome = await ensureNextMeetingEvent(meeting, refine.structuredMinutes?.nextMeeting ?? null, operation)
  const eventsCreated = eventOutcome.state === 'created' ? 1 : 0
  const eventUnresolved = eventOutcome.state === 'unresolved' ? 1 : 0
  const eventFailed = eventOutcome.state === 'failed'

  // ⚠ 2026-10-07：降级/被拦下时**不能**落 'refined'。
  //   否则列表徽章显示「已精翻」，点进去却是刚说过的原话。
  //
  // ⚠★ 2026-10-07（§108）：摘要**也不能**在降级时被原文顶掉。
  //   降级时 `refine.refinedTranscript` 装的是原文（normalizeRefine 的
  //   `String(serverRefined ?? fallback)`），于是原来的
  //   `refine.refinedTranscript.slice(0, 500) || meeting.summary`
  //   里那个 `||` **永远不生效** —— slice 一段非空原文必然非空。
  //   ⇒ 滚动摘要链写好的摘要被刚说过的 500 字原话覆盖，还会同步到云端。
  //   判定抽到 refined-ingest-writeback.ts（纯函数，可单测）。
  const status = refineStatusFor({
    fromFallback: refine.fromFallback,
    rejected: refine.rejected,
  })
  const writeback = resolveIngestWriteback({
    existingSummary: meeting.summary,
    refine,
    status,
  })
  await updateMeeting(meeting.id, {
    refinedTranscript: writeback.refinedTranscript,
    // ⚠ `undefined` 而不是空串：updateMeeting 只把存在的键写进 patch，
    //   传空串会把已有摘要**清成空**（与不传不是一回事）。
    ...(writeback.summary === null ? {} : { summary: writeback.summary }),
    noteId,
    status: writeback.status,
  }, db)

  let cloudSynced = false
  // ── 存储与同步策略挂点（storage-sync）────────────────────────────────
  // meetings=local ⇒ 转写/纪要只留本机，云端上行整段跳过并上报 skipped。
  // （前面 createNote/createLocalTodos 的**本地**写入不受影响；notes 模块
  // 若同为 local，笔记镜像那条路会各自再判一次。）
  if (readCachedModuleMode('meetings') === 'local') {
    recordSyncReportEntry({ module: 'meetings', status: 'skipped' })
  } else {
    try {
      operation.assertCloud()
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
        status: writeback.status,
      }, operation.assertCloud)
      cloudSynced = true
      recordSyncReportEntry({ module: 'meetings', status: 'synced', count: 1 })
    } catch (error) {
      if (isAbortError(error)) throw error
      // 离线或未配置 PG，本地已入库
    }
  }

  return {
    noteId, todosCreated, reminderPlanned, reminders, dueUnresolved,
    eventsCreated, eventUnresolved, eventFailed, cloudSynced,
  }
}

/** §229.1：日程那条链的五种**互斥**结局。 */
export type NextMeetingState = NextMeetingGate | 'duplicate' | 'created' | 'failed'

export interface NextMeetingOutcome {
  state: NextMeetingState
}

/**
 * 建立（或跳过建立）一条「下次会议」日程。
 *
 * 决策全在 `meeting-next-event.ts` 的纯函数里，这里只负责发请求 ——
 * 纯函数不 import I/O 才有门禁能直接 import 它（见该文件头注释）。
 *
 * ⚠§229.1：`none` / `unresolved` 由纯判定直接给出，**不发任何请求**；
 *   `ready` 之后才落成 `duplicate` / `created` / `failed` 三选一。
 *   `duplicate` **不上报为失败**：日程确实在日历里，重复精校只是没再建一遍，
 *   这与「没建成」是两件事（用户看到的是同一个已经存在的日程）。
 */
async function ensureNextMeetingEvent(
  meeting: Pick<LocalMeeting, 'title' | 'location'>,
  nextMeeting: string | null,
  operation: RecordingOperation,
): Promise<NextMeetingOutcome> {
  const gate = classifyNextMeeting(nextMeeting)
  if (gate !== 'ready') return { state: gate }
  const input = buildNextMeetingEvent(meeting, nextMeeting)
  // ⚠ `input` 在 gate==='ready' 时按构造必然非空；写 `!` 而不是判空兜底，
  //   因为一个「ready 却拿不到入参」分支只会变成没人测的假保险。
  if (!input) return { state: 'unresolved' }
  const { from, to } = dedupeWindowSeconds(input)
  // ⚠ §118：feed 的返回形状在本次改成了 {entries, partial}，这里必须显式取
  // .entries —— 直接把整个对象当数组传下去，isDuplicateNextMeetingEvent
  // 看到的 length 恒为 undefined ⇒ 「没找到重复」⇒ **每次都建重复日程**。
  //
  // ⚠ 去重本身不受部分降级影响：「下次会议」落的是 event 来源，
  //   而 event 是唯一失败就整体 503 的那一路（BuildFeed 里 ListRange 出错
  //   直接 return nil, err）⇒ partial 只可能来自 task / scheduled 两路。
  try {
    operation.assertCloud()
    const existing = (await calendarApi.feed(from, to, operation.assertCloud)).entries
    operation.assertCloud()
    if (isDuplicateNextMeetingEvent(existing, input)) return { state: 'duplicate' }
    await calendarApi.create(input, operation.assertCloud)
    return { state: 'created' }
  } catch (error) {
    if (isAbortError(error) && operation.cloudLease) throw error
    operation.assertLocal()
    // 离线 / 日历未配置 / 建了被拒 —— **仍然算失败并上报**（§229.1）。
    // 早于此处的处置是静默降级，用户永远不知道自己要的日程没建成。
    return { state: 'failed' }
  }
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
async function alreadyIngestedTodo(meetingId: string | null, title: string, operation: RecordingOperation): Promise<boolean> {
  const hit = await (operation.cloudDb ?? operation.localDb).queryOne(
    `SELECT id FROM local_todos
     WHERE meeting_id IS ? AND title = ? AND extracted_from_voice = 1`,
    [meetingId, title],
  )
  return hit !== null && hit !== undefined
}

async function createLocalTodos(
  items: ActionItem[],
  noteId: string | null,
  meetingId: string | undefined,
  meetingTitle: string,
  operation: RecordingOperation,
): Promise<{ count: number; reminderPlanned: number; reminders: number; dueUnresolved: number }> {
  const db = operation.cloudDb ?? operation.localDb
  const assertCurrent = operation.cloudLease ? operation.assertCloud : operation.assertLocal
  const unique = dedupeTodos(items)
  let count = 0
  let reminderPlanned = 0
  let reminders = 0
  let dueUnresolved = 0
  const now = Date.now()
  for (const item of unique) {
    if (!item.text.trim()) continue
    // ★§150：查重。逐字对应随手记侧的 alreadyExists（`note-todo-persist.ts`）——
    //   两侧是**对偶实现**（§66.7 的口径就是照抄它的），必须同形。
    //   ⚠ 我没有抽成共享模块：本仓没有跨 feature 的共享模块落点
    //   （`features/common/` 只有一个视图），而 notes 侧本来就是从 meetings
    //   借用 `meeting-due-reminder` 的 ⇒ 惯例是各链自带、互为对照。
    //   ⇒ 改这条语义时**两处都要改**，交叉引用写在各自注释里。
    //
    //   为什么这里需要：收尾编排 `finalizeRecording` **没有任何幂等守卫**
    //   （`meeting-recording-finalize.ts:125` 只有 `if (!meetingId) return`），
    //   而「录一段 → 停 → 再录一段」是正常用法 ⇒ 同一场会的行动项会**再插一遍**，
    //   提醒也会**再建一遍**。`dedupeTodos` 只去掉**同一批内**的重复，救不了跨次。
    //
    //   ⚠ 同文件里 `next_meeting` 那条日历**有**去重（`isDuplicateNextMeetingEvent`），
    //   而待办与提醒没有 —— 同一个函数里两种口径，这个不对称本身就是缺陷。
    try {
      if (await alreadyIngestedTodo(meetingId ?? null, item.text, operation)) continue
    } catch (error) {
      if (isAbortError(error)) throw error
      // 查重失败**不挡写入**：查重是防重复，不是准入门槛（随手记侧同款处置）。
      // 把它当门槛 ⇒ 一次 DB 抖动就让「时间点自动进日程」静默失效。
    }
    assertCurrent()
    const id = `todo-${now}-${Math.random().toString(36).slice(2, 6)}`
    // 与会中总结路径同一套期限解析口径（ISO 优先、中文兜底）。
    const dueAt = resolveTodoDue(item.due, now)
    // §66.7：判据与随手记链逐字同形 —— due 有内容、却解不出时刻。
    // 放在 INSERT **之前**、且不看 INSERT 成不成功：
    // 「没听懂期限」与「待办没写进库」是两件事，混起来会互相掩盖。
    if (item.due && item.due.trim() !== '' && dueAt === null) dueUnresolved++
    await db.run(
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
      reminderPlanned++
      const ok = await ensureTodoReminder({
        text: item.text,
        dueText: item.due,
        assignee: item.assignee,
        meetingTitle,
        at: dueAt.at,
        source: 'meeting-ingest',
      }, operation.assertCloud)
      if (ok) reminders++
    }
  }
  return { count, reminderPlanned, reminders, dueUnresolved }
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
export async function syncMeetingMetadata(meeting: LocalMeeting, operation: RecordingOperation = captureRecordingOperation('meetings')): Promise<boolean> {
  // storage-sync 挂点：meetings=local ⇒ 元数据上行同样跳过（含 skipped 上报）。
  if (readCachedModuleMode('meetings') === 'local') {
    recordSyncReportEntry({ module: 'meetings', status: 'skipped' })
    return false
  }
  try {
    operation.assertCloud()
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
    }, operation.assertCloud)
    recordSyncReportEntry({ module: 'meetings', status: 'synced', count: 1 })
    return true
  } catch (error) {
    if (isAbortError(error)) throw error
    return false
  }
}
