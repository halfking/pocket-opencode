/**
 * meeting-reminder-note.ts — 会议总结 toast 的提醒后缀（纯文案层）。
 *
 * 2026-10-06 的缺陷：`createMeetingTodos` 算出了 `reminders`，函数体里却写
 * `void reminders // 计数仅供日志` —— 提醒创建失败（后端不可达 / 网络 / 权限）
 * 时调用方**无从得知**，于是照实弹一句「已生成当前总结」，而「下周三下午三点」
 * 那条提醒其实一条都没建成。总结成功了，提醒失败了，两件事被一句话盖过去。
 *
 * 与 note-todo-notice.ts 同型：纯函数 + 无 value import，判据落在返回值上。
 * 这里返回的是**后缀**（拼在「已生成当前总结」后面），返回空串即无需补充说明。
 */

/**
 * 生成会议总结 toast 的提醒结果后缀。
 *
 * ⚠ 2026-10-07（§66.7）：第三个参数 `unresolved` 是**补上会议链漏掉的那一格**。
 *
 * 随手记链早就有第四个计数器 `unresolved`（`note-todo-persist.ts`：
 * `if (plan.dueText && plan.dueAt === null) result.unresolved++`），
 * 且 `note-todo-notice.ts` 有一句专门的「N 条期限没听清」，
 * 本仓的门禁 `reminder-outcome-honesty.test.ts` 还**明文断言**
 * 「本来就没有期限」与「建提醒失败」必须分开说。
 *
 * 会议链没有第三格，于是 `reminderPlanned <= 0` 这一支**把两件事混成一句**：
 * 「行动项没有可解析的期限」。而 §65 实测到 `glm-5.2` 会把
 * 「对比表要在**那之前**准备好」的 due 填成 `"那之前"` ——
 * 用户明明说了期限，产品侧解析不出来，却告诉用户「没期限」。
 *
 * 与 `note-todo-notice.ts` 同口径：**听不清 ≠ 没提**，分开说。
 *
 * @param reminders      成功建成的提醒条数（<= reminderPlanned）
 * @param reminderPlanned **尝试**建提醒的条数；0 表示没有任何一条解析出了时刻
 * @param unresolved     **提了期限但解析不出来**的条数（2026-10-07 新增）
 * @param created        本次**真正新写入** local_todos 的条数（§410 新增）。
 *   不传 = 调用方没提供这一格 ⇒ 逐字退回本文档此前的全部措辞，
 *   让既有判据（3 参调用）一句都不用改。生产唯一调用点必须传。
 * @returns 拼在「已生成当前总结」后的后缀；无需说明时返回空串
 */
export function buildMeetingReminderNote(
  reminders: number,
  reminderPlanned: number,
  unresolved = 0,
  created?: number,
): string {
  // ⚠ §410：「本次一条都没新建」必须**先于**其余各支判掉。
  //
  //   缺陷现场：`createMeetingTodos` 返回四个计数器（count/reminders/
  //   reminderPlanned/unresolved），而 `MeetingDetailView.onSummarize`
  //   解构了**三个** —— `count` 被丢掉。于是「本次一条都没新建」与
  //   「行动项没有可解析的期限」在返回值上**完全同形**（0/0/0）。
  //
  //   而 0/0 不是假想：`alreadyCreatedTodo`（同文件 §153）让「总结」按钮
  //   可以重复点而不重复建，**既有门 `meeting-todo-persist-idempotency`
  //   就断言第二次 `count === 0`**；同时本仓的主路径恰好会走到这里 ——
  //   录完音的 `finalizeRecording` 已经用同一批 liveSummary.actionItems
  //   写进了 local_todos，用户随后点「总结」时每一条都命中查重。
  //
  //   实测那一轮用户看到的是：
  //     「已生成当前总结（行动项没有可解析的期限，未建提醒）」
  //   而事实是：待办在库里（带真实 due_at），提醒也已建成。
  //   ⇒ **产品把自己的状态说反了**，而且是用户唯一能看到的那句话。
  //
  //   为什么不并进下面的分支：`created===0 ⇒ reminders===0`
  //   （reminders 只在 INSERT 成功之后才 ++），所以这一支与
  //   「提醒全失败」「没有期限」三态天然互斥，不会掩盖真实的提醒失败。
  if (created !== undefined && created <= 0) {
    return '（行动项此前已加入待办，本次未重复添加）'
  }
  // 「期限没听清」独立成句：它与「没有期限」是两件事。
  const miss = unresolved > 0 ? `，${unresolved} 条期限没听清` : ''
  if (reminderPlanned <= 0) {
    // 两句必须分岔：unresolved>0 说明用户**确实说了期限**，是我们没听懂。
    return unresolved > 0
      ? `（${unresolved} 条期限没听清，未建提醒）`
      : '（行动项没有可解析的期限，未建提醒）'
  }
  if (reminders <= 0) return `，但 ${reminderPlanned} 条日程提醒未能创建${miss}`
  if (reminders < reminderPlanned) return `（${reminders}/${reminderPlanned} 条提醒已建${miss}）`
  return miss
}

