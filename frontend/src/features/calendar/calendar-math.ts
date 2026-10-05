/**
 * 日历日期数学 —— 纯函数，无 Vue / 无网络，可直接 `node --test`。
 *
 * ## 为什么这些函数存在，而不是在 .vue 里写
 *
 * 日历最常见的一类缺陷不是「算错了月」，而是**算错了天**：同一件事在
 * UTC 里是 10-05，在 Asia/Shanghai 是 10-06。凡是用 `new Date('2026-10-06')`
 * 或 `toISOString().slice(0,10)` 取「哪一天」的代码，在东八区都会整体偏一天。
 * 这里把所有「哪一天」的判断收敛成一个入口：
 *
 *   - `dayKey(unix, tz)`      时刻 → 观察者当地的那一天（'YYYY-MM-DD'）
 *   - `unixAtLocal(...)`      当地某天的某时刻 → unix 秒（反函数）
 *
 * 全仓日历逻辑只允许走这两个函数，`.vue` 里不再出现任何 `toISOString().slice`。
 *
 * ## 为什么偏移量要算两遍（`unixAtLocal`）
 *
 * `Intl` 只能「拿一个已知时刻去问它在这个时区是几点」，反过来不行。所以
 * 「当地 09:00 是哪个 unix 时刻」必须靠试算 + 回代修正。偏移量在 DST 切换
 * 前后会变，用试算那一刻算出的偏移量再修正一次，才不会把 02:30 这种
 * **当天并不存在**的当地时间悄悄挪到前一天 01:30 或后一天 03:30。
 * 详见 `unixAtLocal` 里的两遍试算注释。
 */

export const DEFAULT_TZ = 'Asia/Shanghai'

export interface CivilDate {
  year: number
  month: number // 1-12
  day: number // 1-31
}

/** 一个「天」的固定宽度：'YYYY-MM-DD'。用定宽是为了能按字典序比大小。 */
export type DayKey = string

export function pad2(n: number): string {
  return String(n).padStart(2, '0')
}

export function dayKeyOf(civil: CivilDate): DayKey {
  return `${civil.year}-${pad2(civil.month)}-${pad2(civil.day)}`
}

export function parseDayKey(key: DayKey): CivilDate {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(key.trim())
  if (!match) return { year: 1970, month: 1, day: 1 }
  return { year: Number(match[1]), month: Number(match[2]), day: Number(match[3]) }
}

/**
 * 某时区在某个瞬时的 UTC 偏移（秒，东为正）。
 *
 * 做法：把这个瞬时按目标时区格式化，读回「年月日时分秒」，把它**当作 UTC**
 * 再解一遍时间戳，差值就是偏移。这利用了「同一组年月日时分秒在 UTC 下
 * 唯一确定一个时间戳」这一事实，避免手写月份天数表。
 */
