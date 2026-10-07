/**
 * meeting-due-plan.ts — 会议待办「期限短语 → 计划任务入参」的**纯逻辑**部分。
 *
 * 与 meeting-due-reminder.ts 分开是为了可测性：本文件不 import 任何
 * 网络/浏览器模块（只对 ScheduledTaskInput 做 type-only 引用），所以
 * node --test 能直接跑它。把 ensureTodoReminder（要发 HTTP）留在另一个
 * 文件里，纯逻辑才不会被一条 import 解析失败连带打挂。
 *
 * 存在理由：需求里「将一些时间点自动加入计划日程」此前完全没有落点。
 * LLM 摘要给出的 due 是自然语言（「明天下午三点」），而链路下游有两道坎：
 *   1. local_todos.due_at 只认机器格式（两处 parseDue 都只做 Date.parse，
 *      中文 due 全部静默变 null ⇒ 时间信息丢了，用户在待办列表看不到期限）；
 *   2. 计划日程（scheduled-tasks）需要一个后端能解析的 at 时刻。
 *
 * 为什么复用现成的 scheduled-tasks 而不是新造一张日程表：
 *   - 后端 schedule.go 的 ScheduleAt 分支就是 time.Parse(RFC3339, expr)，
 *     一次性任务到点跑完即过期（ComputeNext 返回 0 → 自动停用），
 *     天然就是「提醒」语义，不需要额外的完成态字段；
 *   - 前端 schedule-plan.ts 的 DEFAULT_TZ 已是 Asia/Shanghai，
 *     口径与本文件产出的 expr 一致，不引入第二套时区语义。
 */
import type { ScheduledTaskInput } from '../scheduled-tasks/types.ts'
import { dueAtToScheduleExpr, parseDueAt, type ParsedDue } from './meeting-due.ts'

/** 与 schedule-plan.DEFAULT_TZ 同口径（at 型下 expr 自带偏移，此字段影响展示）。 */
export const REMINDER_TZ = 'Asia/Shanghai'

/**
 * 解析待办期限，**ISO 优先、中文兜底**。
 *
 * ★ 为什么不能继续用 Date.parse：它把「2026-11-20」按 **UTC** 午夜解释
 *   （东八区机器上换算回来是当天 08:00），比约定的「当天上班时间」差 8 小时。
 *   统一走 parseDueAt 才能让「只有日期」稳定落在 09:00。
 *
 * @param now 注入参考时刻，测试用
 */
export function resolveTodoDue(due: string | undefined | null, now: number = Date.now()): ParsedDue | null {
  if (!due) return null
  const text = due.trim()
  if (!text) return null
  try {
    return parseDueAt(text, now)
  } catch {
    // 解析器对畸形输入已保证不抛，这里只是不让一个坏 due 拖垮入库。
    return null
  }
}

export interface ReminderInputArgs {
  /** 待办原文（进 prompt，让智能体知道要跟进什么）。 */
  text: string
  /** LLM 给的原始期限短语（「明天下午三点」）——写进 description 便于回溯。 */
  dueText?: string
  /** 负责人，可选。 */
  assignee?: string
  /** 来源会议标题，可选。 */
  meetingTitle?: string
  /** 已解析出的时刻（epoch ms）。 */
  at: number
  /** 来源标记，落进 payload 便于排查。 */
  source: 'meeting-ingest' | 'meeting-summary' | 'note-voice'
}

/**
 * 构造提醒任务入参（纯函数，不发网络请求 —— 门禁测试直接断言
 * 「expr 里带的是真实 due 时刻」，而不用 mock 一整条 HTTP 链路）。
 */
export function buildReminderInput(args: ReminderInputArgs): ScheduledTaskInput {
  const { text, dueText, assignee, meetingTitle, at, source } = args
  // 随手记（note-voice）来源没有会议标题，用笔记标题占位，
  // 否则 prompt 第一行会读成「会议待办提醒」而实际来自一条语音笔记。
  const origin = meetingTitle || (source === 'note-voice' ? '语音笔记' : '')
  const prompt = [
    `会议待办提醒：「${text}」`,
    origin ? `来源会议：${origin}` : '',
    assignee ? `负责人：${assignee}` : '',
    dueText ? `原始期限表述：${dueText}` : '',
  ].filter(Boolean).join('\n')
  return {
    name: `待办提醒：${text.slice(0, 24)}`,
    description: dueText ? `${text}（期限：${dueText}）` : text,
    kind: 'llmbff_summary',
    scheduleKind: 'at',
    scheduleExpr: dueAtToScheduleExpr(at),
    timezone: REMINDER_TZ,
    payload: { prompt, source, todoText: text, dueText: dueText ?? null, dueAt: at },
    maxRuns: 1,
    enabled: true,
  }
}
