/**
 * meeting-append-notice.ts — 「把一句话写进会议记录」失败时的形态判定与文案（纯函数）。
 *
 * ── 为什么它是缺陷（§836）──
 *   §131 一次加了两条用户写入路径：
 *     ① **更正**一段已有转写（`onEditSegment`）
 *     ② **追加**一句新的话（`onAppendUtterance`）
 *   ① 当时就有 `try/catch` + `toast.error`；② **完全没有**。
 *   ⇒ 同族缺陷的「另一半」一直没被看见。
 *
 *   ② 那条路径上有**三次串行写库**，且失败形态不止一种：
 *     · 录音中：`rt.appendText` 内部 = `saveSegment` + `updateTranscript`；
 *     · 录完后：`saveSegment`（补落库分支，§131.2 修过「点了没反应」）+
 *       `updateTranscript` + `refreshRelated`。
 *   ⇒ 一律静默时，用户面对的是三种**完全不同**的处境：
 *     A. **什么都没存**（`saveSegment` 抛错）⇒ 他打的那句话凭空消失；
 *     B. **存了一半**（`saveSegment` 成功、`updateTranscript` 抛错）
 *        ⇒ 分段表里有这一句，但会议的 `transcript` 列**没有**
 *        ⇒ 界面上看得见、AI 总结/相关推荐/精校**读不到**
 *        ⇒ 两处真相不一致，且没有任何东西提示；
 *     C. **全存下了，只是收尾没做完**（`refreshRelated` 抛错）
 *        ⇒ 用户会以为追加失败而重复操作。
 *
 * ── 第二处：实时字幕的 commit 回调用 `void` 吞掉 rejection（§836/D2）──
 *   `startLiveCaption` 里是
 *       commit: (text) => { void this.appendText(text) }
 *   ⇒ `appendText` 抛错时这里**什么都不发生**：没有 console、没有提示、没有分段。
 *   而这条路径是**系统语音识别**在录的每一句话 ⇒ 一整场会议可能一句都没存进去，
 *   用户看到的实时字幕却**一直正常滚动**。
 */

/** 失败发生在哪一步 —— 决定文案，因为三者的用户处境完全不同。 */
export type AppendStage = 'runtime' | 'segment' | 'transcript' | 'related'

export function describeAppendFailure(stage: AppendStage, _input: { error?: unknown } = {}): string {
  switch (stage) {
    case 'runtime':
      return '这句话没能保存到会议记录里（存储写入失败），请再说一次。'
    case 'segment':
      return '这句话没能保存到会议记录里（分段没有写进去），请再说一次。'
    case 'transcript':
      return '这句话已经加进分段了，但会议全文没有同步更新 —— '
        + 'AI 总结、相关推荐与精校暂时读不到它。'
        + '可以在会议详情页重新生成总结来修复，或手动确认这一句是否已包含在正文里。'
    case 'related':
      return '这句话已经加进会议记录了，只是相关推荐没有刷新（不影响正文）。'
    default:
      return '这句话没能保存到会议记录里，请再说一次。'
  }
}

/** D2：实时字幕入库失败的提示（写进 `sttError`，录音中可见）。 */
export const LIVE_CAPTION_PERSIST_FAIL =
  '实时字幕还在显示，但最近几句没有存进会议记录（存储写入失败）'

/** D1 诊断前缀。 */
export const MEETING_APPEND_FAIL_TAG = '[meeting-detail] 追加一句话失败：'

/** D2 诊断前缀。 */
export const LIVE_CAPTION_PERSIST_FAIL_TAG = '[meeting-recorder] 实时字幕入库失败（字幕仍在显示）：'
