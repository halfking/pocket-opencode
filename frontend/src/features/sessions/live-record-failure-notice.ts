/**
 * live-record-failure-notice.ts — 会话内实时录音链的失败形态与文案（纯函数，无副作用）。
 *
 * ── 为什么它是缺陷（§839）──
 *   `useSessionLiveRecord` 的三个动作此前都是裸的：
 *
 *   ① **建会议失败**（`createMeeting` 抛错）
 *      ⇒ 用户点麦克风，**什么都不会发生**，也没有任何提示。
 *      而 `toggle()` 是被 `@live-record="liveRecord.toggle"` **直接**绑成事件处理器的
 *      ⇒ 一个未处理的 rejection 就到此为止。
 *
 *   ② **启动失败后的收尾被显式静默**（`.catch(() => {})`）
 *        await updateMeeting(m.id, { status: 'completed' }).catch(() => {})
 *      ⇒ 这一行存在的理由，代码自己的注释写得很清楚：「否则遗留的 recording
 *        状态会在每次进入会话时触发『上次录音未正常结束』警告」。
 *      ⇒ **它要防的那个后果，恰恰是它自己失败时会发生的后果**：
 *        清理写失败 ⇒ 会议永久停在 `recording`
 *          · 会议列表一直显示「录音中」；
 *          · **此后每次进入这个会话**都会弹「上次录音未正常结束」——
 *            而用户根本没录成，这是一条**误导性**的警告。
 *      ⇒ 与 §834/D1 同源（那一次是 stop 的收尾，这次是 start 的收尾），
 *        但这里更隐蔽：它是**被人主动 `.catch(() => {})` 掉的**。
 *
 *   ③ **收尾时读不回分段**（`getMeetingWithSegments` 抛错）
 *      ⇒ `finalizeRecording`（整段重转 → 说话人归因 → 精校 → 行动项 → 总结）
 *        **整段不发生**，而此时录音已经停止、状态已写 `completed`、
 *        录音面板也已关闭 ⇒ 用户看到的是「正常结束」。
 *
 *   ⚠ ③ 的处置**刻意是不继续往下跑**：拿不到分段还去跑收尾编排，
 *     等于把一份**残缺的输入**交给一条会重写 `transcript` 的链
 *     ⇒ 宁可不做，也不要在读失败时改写已有正文。
 */

/** 失败发生在会话录音链的哪一步。 */
export type LiveRecordStage = 'create' | 'cleanup' | 'load-segments'

export function describeLiveRecordFailure(
  stage: LiveRecordStage,
  _input: { error?: unknown } = {},
): string {
  switch (stage) {
    case 'create':
      return '没能开始录音：会议记录没有创建成功（本地存储写入失败）。'
        + '请检查本地存储空间后重试 —— 这次没有留下任何内容，会议列表里也不会多出一条。'
    case 'cleanup':
      return '录音没能开始，而这次尝试留下的会议记录没能收尾，'
        + '它会一直显示「录音中」，而且以后每次进入本会话都会提示「上次录音未正常结束」。'
        + '请到会议列表把这条记录删掉。'
    case 'load-segments':
      return '录音已停止并保存，但收尾处理没有执行：没能读回已保存的分段，'
        + '所以整段校对、说话人归因与行动项都没有生成。'
        + '录音本身完好，可在会议详情页重新生成总结。'
    default:
      return '会话录音这一步没能完成，请重试；若反复失败请检查本地存储空间。'
  }
}

/** 诊断前缀。 */
export const LIVE_RECORD_FAIL_TAG = '[session-live-record] 会话录音链失败：'
