/**
 * 月视图的**呈现层**纯函数。
 *
 * ## 为什么这些要从 .vue 里搬出来
 *
 * 它们决定「这一天格上画什么」——也就是用户看到的全部内容。留在组件里就
 * 只能靠人眼检查，而这里的逻辑恰好是日历最容易错的地方：跨天条目该占几格、
 * 取数窗口该覆盖到哪天、点相邻月补白格要不要翻月。
 *
 * 判定一律走 calendar-feed 的 `overlapsDay`（半开区间，已被测试钉住），
 * **不要**在这里重写一份区间判断：组件里那份曾用闭区间 `startAt <= endOfDay`，
 * 于是相邻两天会重复显示同一条事件。
 */
import {
  buildMonthGrid,
  endOfLocalDay,
  monthKey,
  parseMonthKey,
  startOfLocalDay,
  type DayKey,
  type MonthGridCell,
} from './calendar-math.ts'
import { overlapsDay } from './calendar-feed.ts'
import type { CalendarEntry } from './types.ts'

/** 月格默认周起始（1=周一，与 buildMonthGrid 的约定一致）。 */
export const DEFAULT_WEEK_START = 1

/**
 * 某天要画哪些色块，以及还剩几条没画。
 *
 * 全部条目都按「与这一天相交」判定（`overlapsDay`），因此跨天条目会在它
 * 覆盖的每一天都出现 —— 一场三天的会议在月视图上是连续三格，而不是只
 * 出现在第一天。
 */
export function chipsForDay(
  entries: CalendarEntry[],
  day: DayKey,
  timeZone: string,
  maxChips: number,
): { visible: CalendarEntry[]; overflow: number } {
  const onDay = entries.filter((entry) => overlapsDay(entry, day, timeZone))
  return {
    visible: onDay.slice(0, maxChips),
    overflow: Math.max(0, onDay.length - maxChips),
  }
}

/** 某天的条目总数（不受 maxChips 限制），用于角标与无障碍播报。 */
export function countOnDay(entries: CalendarEntry[], day: DayKey, timeZone: string): number {
  return entries.filter((entry) => overlapsDay(entry, day, timeZone)).length
}

/**
 * 展示某个月需要的取数窗口。
 *
 * 窗口必须覆盖**整张 42 格宫格**，含首尾的相邻月补白：只取「本月 1 号到
 * 本月末」，翻到月末时相邻月那几格会永远是空的 —— 而用户正是靠那几格
 * 规划下个月。
 */
export function fetchWindowForMonth(
  month: string,
  timeZone: string,
  todayKey: DayKey,
  weekStart: number = DEFAULT_WEEK_START,
): { from: number; to: number; grid: MonthGridCell[] } {
  const { year, month: m } = parseMonthKey(month)
  const grid = buildMonthGrid(year, m, todayKey, weekStart)
  const first = grid[0].dayKey
  const last = grid[grid.length - 1].dayKey
  return {
    from: startOfLocalDay(first, timeZone),
    // 半开区间右开端点 = 最后一天的「次日 00:00」。
    to: endOfLocalDay(last, timeZone),
    grid,
  }
}

/**
 * 点中某一天之后，月份视图应该显示哪个月。
 *
 * 补白格属于相邻月：若点了却**不**翻月，选中日就落在当前视图之外，界面
 * 看起来「点了没反应」—— 这是 BUG-P/Q 那一类（入口在、点不动）的日历版。
 */
export function resolveSelectedDay(
  tapped: DayKey,
  currentMonth: string,
  grid: MonthGridCell[],
): { selectedDay: DayKey; month: string } {
  const selectedDay = tapped
  const inCurrentMonth = grid.some((cell) => cell.inMonth && cell.dayKey === tapped)
  if (inCurrentMonth) return { selectedDay, month: currentMonth }
  // 注意：这里**不能**用 parseMonthKey —— 它吃的是 'YYYY-MM'，传进来的
  // 是完整日期 'YYYY-MM-DD'，正则不匹配会静默回落 1970-01，于是点相邻月
  // 会跳到 1970 年 1 月。
  const { year, month } = splitDay(tapped)
  return { selectedDay, month: monthKey(year, month) }
}

/** 月份标题。交给 Intl 按观察者语言格式化，不自己拼「2026 年 10 月」。 */
export function monthTitle(month: string, locale?: string): string {
  const { year, month: m } = parseMonthKey(month)
  return new Intl.DateTimeFormat(locale, { year: 'numeric', month: 'long' }).format(
    new Date(Date.UTC(year, m - 1, 1)),
  )
}

/**
 * 星期表头标签，按 weekStart 旋转。
 *
 * 锚点取 2026-10-05（周一），逐日推进 —— 用 UTC 做推进，绕开「本地午夜」
 * 带来的偏移，否则某些时区下表头会整体错一天。
 */
export function weekdayLabels(locale?: string, weekStart: number = DEFAULT_WEEK_START): string[] {
  const anchorMonday = new Date(Date.UTC(2026, 9, 5)) // getDay() === 1
  const labels: string[] = []
  for (let i = 0; i < 7; i += 1) {
    const day = new Date(anchorMonday)
    // 目标列的星期是 (weekStart + i) % 7，而锚点自己的星期是 1，
    // 所以偏移量要减掉这个 1。写成 `+(weekStart+i)%7` 会整体右移一天
    // （weekStart=1 时第一列变成 Tue）。
    const offset = (((weekStart + i) % 7) - 1 + 7) % 7
    day.setUTCDate(anchorMonday.getUTCDate() + offset)
    labels.push(new Intl.DateTimeFormat(locale, { weekday: 'short' }).format(day))
  }
  return labels
}

/** 某天的人类可读标题（列表区上方用）。 */
export function dayTitle(day: DayKey, locale?: string): string {
  const { year, month, day: d } = splitDay(day)
  return new Intl.DateTimeFormat(locale, {
    month: 'long',
    day: 'numeric',
    weekday: 'short',
  }).format(new Date(Date.UTC(year, month - 1, d, 12)))
}

function splitDay(key: DayKey): { year: number; month: number; day: number } {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(key)
  if (!match) return { year: 1970, month: 1, day: 1 }
  return { year: Number(match[1]), month: Number(match[2]), day: Number(match[3]) }
}

/** 条目在月格上的一行文本：全天标「全天」，否则 `HH:mm`，再否则标「截止」。 */
export function chipTimeLabel(
  entry: CalendarEntry,
  timeZone: string,
  labels: { allDay: string; dueBy: string },
): string {
  if (entry.allDay) return labels.allDay
  return formatClockOf(entry.startAt, timeZone) || labels.dueBy
}

function formatClockOf(unixSeconds: number, timeZone: string): string {
  if (!unixSeconds) return ''
  return new Intl.DateTimeFormat('en-GB', {
    timeZone,
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(unixSeconds * 1000))
}
