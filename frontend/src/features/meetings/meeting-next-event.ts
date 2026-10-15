/**
 * meeting-next-event.ts — 把精校结果里的 `next_meeting` 变成一条**日程**（§56）。
 *
 * ── 为什么是日程，不是待办 ──
 *
 * 用户需求是「将一些时间点自动加入到计划日程中」。这个仓里其实有**两套**：
 *  - `local_todos` + `ensureTodoReminder` —— 行动项的期限（§48 建的通路）
 *  - `calendarApi` 的 `CalendarEvent` —— 真正的日程条目，`feed()` 会把它
 *    与「任务截止 / 定时任务」合并成统一日历
 *
 * 「下周三下午三点有个客户评审会」是一个**会议**，不是一件要做的活。
 * 塞进 `local_todos` 会让用户的待办列表里出现一条「下次会议」，
 * 还得手动勾掉 —— 那是错的数据模型，不是权宜之计。
 *
 * ⚠ 这也是 §54.2 实测里 `next_meeting` 明明被 `meetings.ts:250` 消费、
 *   却一直是 `null` 时的隐蔽之处：字段有人读，但**没有任何写入方**。
 *
 * ── 为什么单独成文件，且不 import 任何 I/O ──
 *
 * `buildNextMeetingEvent` 是纯函数（入参 → 普通对象，不发网络请求），
 * 按本仓的规矩（§52）它必须能被 node 直接 import 做行为断言。
 * 一旦在这里 import `localDB` / `calendarApi`，node 里就 import 不进来了 ——
 * 门禁只能退化成源码扫描，而源码扫描量的是「代码写在那」，不是「会执行」。
 */

// ⚠ 只能从 `meeting-due-plan` 取，**不能**从 `meeting-due-reminder` 取：
//   后者 re-export 了同样两个名字，但它自己 import 了 `scheduledTasksApi`
//   （发网络请求的 I/O）。从那里取会把 I/O 拖进这个纯模块，
//   node 里就 import 不进来，门禁只能退化成源码扫描。
//   这正是本文件头注释里那条规矩要防的事。
import { REMINDER_TZ, resolveTodoDue } from './meeting-due-plan.ts'
import type { CalendarEventInput } from '../calendar/types.ts'
import type { LocalMeeting } from './meetings-store.ts'

/**
 * 日程默认时长（毫秒）。
 *
 * ⚠ 模型给的 `next_meeting` 只有时间点，**没有时长**（「下周三下午三点」里
 * 没有「开一小时」）。而 `CalendarEventInput` 强制要求 `endAt`。
 * 取 60 分钟是**默认约定**而不是推断出来的真值 —— 日历里 3 点到 4 点与
 * 3 点到 3 点零一分的区别对用户没意义，但 `endAt === startAt` 会让
 * 月视图把它画成零长度条目，用户看不出那是会议。
 * 想更准就得让模型多吐一个时长字段，那是另一件事。
 */
export const NEXT_MEETING_DURATION_MS = 60 * 60 * 1000

/** 查重窗口（毫秒）：日程开始前后各留 1 小时。 */
const DEDUPE_WINDOW_MS = 60 * 60 * 1000

/** 取出 `next_meeting` 里的**时间文本**。 */
export function nextMeetingDueText(nextMeeting: string | null | undefined): string | null {
  const t = (nextMeeting ?? '').trim()
  return t === '' ? null : t
}

