/**
 * 日历条目聚合 —— 纯函数。回答一个问题：**这一天有哪些事？**
 *
 * ## 为什么要一个「统一条目」而不是各自渲染各自的列表
 *
 * 目标里最重要的一条是「重要的信息均需要在日历中」。本仓里带时间的域有
 * 任务（due_at）、定时任务（next_run_at）、会议、以及新建的日程事件。
 * 如果每种来源在月视图里各画各的，用户会得到四个互不相干的月历；而他
 * 真正想问的是「这天我要做什么」。所以这里把所有来源压成同一个
 * `CalendarEntry` 形状，由**一个**宫格渲染器消费。
 *
 * 来源信息不被抹掉：`source` 与 `refId` 一路带到 UI，点条目能跳回它
 * 真正的详情页，而不是在一个日历副本里编辑一份影子数据。
 */

import {
  addDays,
  DEFAULT_TZ,
  dayKey,
  endOfLocalDay,
  localHour,
  localMinute,
  startOfLocalDay,
  type DayKey,
// 值导入必须带 .ts 扩展名：本文件被 `node --test` 直接加载，node 不做
// bundler 的扩展名补全，写成 './calendar-math' 会 ERR_MODULE_NOT_FOUND。
// （`import type` 会被擦除，所以类型导入不写扩展名也没问题。）
} from './calendar-math.ts'
import type { CalendarEntry, CalendarSource } from './types.ts'

// 形状定义只有一份（types.ts）：曾经这里另抄一份 CalendarSource，多出来的
// 'meeting' 让 store 传入的 source 与 SOURCE_META 的键类型对不上，
// vue-tsc 直接报错。两份定义漂移就是这么发生的。
export type { CalendarEntry, CalendarSource }

/** 一天的分组结果。 */
export interface DayBucket {
  dayKey: DayKey
  /** 全天条目 + 当天有交集的跨天条目在前，计时条目按开始时间在后。 */
  allDay: CalendarEntry[]
  timed: CalendarEntry[]
  /** allDay.length + timed.length，供月格上的「+N」角标用。 */
  total: number
}

/**
 * 条目是否与某一天相交。
 *
 * 用半开区间 `[start, end)`：否则一个 09:00–10:00 的事件会在 10-06 和
 * 10-07 各出现一次（因为 10:00 同时是前一事件的终点和后一天的起点），
 * 而 `endAt == startAt` 的零长条目会**整天都不出现**——那正好是任务到期
 * 这种最常见的形态。
 */
export function overlapsDay(entry: CalendarEntry, day: DayKey, timeZone: string = DEFAULT_TZ): boolean {
  const dayStart = startOfLocalDay(day, timeZone)
  const dayEnd = endOfLocalDay(day, timeZone)
  const entryEnd = entry.endAt > entry.startAt ? entry.endAt : entry.startAt + 1
  return entry.startAt < dayEnd && entryEnd > dayStart
}

/** 同一天内的排序权重：全天在前，再按开始时间，再按标题稳定排序。 */
function compareEntries(a: CalendarEntry, b: CalendarEntry): number {
  if (a.allDay !== b.allDay) return a.allDay ? -1 : 1
  if (a.startAt !== b.startAt) return a.startAt - b.startAt
  return a.title.localeCompare(b.title)
}

/**
 * 挑出与某一天相交的条目并分组。
 *
 * `allDay` 只收「真的被标成全天」的条目；跨天但有具体钟点的条目算 timed，
 * 因为它在时间轴上有确切位置（月视图里画成一条横跨的带子）。
 */
export function bucketForDay(
  entries: CalendarEntry[],
  day: DayKey,
  timeZone: string = DEFAULT_TZ,
): DayBucket {
  const matched = entries.filter((entry) => overlapsDay(entry, day, timeZone))
  const allDay = matched.filter((entry) => entry.allDay).sort(compareEntries)
  const timed = matched.filter((entry) => !entry.allDay).sort(compareEntries)
  return { dayKey: day, allDay, timed, total: matched.length }
}

/**
 * 把条目摊平成 `dayKey → 该天条目` 的索引，供 42 格宫格一次性查询。
 *
 * 只索引条目**真正覆盖到的天**：跨 3 天的事件进 3 个桶，单日事件只进 1 个。
 * 索引的是有限的天数而不是「月」，所以查宫格是 O(1)，翻月不需要重算。
 */