export function tzOffsetSeconds(unixSeconds: number, timeZone: string): number {
  const formatter = new Intl.DateTimeFormat('en-US', {
    timeZone,
    hour12: false,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
  const parts = formatter.formatToParts(new Date(unixSeconds * 1000))
  const bag: Record<string, number> = {}
  for (const part of parts) {
    if (part.type === 'literal') continue
    bag[part.type] = Number(part.value)
  }
  // en-US + hour12:false 在午夜会给出 '24'，归一化成 0，否则当天会被推后一天。
  const hour = bag.hour === 24 ? 0 : bag.hour
  const asUTC = Date.UTC(bag.year, bag.month - 1, bag.day, hour, bag.minute, bag.second)
  return Math.round((asUTC - unixSeconds * 1000) / 1000)
}

/** 时刻 → 观察者当地的那一天。 */
export function dayKey(unixSeconds: number, timeZone: string = DEFAULT_TZ): DayKey {
  if (!unixSeconds) return ''
  const formatter = new Intl.DateTimeFormat('en-CA', {
    timeZone,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  })
  // en-CA 的短格式就是 YYYY-MM-DD，直接取第一段。
  return formatter.format(new Date(unixSeconds * 1000))
}

/**
 * 当地某天的某时刻 → unix 秒（`dayKey` 的反函数）。
 *
 * 两遍试算的原因：偏移量本身依赖于「算出来的时刻」，而 DST 切换当天的
 * 偏移量在切换点前后不同。第一遍用 UTC 当估算，第二遍用第一遍的偏移量
 * 回代修正——DST 当天仍然算不准的极端情形（当地时间本身不存在）会落到
 * 下一个真实时刻，这是 JS `Date` 能表达的最接近答案，本仓不做静默丢弃。
 */
export function unixAtLocal(
  year: number,
  month: number,
  day: number,
  hour: number,
  minute: number,
  timeZone: string = DEFAULT_TZ,
): number {
  const naiveUTC = Date.UTC(year, month - 1, day, hour, minute, 0)
  const firstGuess = naiveUTC / 1000 - tzOffsetSeconds(naiveUTC / 1000, timeZone)
  const refined = naiveUTC / 1000 - tzOffsetSeconds(firstGuess, timeZone)
  return Math.round(refined)
}

/** `dayKey` + 当地时刻 → unix 秒。 */
export function unixAtLocalDayKey(key: DayKey, hour: number, minute: number, timeZone: string = DEFAULT_TZ): number {
  const civil = parseDayKey(key)
  return unixAtLocal(civil.year, civil.month, civil.day, hour, minute, timeZone)
}

/** 当地某天的 00:00 对应的 unix 秒。 */
export function startOfLocalDay(key: DayKey, timeZone: string = DEFAULT_TZ): number {
  return unixAtLocalDayKey(key, 0, 0, timeZone)
}

/**
 * 当地某天的下一天 00:00 —— 作为**半开区间** `[start, nextStart)` 的右开端点。
 *
 * 用右开端点而不是「当天 23:59:59」是有原因的：23:59:59 会漏掉最后一秒内
 * 到达的事件，而且夏令时切换日当天的实际长度不是 24 小时（那天可能是 23 或 25
 * 小时）。从「当天 00:00」推进到「下一自然日 00:00」把这两种情况都盖住了。
 */
export function endOfLocalDay(key: DayKey, timeZone: string = DEFAULT_TZ): number {
  const next = addDays(key, 1)
  return unixAtLocalDayKey(next, 0, 0, timeZone)
}

export function addDays(key: DayKey, days: number): DayKey {
  const civil = parseDayKey(key)
  // 用 UTC 做「加天数」：UTC 没有 DST，+1 天就是 +1 天，不会出现 23/25 小时
  // 那天的错位。随后再把这个纯日期交给 unixAtLocal 换算成当地时间。
  const base = new Date(Date.UTC(civil.year, civil.month - 1, civil.day))
  base.setUTCDate(base.getUTCDate() + days)
  return dayKeyOf({
    year: base.getUTCFullYear(),
    month: base.getUTCMonth() + 1,
    day: base.getUTCDate(),
  })
}

/** 两个 DayKey 相差几天（b - a）。 */
export function diffDays(a: DayKey, b: DayKey): number {
  const ca = parseDayKey(a)
  const cb = parseDayKey(b)
  const ma = Date.UTC(ca.year, ca.month - 1, ca.day)
  const mb = Date.UTC(cb.year, cb.month - 1, cb.day)
  return Math.round((mb - ma) / 86400000)
}

/** 该日是周几：0=周日 … 6=周六（与 Date#getDay 一致）。 */
export function weekdayOf(key: DayKey): number {
  const civil = parseDayKey(key)
  return new Date(Date.UTC(civil.year, civil.month - 1, civil.day)).getUTCDay()
}

/** 该月天数（month 为 1-12）。 */
export function daysInMonth(year: number, month: number): number {
  return new Date(Date.UTC(year, month, 0)).getUTCDate()
}

export interface MonthGridCell {
  dayKey: DayKey
  dayOfMonth: number
  /** 是否属于当前展示的月份。相邻月补白时为 false。 */
  inMonth: boolean
  isToday: boolean
  isWeekend: boolean
}

/**
 * 生成月视图的 6×7 宫格。
 *
 * 固定 6 周（42 格）而不是「按需 4/5/6 周」：月视图高度固定，翻月时不发生
 * 布局跳动（uxpatterns 对 calendar view 的稳定性要求）。周起始日由
 * `weekStart` 决定：0=周日（中国习惯是 1=周一）。
 */
export function buildMonthGrid(
  year: number,
  month: number,
  todayKey: DayKey,
  weekStart = 1,
): MonthGridCell[] {
  const first = dayKeyOf({ year, month, day: 1 })
  const firstWeekday = weekdayOf(first)
  const lead = (firstWeekday - weekStart + 7) % 7
  const gridStart = addDays(first, -lead)
  const cells: MonthGridCell[] = []
  for (let i = 0; i < 42; i += 1) {
    const key = addDays(gridStart, i)
    const civil = parseDayKey(key)
    const wd = weekdayOf(key)
    cells.push({
      dayKey: key,
      dayOfMonth: civil.day,
      inMonth: civil.month === month && civil.year === year,
      isToday: key === todayKey,
      isWeekend: wd === 0 || wd === 6,
    })
  }
  return cells
}

/** 月份标签用的「2026-10」形态，避免调用方各自拼 `${y}-${m}` 漏补零。 */
export function monthKey(year: number, month: number): string {
  return `${year}-${pad2(month)}`
}

export function parseMonthKey(key: string): { year: number; month: number } {
  const match = /^(\d{4})-(\d{2})$/.exec(key.trim())
  if (!match) return { year: 1970, month: 1 }
  return { year: Number(match[1]), month: Number(match[2]) }
}

/** 月份平移：13 月 → 下一��� 1 月，0 月 → 上一年 12 月。 */
export function shiftMonth(key: string, delta: number): string {
  const { year, month } = parseMonthKey(key)
  const total = year * 12 + (month - 1) + delta
  return monthKey(Math.floor(total / 12), (total % 12) + 1)
}

/** 一天的起止 unix 秒。用于向后端要数据时的范围参数。 */
export function localDayRange(key: DayKey, timeZone: string = DEFAULT_TZ): { start: number; end: number } {
  return { start: startOfLocalDay(key, timeZone), end: endOfLocalDay(key, timeZone) }
}

/** 展示用时间：`HH:mm`（24 小时制，不受 locale 影响）。 */
export function formatClock(unixSeconds: number, timeZone: string = DEFAULT_TZ): string {
  if (!unixSeconds) return ''
  return new Intl.DateTimeFormat('en-GB', {
    timeZone,
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(new Date(unixSeconds * 1000))
}

/** 该时刻落在当地几点（0-23）。用于按小时分桶排事件。 */
export function localHour(unixSeconds: number, timeZone: string = DEFAULT_TZ): number {
  const formatter = new Intl.DateTimeFormat('en-US', {
    timeZone,
    hour: '2-digit',
    hour12: false,
  })
  const parts = formatter.formatToParts(new Date(unixSeconds * 1000))
  for (const part of parts) {
    if (part.type === 'hour') {
      const value = Number(part.value)
      return value === 24 ? 0 : value
    }
  }
  return 0
}

/** 该时刻落在当地第几分钟（0-59）。 */
export function localMinute(unixSeconds: number, timeZone: string = DEFAULT_TZ): number {
  const formatter = new Intl.DateTimeFormat('en-US', {
    timeZone,
    minute: '2-digit',
  })
  for (const part of formatter.formatToParts(new Date(unixSeconds * 1000))) {
    if (part.type === 'minute') return Number(part.value)
  }
  return 0
}

/** `datetime-local` 输入框要的值：当地 'YYYY-MM-DDTHH:mm'。 */
export function toLocalInputValue(unixSeconds: number, timeZone: string = DEFAULT_TZ): string {
  if (!unixSeconds) return ''
  const dateKey = dayKey(unixSeconds, timeZone)
  const civil = parseDayKey(dateKey)
  return `${dateKey}T${pad2(localHour(unixSeconds, timeZone))}:${pad2(localMinute(unixSeconds, timeZone))}`
}

/** `datetime-local` 输入框的值 → unix 秒。空串返回 0（「未设置」）。 */
export function fromLocalInputValue(value: string, timeZone: string = DEFAULT_TZ): number {
  const match = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(value.trim())
  if (!match) return 0
  return unixAtLocal(
    Number(match[1]),
    Number(match[2]),
    Number(match[3]),
    Number(match[4]),
    Number(match[5]),
    timeZone,
  )
}

/**
 * 设备当前时区，取不到时回落 DEFAULT_TZ。
 *
 * 回落是有意为之而不是抛错：日历在拿不到时区时必须还能显示，
 * 显示在默认时区远好过整页崩掉。
 */
export function deviceTimeZone(): string {
  try {
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone
    return tz || DEFAULT_TZ
  } catch {
    return DEFAULT_TZ
  }
}