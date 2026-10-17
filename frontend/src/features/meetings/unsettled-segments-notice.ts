/**
 * unsettled-segments-notice.ts — 「停止时有分段还没转完」的诊断文案（纯函数，无副作用）。
 *
 * ── 缺陷（§830）──
 *   `recordingRuntime.stop()` 里等在途分段落库有** 10 秒上限**（防挂死，合理），
 *   但超时之后它**继续往下走**，而且**一声不吭**：
 *
 *     · `mergeOverSplitSpeakers()` 用**当下**的 `this.segments` 重渲染正文；
 *     · `status: 'completed'` 照写；
 *     · 调用方紧接着的 `finalizeRecording()` 从 **DB** 读分段做整段重转与精校。
 *
 *   ⇒ 晚到的那几段**不在精校输入里**。而 `resolveRefinedView` 在精校成功时
 *   展示的是 `refinedTranscript`（徽章「已精校」）⇒ 用户看到一份**缺了最后几句**、
 *   却标着「已精校」的正文。
 *
 *   ⚠ 那几段**会**在稍后自己补写 `transcript`（`ingestSpeechBlob` 末尾会重渲染），
 *   但精校视图**不展示**那条路 ⇒ 「数据在、但用户看不到」是最难发现的一种残缺。
 *
 * ⚠ 抽成纯模块是为了能测（§52）：`recordingRuntime.ts` 是类方法，node 里跑不到；
 *   文案只写在调用点的话，判据只能扫 `console.warn` 的语法形状（§818 实测零牙）。
 */

/** 停止时的在途分段等待上限（毫秒）。与调用点用的是同一个值。 */
export const UNSETTLED_SEGMENT_LIMIT_MS = 10_000

export interface UnsettledSegmentsInput {
  /** 超时那一刻仍在途的分段数。 */
  pending: number
  /** 等待上限（毫秒），写进文案便于对照日志。 */
  limitMs?: number
}

/**
 * 生成诊断文案。
 *
 * 刻意点明三件事：还剩几段、它们**没有**进精校、以及**去哪儿看**它们
 * —— 否则读者会以为「录音结束时那几句彻底没了」，进而做出错误的补救动作。
 */
export function describeUnsettledSegments(input: UnsettledSegmentsInput): string {
  const pending = Number.isFinite(input.pending) ? Math.max(0, Math.round(input.pending)) : 0
  const limit = Number.isFinite(input.limitMs as number) ? Math.round(input.limitMs as number) : UNSETTLED_SEGMENT_LIMIT_MS
  return `[meeting-recorder] 停止时仍有 ${pending} 段转写未完成（已等 ${limit}ms 上限）：`
    + '这几段**不会**进入本次整段重转与精校，'
    + '而精校视图默认展示精校后的正文 ⇒ 用户看到的最后几句会比实际录到的少。'
    + '它们稍后仍会补进原始转写，'
    + '可在会议详情点「看原文」（MeetingDetailView 的 refined/original 切换）查看。'
}