export function indexByDay(
  entries: CalendarEntry[],
  timeZone: string = DEFAULT_TZ,
): Map<DayKey, CalendarEntry[]> {
  const index = new Map<DayKey, CalendarEntry[]>()
  for (const entry of entries) {
    const end = entry.endAt > entry.startAt ? entry.endAt : entry.startAt + 1
    // 起点所在的天一定命中；终点若正好落在某天的 00:00，那天**不含**它（半开区间）。
    let cursor = dayKey(entry.startAt, timeZone)
    const lastDay = dayKey(end - 1, timeZone)
    // 防御：极端脏数据（endAt 远大于 startAt）不该让这个函数死循环。
    let guard = 0
    while (guard < 3660) {
      guard += 1
      if (!index.has(cursor)) index.set(cursor, [])
      index.get(cursor)!.push(entry)
      if (cursor === lastDay) break
      cursor = addDays(cursor, 1)
    }
  }
  for (const list of index.values()) list.sort(compareEntries)
  return index
}

/** 取某天的桶；无数据时返回空桶（而不是 undefined），调用方不必判空。 */
export function entriesOn(
  index: Map<DayKey, CalendarEntry[]>,
  day: DayKey,
): CalendarEntry[] {
  return index.get(day) ?? []
}

export interface TimedSlot {
  entry: CalendarEntry
  /** 在时间轴上的起点（分钟，从当地 00:00 起算）。 */
  topMinutes: number
  heightMinutes: number
}

/**
 * 把某天的计时条目摊成时间轴上的分钟坐标。
 *
 * 跨天条目的**显示**时长被裁剪到当天边界：一条 23:00 → 次日 02:00 的事件在
 * 06 日画到 24:00、07 日画到 02:00，而不是在 06 日画一条 27 小时的条。
 */
export function layoutDayTimeline(
  entries: CalendarEntry[],
  day: DayKey,
  timeZone: string = DEFAULT_TZ,
): TimedSlot[] {
  const dayStart = startOfLocalDay(day, timeZone)
  const dayEnd = endOfLocalDay(day, timeZone)
  const slots: TimedSlot[] = []
  for (const entry of entries) {
    if (entry.allDay) continue
    const rawEnd = entry.endAt > entry.startAt ? entry.endAt : entry.startAt + 60 * 60
    const clippedStart = Math.max(entry.startAt, dayStart)
    const clippedEnd = Math.min(rawEnd, dayEnd)
    if (clippedEnd <= clippedStart) continue
    // topMinutes 必须取**裁剪后**的起点：跨天条目在次日要画到当天 00:00，
    // 而不是从它原本的 23:00 起算（那会把条子画到当天之外去）。
    const topMinutes = localHour(clippedStart, timeZone) * 60 + localMinute(clippedStart, timeZone)
    slots.push({
      entry,
      topMinutes,
      heightMinutes: Math.max(15, Math.round((clippedEnd - clippedStart) / 60)),
    })
  }
  return slots.sort((a, b) => a.topMinutes - b.topMinutes || a.entry.title.localeCompare(b.entry.title))
}

/**
 * 同一天的计时条目是否互相冲突。
 *
 * 相邻不算冲突（10:00 结束 / 10:00 开始是背靠背，不是撞车）——判据用
 * `startA < endB && startB < endA` 的严格重叠，而不是闭区间相交。
 *
 * 零长条目（任务/定时任务的到期点）**不参与**冲突判定：它是一个时刻而不是
 * 一段时间，不占用任何钟点。若让它参与，本仓每条带 due_at 的任务都会和当天的
 * 任何安排判成撞车，冲突提示随即失去意义。
 * （注意这与 `layoutDayTimeline` 给零长条目保底 1 小时并不矛盾：那是为了
 * 显示——条子要有高度可点；这里问的是「有没有占用时间」，答案是不占用。）
 */
export function hasConflict(a: CalendarEntry, b: CalendarEntry): boolean {
  if (a.allDay || b.allDay) return false
  if (a.endAt <= a.startAt || b.endAt <= b.startAt) return false
  return a.startAt < b.endAt && b.startAt < a.endAt
}

/** 该天是否有任何未完成的条目——用于月格上的「今天有事」强调。 */
export function hasPending(entries: CalendarEntry[]): boolean {
  return entries.some((entry) => !entry.done)
}

/** 逾期判定：未完成且截止时刻早于 now。 */
export function computeOverdue(entry: CalendarEntry, nowUnix: number): boolean {
  if (entry.done) return false
  const end = entry.endAt > entry.startAt ? entry.endAt : entry.startAt
  return end < nowUnix
}

/** 生成稳定 id。id 冲突时索引会互相覆盖，所以前缀不可省。 */
export function entryId(source: CalendarSource, refId: string): string {
  return `${source}:${refId}`
}