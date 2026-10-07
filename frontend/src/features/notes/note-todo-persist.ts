/**
 * note-todo-persist.ts — 语音笔记的行动项落库 + 期限入日程（唯一带 I/O 的部分）。
 *
 * 需求「录音时即时总结，并把一些时间点自动加入计划日程」此前在**随手记侧
 * 完全没有落点**：录音停止后只调 /api/notes/{id}/summarize 拿一个 summary
 * 字符串，笔记里说过的「明天下午三点」这类期限没有任何去处 ——
 * 不进待办、不进日程、也不在界面上出现。
 *
 * 为什么不新造一套期限解析：会议侧已经有 resolveTodoDue（ISO 优先、中文
 * 兜底）与 ensureTodoReminder。两侧对「下午3点 = 15:00」「明天无钟点 =
 * 09:00」的口径必须一致，否则同一个人在会议里说「明天下午三点」和在笔记里
 * 说同一句会得到两个不同的提醒时刻。解析与「该不该建提醒」的判定都在
 * note-todo-plan.ts（纯函数，可直接断言），本文件只负责执行。
 *
 * 与会议侧的差别：note_id 有值、meeting_id 为 null（这条待办来自一条笔记
 * 而不是一场会），source 用 'note-voice' 以便在 payload 里区分来源。
 */
import { localDB } from '../../native/local-db'
import type { NoteActionItem } from '../../api/notes'
import { ensureTodoReminder } from '../meetings/meeting-due-reminder'
import { planNoteTodos } from './note-todo-plan.ts'

export interface NoteTodoResult {
  /** 成功写入 local_todos 的条数。 */
  created: number
  /** 成功建提醒的条数（<= reminderPlanned）。 */
  reminders: number
  /**
   * **尝试**建提醒的条数。
   *
   * ⚠ 2026-10-06 新增。此前只有 reminders（成功数），调用方据此判断不了
   * 「一条都没建」是「这些行动项本来就没有期限」还是「建提醒全失败了」，
   * 于是无论哪种都显示「已加入 N 条待办与日程提醒」——
   * 提醒一条没建出来时，用户仍然以为时间点已经进了日程。
   */
  reminderPlanned: number
  /** 期限解不出来的条数 —— 记下来是为了让调用方能如实告诉用户。 */
  unresolved: number
}

/**
 * 把一条语音笔记的行动项写进 local_todos，并为能解出时刻的条目建提醒。
 *
 * **永不抛异常**：这是录音停止后的收尾链路，笔记正文已经落库了，
 * 待办与提醒都是附加价值。localDB 失败不应该让整条草稿流程看起来失败
 * （presentVoiceDraft 的 catch 会把整个总结标成失败，用户看到的却是
 * 「总结失败」——而总结其实已经拿到了）。
 */
/**
 * ★§147（2026-10-07）新增：这条链原先**没有任何幂等**，而重复是最自然的用户路径。
 *
 * 可达路径（每一环都已在生产代码里核对过，不是假想）：
 *   ① 随手记录完音 → `NoteListView.presentVoiceDraft` **自动**调 summarize
 *      → `createNoteTodos` 插入 N 条待办 + M 个提醒；
 *   ② ASR 把人名抄错是已知高频问题（§138 真音频实测：两个模型都把「林岚」抄成「林兰」），
 *      用户在 `NoteEditView` 改正文 —— 顺手、必然会发生；
 *   ③ 回详情页再点一次「总结」→ **又一次** `createNoteTodos`。
 * ⇒ 旧实现每次都生成全新 id（`todo-${now}-${i}-${随机4位}`）并裸 `INSERT`，
 *   `ensureTodoReminder` 也是裸 `create` ⇒ **待办与日程提醒各翻一倍**，
 *   而且两批 `note_id` 相同、内容相同，只有 id 不同 ⇒ 任何按 note 归组的界面都会显示重复。
 *
 * ⚠ 与记账那条路对照（别记错对象）：`handleNoteSummarize` 的**财务记账早就是幂等的**
 *   —— `note_ref = "note:" + id` + `GetByNoteRefScoped` 先查后建，`CreateScoped` 内部再去重。
 *   本函数是**同一条需求里另一个出口**，它当时没有跟上。
 *
 * ⚠ 本次只修「**同一条不许重复**」这一半 —— 任何读法下重复都是错的。
 *   「模型这次给的内容和上次不同」该怎么办（替换旧的？并存？）是**产品取舍**，
 *   仍未拍板，见 docs §147；**不在这里代拍**。
 */
async function alreadyExists(noteId: string, title: string): Promise<boolean> {
  const hit = await localDB.queryOne(
    `SELECT id FROM local_todos
     WHERE note_id = ? AND title = ? AND extracted_from_voice = 1`,
    [noteId, title],
  )
  return hit !== null && hit !== undefined
}

export async function createNoteTodos(
  noteId: string,
  items: NoteActionItem[] | null | undefined,
  noteTitle = '',
): Promise<NoteTodoResult> {
  const result: NoteTodoResult = { created: 0, reminders: 0, reminderPlanned: 0, unresolved: 0 }
  const now = Date.now()
  const plans = planNoteTodos({
    items,
    now,
    makeId: (i) => `todo-${now}-${i}-${Math.random().toString(36).slice(2, 6)}`,
  })
  for (const plan of plans) {
    if (plan.dueText && plan.dueAt === null) result.unresolved++
    // ★§147 幂等：这条待办已经在库里 ⇒ 不再插第二条，也**不再建一次提醒**
    //   （提醒是跟着这条待办的，待办没新建，提醒自然不该新建 ——
    //   否则就是同一条待办配两个日程时间点）。
    //   `created` 因此只数**真正新写入**的条数 ⇒ 重复总结时它为 0，
    //   §127 的提示逻辑（`created === 0` 且什么都没做 ⇒ 不提示）会如实显示「没有新增」。
    try {
      if (await alreadyExists(noteId, plan.text)) continue
    } catch {
      // 查重本身失败**不能**挡掉写入：查重是防重复，不是准入门槛。
      // 查不动就退化成旧行为（可能重复），好过一条待办都建不出来。
    }
    try {
      await localDB.run(
        `INSERT INTO local_todos
         (id, note_id, title, description, status, priority, due_at, extracted_from_voice, meeting_id, created_at, updated_at)
         VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
        [
          plan.id, noteId, plan.text, plan.assignee ? `负责人：${plan.assignee}` : null,
          'pending', 'medium', plan.dueAt, 1, null, now, now,
        ],
      )
      result.created++
    } catch {
      // 单条入库失败不中断后面几条：模型给 5 条行动项，第 1 条写不进去
      // 不该让 2~5 条也一起消失。
      continue
    }
    // 解出真实时刻才建提醒，不建假日程（同会议侧的处置）。
    if (plan.remind && plan.dueAt !== null) {
      result.reminderPlanned++
      const ok = await ensureTodoReminder({
        text: plan.text,
        dueText: plan.dueText,
        assignee: plan.assignee,
        meetingTitle: noteTitle,
        at: plan.dueAt,
        source: 'note-voice',
      })
      if (ok) result.reminders++
    }
  }
  return result
}
