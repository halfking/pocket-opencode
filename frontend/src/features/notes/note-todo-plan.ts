/**
 * note-todo-plan.ts — 语音笔记行动项的**纯逻辑**部分（不碰 localDB / 网络）。
 *
 * 存在理由与 meetings 侧的 meeting-due-plan.ts 同型：门禁测试要在不 mock
 * 一整条 HTTP + SQLite 链路的前提下，直接断言「哪些条目该建提醒」。
 *
 * ★ 这道拆分是被一次变异验证逼出来的，不是洁癖：把
 *   `if (dueAt) { await ensureTodoReminder(...) }` 改成 `if (false) {...}`
 * 之后，**接在 note-todo-persist.ts 上的源码文本断言仍然全绿** ——
 * 因为 `ensureTodoReminder(` 这段字面量还在文件里。文本判据只证明
 * 「代码写在那里」，不证明「它会执行」。把「该不该建提醒」这个决定
 * 收进纯函数后，判据才落在返回值上，删掉分支就一定红。
 */
import type { NoteActionItem } from '../../api/notes'
import { resolveTodoDue } from '../meetings/meeting-due-plan.ts'

/** 一条待办的落库计划（还没写进任何存储）。 */
export interface NoteTodoPlan {
  /** local_todos.id 的建议值（调用方可直接用，便于测试断言稳定）。 */
  id: string
  text: string
  assignee?: string
  /** 解析出的到期时刻（epoch ms）；解不出为 null。 */
  dueAt: number | null
  /**
   * 是否该建计划日程提醒。
   *
   * 只有「解出了真实时刻」才为 true —— 同会议侧的处置：不建假日程。
   */
  remind: boolean
  /** 原始期限短语（写进提醒 description 便于回溯）。 */
  dueText?: string
}

export interface PlanNoteTodosArgs {
  items: NoteActionItem[] | null | undefined
  now?: number
  /** 生成 id 用；测试传固定值以断言确定性。 */
  makeId: (index: number) => string
}

/**
 * 把行动项列表变成落库计划。
 *
 * 去重按 `text.trim()`：切片重叠的转写里模型很容易把同一条行动项吐两遍，
 * 重复入 local_todos 会在待办列表里出现两条一模一样的条目。
 */
export function planNoteTodos(args: PlanNoteTodosArgs): NoteTodoPlan[] {
  const { items, makeId } = args
  const now = args.now ?? Date.now()
  const seen = new Set<string>()
  const plans: NoteTodoPlan[] = []
  for (const item of items ?? []) {
    const text = item?.text?.trim()
    if (!text || seen.has(text)) continue
    seen.add(text)

    // 与会议侧同一口径：ISO 优先、中文兜底。
    const dueAt = resolveTodoDue(item.due, now)
    const plan: NoteTodoPlan = {
      id: makeId(plans.length),
      text,
      dueAt: dueAt ? dueAt.at : null,
      remind: dueAt !== null,
    }
    const assignee = item.assignee?.trim()
    if (assignee) plan.assignee = assignee
    const dueText = item.due?.trim()
    if (dueText) plan.dueText = item.due
    plans.push(plan)
  }
  return plans
}
