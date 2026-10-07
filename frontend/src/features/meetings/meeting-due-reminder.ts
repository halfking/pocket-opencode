/**
 * meeting-due-reminder.ts — 真正把提醒任务发到后端的那一层（唯一带 I/O 的部分）。
 *
 * 纯逻辑（期限解析 + 入参构造）在 meeting-due-plan.ts，两者拆开是为了让
 * 门禁测试不必 mock HTTP 就能直接断言「expr 带的是真实 due 时刻」。
 *
 * ★ 失败处理：这里**永不抛异常**。录音结束后的入库是主链路（待办/笔记
 *   必须落库），提醒只是附加价值；云端不可达时静默跳过即可，不能让主
 *   链路跟着炸。所以调用方可以直接 await 而不用 try/catch。
 */
import { scheduledTasksApi } from '../scheduled-tasks/api'
import { buildReminderInput, type ReminderInputArgs } from './meeting-due-plan'

export { buildReminderInput, resolveTodoDue, REMINDER_TZ } from './meeting-due-plan'
export type { ReminderInputArgs } from './meeting-due-plan'

/**
 * 为一条待办建提醒任务。**永不抛异常**。
 *
 * @returns 是否成功创建（失败为 false，调用方仅用于统计/日志）
 */
export async function ensureTodoReminder(args: ReminderInputArgs): Promise<boolean> {
  try {
    await scheduledTasksApi.create(buildReminderInput(args))
    return true
  } catch {
    return false
  }
}
