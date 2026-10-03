/**
 * 学习提醒的展示策略 —— 纯函数，零运行时依赖。
 *
 * 为什么要单独成文件：这些规则必须和后端的"无事不打扰"口径
 * （learning.DueSummary.Empty()、executors.digestTitle）保持一致，
 * 而 service 层依赖 http/auth，node 的 strip-types 直接加载不了那条依赖链。
 * 把策略抽成无依赖模块，才能被 `src/utils/__tests__/learning-due.test.ts`
 * 真正跑到，而不是测一份拷贝。
 */
import type { LearningDueSummary, LearningReminder } from '../types/learning'

/**
 * 今天有没有该学/该做的事。
 *
 * 判定口径与服务端 DueSummary.Empty() 逐项对应：四项全 0 时，界面收起提醒区，
 * 不显示"到期 0 / 待处理 0 / 复习 0 / 到期任务 0"这种噪音。
 */
export function hasDueWork(summary: LearningDueSummary | null | undefined): boolean {
  if (!summary) return false
  return (
    summary.dueCards > 0 ||
    summary.inbox > 0 ||
    summary.reviewItems > 0 ||
    summary.dueTasks > 0
  )
}

/**
 * 今日概览主标题的 i18n key。
 *
 * 顺序与后端 executors.digestTitle 一致：最可执行的事项优先
 * （真的到期了 > 有一堆待处理 > 在复习中 > 工作项到期）。
 * 只返回 key，不返回文案 —— 界面负责 t(key)，测试断言 key 顺序。
 */
export type DueSummaryHeadlineKey =
  | 'study.due.cardsDue'
  | 'study.due.inboxWaiting'
  | 'study.due.reviewing'
  | 'study.due.tasksDue'
  | 'study.due.allClear'

export function dueSummaryHeadlineKey(
  summary: LearningDueSummary | null | undefined,
): DueSummaryHeadlineKey {
  if (!summary) return 'study.due.allClear'
  if (summary.dueCards > 0) return 'study.due.cardsDue'
  if (summary.inbox > 0) return 'study.due.inboxWaiting'
  if (summary.reviewItems > 0) return 'study.due.reviewing'
  if (summary.dueTasks > 0) return 'study.due.tasksDue'
  return 'study.due.allClear'
}

/** 概览里用于计数的主数字：到期卡片优先，其次 inbox。 */
export function dueSummaryCount(summary: LearningDueSummary | null | undefined): number {
  if (!summary) return 0
  if (summary.dueCards > 0) return summary.dueCards
  if (summary.inbox > 0) return summary.inbox
  if (summary.reviewItems > 0) return summary.reviewItems
  return summary.dueTasks
}

/**
 * 提醒的下一次触发时间，用于设置区的"下次回顾：20:30"。
 * 没有到期提醒时返回 0，界面据此不显示这一行。
 */
export function nextReminderAt(reminders: LearningReminder[] | null | undefined): number {
  if (!reminders || reminders.length === 0) return 0
  let next = 0
  for (const r of reminders) {
    // 已确认的提醒不再参与"下一次"计算，否则用户 ack 之后界面还在报时间。
    if (r.state === 'acked' || r.state === 'done') continue
    if (r.nextDueAt <= 0) continue
    if (next === 0 || r.nextDueAt < next) next = r.nextDueAt
  }
  return next
}

/**
 * 从一条每日提醒里取出 HH:MM；非每日规则或畸形值返回空串。
 *
 * 范围校验与服务端 learning.parseHHMM 一致（hh ≤ 23、mm ≤ 59）：
 * 只查形状的话，"20:75" 会原样渲染到界面上，而服务端 POST 时又会拒绝它
 * ——界面与服务端对同一个值的判断必须一致。
 */
export function dailyRuleTime(reminder: LearningReminder | null | undefined): string {
  if (!reminder || reminder.ruleKind !== 'daily') return ''
  const v = reminder.ruleValue ?? ''
  const m = /^(\d{2}):(\d{2})$/.exec(v)
  if (!m) return ''
  const hh = Number(m[1])
  const mm = Number(m[2])
  if (hh > 23 || mm > 59) return ''
  return v
}

/**
 * 把 unix 秒格式化成 HH:MM（本地时区）。
 *
 * 刻意不引 i18n 的日期时间格式器：这个函数要被纯单测覆盖，
 * 而 i18n 实例在 node 测试环境里不可用。
 */
export function formatClockTime(unixSeconds: number): string {
  if (!Number.isFinite(unixSeconds) || unixSeconds <= 0) return ''
  const d = new Date(unixSeconds * 1000)
  const hh = String(d.getHours()).padStart(2, '0')
  const mm = String(d.getMinutes()).padStart(2, '0')
  return `${hh}:${mm}`
}
