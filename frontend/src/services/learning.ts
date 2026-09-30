/**
 * learning.ts — Learning Core 的 HTTP service 层
 * （docs/学习muse/04-数据模型与API契约.md §2.2）。
 *
 * 约定与 services/flashcards.ts 一致：
 *  - 一律不缓存状态，状态归 store / 组件；
 *  - userId / workspaceId 由服务端从 JWT 推导，这里**不发送**这两个字段；
 *  - 所有错误都以 ApiError 抛出，body 已解析，供业务判断。
 *
 * 幂等语义（ADR-004）：captureItem 对同一 (sourceKind, sourceId) 重复调用
 * 返回既有条目，因此它返回 `existed: true` 而不是抛错——重复点击"加入学习"
 * 不应该被当成失败。
 */
import { http } from '../api/http'
import type {
  LearningCaptureInput,
  LearningDueSummary,
  LearningItem,
  LearningItemsResponse,
  LearningReminder,
  LearningReminderInput,
  LearningRemindersResponse,
  LearningScheduleInput,
  LearningScheduleOutput,
  LearningStage,
  LearningStreakView,
} from '../types/learning'

// 展示策略（hasDueWork / 标题优先级 / 下次提醒时间）不在这里：
// 它们是零依赖纯函数，放在 src/utils/learning-due.ts，才能被 node 直接测到。

const BASE = '/api/learning'

export interface ListItemsFilter {
  stage?: LearningStage
  sourceKind?: string
  limit?: number
}

/**
 * 收集一条学习材料（笔记 / 邮件 / RSS / 会议 / 聊天 / 手工）。
 *
 * 幂等：对同一 (sourceKind, sourceId) 重复调用返回**既有条目**且不报错
 * （服务端用部分唯一索引实现，见 ADR-004）。服务端会区分 201/200，但
 * 这里不把它暴露成布尔量——那需要靠时间戳之类的启发式反推，不可靠；
 * 调用方要的只是"收集成功"这一件事。
 */
export async function captureItem(input: LearningCaptureInput): Promise<LearningItem> {
  return http<LearningItem>(`${BASE}/items`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
}

/** 列出学习条目（默认按收集时间倒序）。 */
export async function listItems(filter: ListItemsFilter = {}): Promise<LearningItem[]> {
  const params = new URLSearchParams()
  if (filter.stage) params.set('stage', filter.stage)
  if (filter.sourceKind) params.set('sourceKind', filter.sourceKind)
  if (filter.limit && filter.limit > 0) params.set('limit', String(filter.limit))
  const query = params.toString()
  const path = `${BASE}/items${query ? `?${query}` : ''}`
  const body = await http<LearningItemsResponse>(path)
  return body.items ?? []
}

/** 今日概览：到期卡片 + inbox 待处理 + 复习中 + 今日到期工作项。 */
export async function fetchDueSummary(): Promise<LearningDueSummary> {
  return http<LearningDueSummary>(`${BASE}/items/due`)
}

/**
 * 连续学习天数。
 *
 * `tzOffset` 是本机相对 UTC 的**秒偏移**（东八区 = -28800，因为
 * `Date.getTimezoneOffset()` 的符号是反的）。学习表只存 unix 秒、没有时区列，
 * 日界必须由客户端提供；缺省按 UTC 计算。
 *
 * 前端不缓存也不递增这个数字：它是派生值，后端每次从活动日期重算。
 */
export async function fetchStreak(tzOffset?: number): Promise<LearningStreakView> {
  const qs = tzOffset === undefined ? '' : `?tz_offset=${Math.trunc(tzOffset)}`
  return http<LearningStreakView>(`${BASE}/streak${qs}`)
}

/** 本机相对 UTC 的秒偏移（东八区返回 -28800），用于 fetchStreak。 */
export function localUtcOffsetSeconds(): number {
  return -new Date().getTimezoneOffset() * 60
}

/** 推进学习阶段（inbox → learning → review → mastered / archived）。 */
export async function updateStage(id: string, stage: LearningStage): Promise<void> {
  await http(`${BASE}/items/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ stage }),
  })
}

/** 创建或更新一条提醒（服务端按 (user, kind, itemId) 幂等 upsert）。 */
export async function upsertReminder(input: LearningReminderInput): Promise<LearningReminder> {
  return http<LearningReminder>(`${BASE}/reminders`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
}

export async function listReminders(
  state?: string,
  limit?: number,
): Promise<LearningReminder[]> {
  const params = new URLSearchParams()
  if (state) params.set('state', state)
  if (limit && limit > 0) params.set('limit', String(limit))
  const query = params.toString()
  const body = await http<LearningRemindersResponse>(`${BASE}/reminders${query ? `?${query}` : ''}`)
  return body.reminders ?? []
}

/** 推迟提醒；minutes 省略时服务端按 60 分钟处理。 */
export async function snoozeReminder(id: string, minutes?: number): Promise<number> {
  const body = await http<{ id: string; snoozedUntil: number }>(
    `${BASE}/reminders/${encodeURIComponent(id)}/snooze`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(minutes ? { minutes } : {}),
    },
  )
  return body.snoozedUntil
}

/** 确认提醒，终态不再推送。 */
export async function ackReminder(id: string): Promise<void> {
  await http(`${BASE}/reminders/${encodeURIComponent(id)}/ack`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({}),
  })
}

/**
 * 服务端权威调度：问"这张卡下一次该什么时候复习"，不在前端跑第二份 FSRS。
 * 见 docs/学习muse/06-ADR.md ADR-002 —— P0 阶段它是新增能力，
 * P4 才把写入真相切过来。
 */
export async function schedule(input: LearningScheduleInput): Promise<LearningScheduleOutput> {
  return http<LearningScheduleOutput>(`${BASE}/schedule`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(input),
  })
}
