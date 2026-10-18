/**
 * meeting-edit-failure-notice.ts — 会议详情页「一次主动编辑」的失败形态与文案（纯函数）。
 *
 * ── 为什么它们是缺陷（§835）──
 *   这一轮抓到两处**用户主动操作**的失败/半失败形态，此前都是完全无声的：
 *
 *   **D1 元信息保存：面板先关，写库后才知道成没成**
 *     `MeetingSettingsSheet.onSave()` 是
 *         emit('save', {...}); emit('close')
 *     —— Vue 的 emit 是**同步**的，父组件的 `onMetaSave` 是 async 且**没人 await**
 *     ⇒ 面板在写库结果出来之前就关掉了，form 随之销毁。
 *     而父侧 `onMetaSave` 里 `await updateMeeting(...)` **没有 try/catch**
 *     ⇒ 写失败时：一个未处理的 rejection、**零反馈**、
 *     而且**用户刚填的标题/主题/地点/参与人/标签已经不在屏幕上了**。
 *     ⇒ 失败与成功在界面上**完全不可区分**：面板关了 = 看起来存好了。
 *     ⚠ 这是诚实性缺陷的第四形态：**UI 主动做出了「已保存」的动作**，
 *       比 §820「谎报存在」更主动 —— 它连动作都替你做了。
 *
 *   **D2 说话人改名：半改半不改，且完全无声**
 *     `recordingRuntime.labelSpeaker()` 的逐段写入是
 *         for (const [segId, pid] of this.segmentProfiles) {
 *           await updateSegmentSpeaker(segId, displayName)   ← 抛错就跳出循环
 *         }
 *     任一段写失败 ⇒ **后面的段一个都没改**，而前面几段**已经改了**
 *     ⇒ 用户看到同一个人在正文里一会儿是新名一会儿是旧名，
 *       且 `onSpeakerLabel` 同样没有 try/catch ⇒ 一个字都不说。
 *     ⚠ 更靠前的一处同样致命：改声纹标签的 `upsertVoiceprintLabel`
 *       写失败会**直接跳过整个 for 循环** ⇒ 一段都没改。
 *
 *   ⚠ 与 §832/§833/§834 同族（「用户做了一件事，失败没有任何信号」），
 *     但 D2 更难被发现：它不是「什么都没发生」，是「**发生了一半**」。
 */

/** D2：一次改名在正文里的落点统计。 */
export interface SpeakerLabelOutcome {
  /** 成功改写库的分段数。 */
  renamed: number
  /** 写库失败、被跳过的分段数。 */
  failed: number
  /** 声纹标签那一行（profile）是否写失败。 */
  profileFailed: boolean
}

export type SpeakerLabelTarget = readonly (readonly [segId: string, profileId: string])[]

/**
 * 逐段回填说话人名，**一段失败不打断其余**。
 *
 * ⚠ 写成「跳过失败继续」而不是「遇错即抛」是有代价的：
 *   抛错会让整批回滚吗？**不会** —— 已经写成功的那几段不会回滚（无事务），
 *   所以「抛错」并不会带来一致性，只会丢掉「哪几段没改成」这个信息。
 *   ⇒ 与其假装原子，不如把真实结果统计出来交给用户。
 *
 * @param write 注入的写入函数（单段落库）。测试里可换成会抛错的假实现。
 */
export async function applySpeakerLabelToSegments(
  targets: SpeakerLabelTarget,
  profileId: string,
  displayName: string,
  write: (segId: string, displayName: string) => Promise<void>,
): Promise<{ renamed: number; failed: number; failedIds: string[] }> {
  let renamed = 0
  let failed = 0
  const failedIds: string[] = []
  for (const [segId, pid] of targets) {
    if (pid !== profileId) continue
    try {
      await write(segId, displayName)
      renamed += 1
    } catch {
      // ⚠ 刻意只记不抛：见上面的取舍说明。一段失败不该让其余段停在旧名上。
      failed += 1
      failedIds.push(segId)
    }
  }
  return { renamed, failed, failedIds }
}

/**
 * D2 的文案。空串 = 不该提示（全部成功）。
 *
 * ⚠ 刻意区分「全没改成」与「改了一部分」：两者的用户动作不一样 ——
 *   全没改成 ⇒ 大概率是存储整体不可写，重试同样会失败；
 *   改了一部分 ⇒ 重试可能成功，且用户需要知道**哪些**没对上。
 */
export function describeSpeakerRenameFailure(outcome: SpeakerLabelOutcome): string {
  if (!outcome.profileFailed && outcome.failed === 0) return ''
  const { renamed, failed, profileFailed } = outcome
  if (renamed === 0) {
    return '说话人改名没有生效：本地库没能写入，正文里的名字仍是旧的。'
      + (profileFailed ? '（声纹标签与分段名都没写成）' : '')
      + '录音仍在继续，可以稍后再改一次；若反复失败请检查本地存储空间。'
  }
  const tail = profileFailed
    ? '；声纹标签本身也没写成，这个名字下次不会被记住'
    : ''
  return `说话人改名只生效了一部分：${renamed} 段已改，${failed} 段没能写进本地库`
    + `（刷新后那几段仍显示旧名）${tail}。录音仍在继续，可稍后再改一次。`
}

/** D1：元信息保存失败的文案。 */
export function describeMeetingMetaSaveFailure(_input: { error?: unknown } = {}): string {
  return '会议信息没能保存到本地，面板保持打开以便你重试。'
    + '你刚填的内容还在输入框里，请检查本地存储空间后再保存一次。'
}

/** D1 诊断前缀。 */
export const MEETING_META_SAVE_FAIL_TAG = '[meeting-detail] 元信息保存失败：'

/** D2 诊断前缀（与 §834 的收尾写库失败区分开）。 */
export const SPEAKER_RENAME_FAIL_TAG = '[meeting-detail] 说话人改名未全部生效：'