/**
 * §129：**一条行动项都没拿到**时的后缀。
 *
 * 缺陷现场：`onSummarize` 里 `buildMeetingReminderNote` 只在 `if (items.length)`
 * **里面**被调，所以「根本没试过」与「试过了但一条没建成」文案完全相同；
 * 而 `items` 取自 `liveSummary.value?.actionItems` —— 内存 ref 在首轮
 * `refresh()` 完成前恒为 null，于是首次进页面就按「总结」时**建 0 条待办**，
 * toast 仍然照弹「已生成当前总结」。
 *
 * ⚠ 两句必须分岔，否则用户分不出「模型没给出行动项」与「我们还没拿到总结」。
 *
 * ── §416：那句「再点一次」原本是一个**走不通**的指引 ──
 *
 * 此前 `sourceReady=false` 无论录音是否还在继续，都许一句「再点一次「总结」」。
 * 但 `onSummarize` 里那次点击**只重写纪要正文**（`summarizeMeeting` → 写
 * `meeting.summary`），行动项恒取自 `liveSummary ?? meeting.liveSummary`
 * —— 这两条链之间**没有任何调用关系**。⇒ 录音一停，再点多少次都是同一句话、
 * 同一个结果。短会议尤其常见：`useLiveSummary` 的门是
 * `newCount < 3 && elapsed < 30s ⇒ 跳过`，一场不到 30 秒 / 3 段的会
 * **永远**不会有实时总结，于是这句指引在一个常见场景里**必然无效**。
 *
 * 这不是「措辞不好」，是把用户指到一个不存在的地方 ——
 * 与 `meeting-recording-finalize.ts` 里那句「精翻稍后可在会议详情重试」
 * 被判缺陷是同一条纪律（那里同样没有那个入口）。
 *
 * ⇒ 承诺必须随录音状态分岔：还在录 ⇒ 实时总结迟早会来，指路成立；
 *   已停 ⇒ 说清行动项从哪来，并明确再点一次补不出来。
 *
 * @param sourceReady true = 总结源拿到了（其中确实没有行动项）；false = 源尚未就绪
 * @param recording   **此刻是否仍在录音**。缺省 false = 保守按「已结束」说话。
 *   ⚠ 缺省选 false 而不是 true：未知状态下许一个承诺，比说「补不出来」更危险。
 * @returns 拼在「已生成当前总结」后的后缀
 */
export function buildNoActionItemsNote(sourceReady: boolean, recording = false): string {
  if (sourceReady) return '（这次总结没有给出行动项，未建待办）'
  // 还在录：分段会继续增长 ⇒ 实时总结迟早触发 ⇒ 这时「再点一次」是真话。
  if (recording) {
    return '（实时总结还没生成出来，这次一条行动项都没拿到、未建待办；继续录着，等实时总结出来后再点一次「总结」）'
  }
  // 已停：行动项只来自录音中的即时总结，这条链已经不会自己跑了。
  return '（实时总结还没生成出来，这次一条行动项都没拿到、未建待办；待办来自录音中的即时总结，这场没生成出来，再点「总结」也补不出来）'
}
