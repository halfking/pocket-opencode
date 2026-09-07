import type { ScheduleKind } from './types'

export type PlanMode = 'once' | 'repeat'
export type RepeatKind = 'daily' | 'weekdays' | 'weekly' | 'monthly' | 'interval'
export type IntervalUnit = 'm' | 'h' | 'd'

export interface SchedulePlan {
  mode: PlanMode
  timezone: string
  date: string
  time: string
  repeatKind: RepeatKind
  weekdays: number[]
  monthDay: number
  intervalValue: number
  intervalUnit: IntervalUnit
  custom: boolean
  customKind: ScheduleKind
  customExpr: string
}

const DEFAULT_TZ = 'Asia/Shanghai'
const WEEKDAY_LABELS = ['日', '一', '二', '三', '四', '五', '六']

function pad2(n: number): string {
  return String(n).padStart(2, '0')
}

function tomorrowDate(): string {
  const d = new Date()
  d.setDate(d.getDate() + 1)
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`
}

function parseClock(time: string): { minute: number; hour: number } {
  const match = /^(\d{1,2}):(\d{2})$/.exec(time.trim())
  if (!match) return { hour: 9, minute: 0 }
  const hour = Math.min(23, Math.max(0, Number(match[1])))
  const minute = Math.min(59, Math.max(0, Number(match[2])))
  return { hour, minute }
}

function formatClock(hour: number, minute: number): string {
  return `${pad2(hour)}:${pad2(minute)}`
}

export function defaultSchedulePlan(): SchedulePlan {
  return {
    mode: 'repeat',
    timezone: DEFAULT_TZ,
    date: tomorrowDate(),
    time: '09:00',
    repeatKind: 'weekdays',
    weekdays: [1],
    monthDay: 1,
    intervalValue: 30,
    intervalUnit: 'm',
    custom: false,
    customKind: 'cron',
    customExpr: '',
  }
}

export function encodeSchedule(plan: SchedulePlan): { scheduleKind: ScheduleKind; scheduleExpr: string } {
  if (plan.custom && plan.customExpr.trim()) {
    return { scheduleKind: plan.customKind, scheduleExpr: plan.customExpr.trim() }
  }
  if (plan.mode === 'once') {
    const date = plan.date || tomorrowDate()
    const time = plan.time || '09:00'
    return { scheduleKind: 'at', scheduleExpr: `${date}T${time}:00+08:00` }
  }
  if (plan.repeatKind === 'interval') {
    const value = Math.max(1, Math.floor(plan.intervalValue || 1))
    if (plan.intervalUnit === 'd') return { scheduleKind: 'interval', scheduleExpr: `${value * 24}h` }
    return { scheduleKind: 'interval', scheduleExpr: `${value}${plan.intervalUnit}` }
  }
  const { hour, minute } = parseClock(plan.time)
  if (plan.repeatKind === 'daily') return { scheduleKind: 'cron', scheduleExpr: `${minute} ${hour} * * *` }
  if (plan.repeatKind === 'weekdays') return { scheduleKind: 'cron', scheduleExpr: `${minute} ${hour} * * 1-5` }
  if (plan.repeatKind === 'monthly') {
    const day = Math.min(31, Math.max(1, Math.floor(plan.monthDay || 1)))
    return { scheduleKind: 'cron', scheduleExpr: `${minute} ${hour} ${day} * *` }
  }
  const days = normalizeWeekdays(plan.weekdays)
  return { scheduleKind: 'cron', scheduleExpr: `${minute} ${hour} * * ${days.join(',')}` }
}

export function parseSchedule(kind: ScheduleKind | string, expr: string): SchedulePlan {
  const plan = defaultSchedulePlan()
  const raw = (expr || '').trim()
  if (kind === 'at') {
    const parsed = parseAtExpr(raw)
    if (parsed) {
      plan.mode = 'once'
      plan.date = parsed.date
      plan.time = parsed.time
      return plan
    }
    return asCustom(plan, 'at', raw)
  }
  if (kind === 'interval') {
    const parsed = parseIntervalExpr(raw)
    if (parsed) {
      plan.mode = 'repeat'
      plan.repeatKind = 'interval'
      plan.intervalValue = parsed.value
      plan.intervalUnit = parsed.unit
      return plan
    }
    return asCustom(plan, 'interval', raw)
  }
  const cron = parseFriendlyCron(raw)
  if (cron) {
    plan.mode = 'repeat'
    plan.repeatKind = cron.repeatKind
    plan.time = cron.time
    plan.weekdays = cron.weekdays
    plan.monthDay = cron.monthDay
    return plan
  }
  return asCustom(plan, 'cron', raw)
}

export function describeSchedule(plan: SchedulePlan): string {
  if (plan.custom) return plan.customExpr ? `自定义 · ${plan.customExpr}` : '自定义计划'
  if (plan.mode === 'once') return `${plan.date} ${plan.time} 执行一次`
  if (plan.repeatKind === 'daily') return `每天 ${plan.time}`
  if (plan.repeatKind === 'weekdays') return `工作日 ${plan.time}`
  if (plan.repeatKind === 'weekly') {
    const labels = normalizeWeekdays(plan.weekdays).map((d) => WEEKDAY_LABELS[d])
    return `每周${labels.join('、')} ${plan.time}`
  }
  if (plan.repeatKind === 'monthly') return `每月 ${plan.monthDay} 日 ${plan.time}`
  const unitLabel = plan.intervalUnit === 'm' ? '分钟' : plan.intervalUnit === 'h' ? '小时' : '天'
  return `每隔 ${plan.intervalValue} ${unitLabel}`
}

export function describeTaskSchedule(kind: ScheduleKind | string, expr: string): string {
  return describeSchedule(parseSchedule(kind, expr))
}

function asCustom(plan: SchedulePlan, kind: ScheduleKind, expr: string): SchedulePlan {
  return { ...plan, custom: true, customKind: kind, customExpr: expr }
}

function normalizeWeekdays(days: number[]): number[] {
  const unique = [...new Set(days.filter((d) => Number.isInteger(d) && d >= 0 && d <= 6))]
  if (unique.length === 0) return [1]
  return unique.sort((a, b) => a - b)
}

function parseAtExpr(expr: string): { date: string; time: string } | null {
  const match = /^(\d{4}-\d{2}-\d{2})T(\d{2}):(\d{2})/.exec(expr)
  if (!match) return null
  return { date: match[1], time: `${match[2]}:${match[3]}` }
}

function parseIntervalExpr(expr: string): { value: number; unit: IntervalUnit } | null {
  const match = /^(\d+)(ms|s|m|h)$/.exec(expr)
  if (!match) return null
  const n = Number(match[1])
  if (!n) return null
  const unit = match[2]
  if (unit === 'h' && n % 24 === 0) return { value: n / 24, unit: 'd' }
  if (unit === 'm' || unit === 'h') return { value: n, unit }
  return null
}

function parseFriendlyCron(expr: string): {
  repeatKind: RepeatKind
  time: string
  weekdays: number[]
  monthDay: number
} | null {
  const match = /^(\d{1,2}) (\d{1,2}) (\*|\d{1,2}) \* (\*|[0-6](?:-[0-6])?|[0-6](?:,[0-6])+)$/.exec(expr)
  if (!match) return null
  const minute = Number(match[1])
  const hour = Number(match[2])
  if (minute > 59 || hour > 23) return null
  const time = formatClock(hour, minute)
  const dom = match[3]
  const dow = match[4]
  if (dom !== '*' && dow === '*') {
    const monthDay = Number(dom)
    if (monthDay < 1 || monthDay > 31) return null
    return { repeatKind: 'monthly', time, weekdays: [1], monthDay }
  }
  if (dom !== '*') return null
  if (dow === '*') return { repeatKind: 'daily', time, weekdays: [1], monthDay: 1 }
  if (dow === '1-5') return { repeatKind: 'weekdays', time, weekdays: [1], monthDay: 1 }
  if (/^[0-6](-[0-6])?$/.test(dow) || /^[0-6](,[0-6])+$/.test(dow)) {
    const weekdays = expandDow(dow)
    if (!weekdays) return null
    return { repeatKind: 'weekly', time, weekdays, monthDay: 1 }
  }
  return null
}

function expandDow(dow: string): number[] | null {
  if (dow.includes('-')) {
    const [start, end] = dow.split('-').map(Number)
    if (start > end) return null
    return Array.from({ length: end - start + 1 }, (_, i) => start + i)
  }
  return normalizeWeekdays(dow.split(',').map(Number))
}