/**
 * §229.1：`next_meeting` 的**纯判定**部分 —— 它到底是「没有」「没听懂」
 * 还是「可以建」。I/O 层（feed/create）再把「可以建」落成
 * `duplicate` / `created` / `failed`。
 *
 * ── 为什么必须把「没有」和「没听懂」分开 ──
 *
 * 此前 `ensureNextMeetingEvent` 只返回 number（0/1），而 **0 同时是四种
 * 完全不同的结果**：
 *
 *   ① `next_meeting` 为空      → 会议里确实没定下次，说空话**是对的**；
 *   ② 有原文但解不出时刻       → **用户说了时间点，是我们没听懂**；
 *   ③ 查重命中               → 日程**已经在日历里**（这是成功）；
 *   ④ feed/create 抛异常      → **我们没建成**（离线/日历未配置/被拒）。
 *
 * 而 `buildIngestNotice` 只有 `eventsCreated > 0` 一句 ⇒ ②④ 一律被报成
 * 「什么也没发生」。② 与 §66.7 给 `action_items[].due` 补的 `unresolved`
 * 是**同一个洞**：当时只补了待办那一侧，日历这一侧漏了。
 *
 * ⚠ 三个非零态（created / unresolved / failed）**互斥**，
 *   所以文案层三句话最多只会有一句生效。
 */
export type NextMeetingGate = 'none' | 'unresolved' | 'ready'

/** 纯判定：不碰网络，只回答「有没有下次会议时间点 / 听不听得懂」。 */
export function classifyNextMeeting(
  nextMeeting: string | null | undefined,
  now: number = Date.now(),
): NextMeetingGate {
  const dueText = nextMeetingDueText(nextMeeting)
  if (!dueText) return 'none'
  if (!resolveTodoDue(dueText, now)) return 'unresolved'
  return 'ready'
}

/**
 * 由会议元数据 + `next_meeting` 原文构造日程入参；无法解析成时刻时返回 `null`。
 *
 * ⚠ `resolveTodoDue` 解析不出来就**返回 null，不猜**：
 *   模型可能填「下次再聊」这种没有时间点的短句，或填了
 *   「下个月」这种解析器覆盖不到的相对时间。这时**不建日程**是对的 ——
 *   建一条时刻错掉的日程比不建更糟（用户会照着错的点去）。
 *
 * @param now 注入参考时刻，测试用
 */
export function buildNextMeetingEvent(
  meeting: Pick<LocalMeeting, 'title' | 'location'>,
  nextMeeting: string | null | undefined,
  now: number = Date.now(),
): CalendarEventInput | null {
  const dueText = nextMeetingDueText(nextMeeting)
  if (!dueText) return null
  const due = resolveTodoDue(dueText, now)
  if (!due) return null

  const title = meeting.title?.trim() ? `${meeting.title.trim()} · 下次会议` : '下次会议'
  return {
    title,
    // ★ 日历 API 用 **unix 秒**（types.ts：「unix 秒」），
    //   而 `ParsedDue.at` 是 epoch 毫秒。少这一步会落在 1970 年。
    startAt: Math.floor(due.at / 1000),
    endAt: Math.floor((due.at + NEXT_MEETING_DURATION_MS) / 1000),
    allDay: false,
    timezone: REMINDER_TZ,
    visibility: 'private',
    ...(meeting.location?.trim() ? { location: meeting.location.trim() } : {}),
  }
}

/**
 * 该不该再建一条：同标题 + 同起始时刻 ⇒ 已经建过了。
 *
 * ⚠ 精校**可以被重复触发**（§49 之后详情页与随手记两条路径都走收尾，
 *   用户再点一次重试就会再跑一遍）。没有这道查重，日历里会堆出
 *   一串同名同时刻的「下次会议」，而且**删不掉**（分不清哪条是真的）。
 */
export function isDuplicateNextMeetingEvent(
  existing: readonly { title?: string; startAt?: number }[],
  input: CalendarEventInput,
): boolean {
  return existing.some((e) => e.title === input.title && e.startAt === input.startAt)
}

/** 查重用 feed 的时间窗（毫秒 → API 要秒）。 */
export function dedupeWindowSeconds(input: CalendarEventInput): { from: number; to: number } {
  return {
    from: input.startAt - Math.floor(DEDUPE_WINDOW_MS / 1000),
    to: input.endAt + Math.floor(DEDUPE_WINDOW_MS / 1000),
  }
}
