/**
 * 日历前端数据形状。
 *
 * 命名沿用后端 JSON 字段（camelCase），不做二次映射 —— 多一层映射就多一处
 * 两边不同步的机会，而这里没有任何需要转换的语义。
 */
import { ICON, type IconName } from '../../constants/icons'

/** 条目来源：日程 / 任务截止 / 定时任务。与后端 calendar.FeedEntry.Source 对齐。 */
export type CalendarSource = 'event' | 'task' | 'scheduled'

export interface CalendarEntry {
  /** `${source}:${refId}`，跨来源唯一。 */
  id: string
  source: CalendarSource
  /** 来源域内主键，点击时用于跳回详情。 */
  refId: string
  title: string
  description?: string
  location?: string
  /** unix 秒。 */
  startAt: number
  /** unix 秒；等于 startAt 表示「一个时刻」（任务截止）。 */
  endAt: number
  allDay: boolean
  timezone: string
  done?: boolean
  remindAt?: number
}

export interface CalendarEventInput {
  title: string
  description?: string
  location?: string
  startAt: number
  endAt: number
  allDay: boolean
  timezone: string
  remindAt?: number
  visibility: 'private' | 'shared'
}

export interface CalendarEvent extends CalendarEventInput {
  id: string
  workspaceId?: string
  ownerUserId?: string
  createdAt: number
  updatedAt: number
}

export interface CalendarFeedResponse {
  entries: CalendarEntry[]
  from: number
  to: number
  serverTimeMs?: number
}

/** 月视图上方的筛选条：勾掉某个来源即隐藏它。 */
export const CALENDAR_SOURCES: CalendarSource[] = ['event', 'task', 'scheduled']

/** 每个来源的展示元数据。颜色是**附加**信号，条目同时带图标与文字，
 *  因此不依赖颜色单独传达含义（无障碍要求，见 uxpatterns 的 Calendar View）。
 *
 *  图标名取自 constants/icons 的 ICON 注册表而不是裸字符串：这些名字是运行时
 *  决定的，静态扫描器看不见，必须显式登记，否则字体子集会把它们裁掉、
 *  真机上显示成 EVENT / TASK_ALT 这样的字面量。 */
export const SOURCE_META: Record<CalendarSource, { color: string; materialIcon: IconName }> = {
  event: { color: 'var(--cal-source-event)', materialIcon: ICON.calendarSourceEvent },
  task: { color: 'var(--cal-source-task)', materialIcon: ICON.calendarSourceTask },
  scheduled: { color: 'var(--cal-source-scheduled)', materialIcon: ICON.calendarSourceScheduled },
}

/** 月视图一格里最多显示几条；多出来的折叠成「+N」。 */
export const MAX_CHIPS_PER_CELL = 3