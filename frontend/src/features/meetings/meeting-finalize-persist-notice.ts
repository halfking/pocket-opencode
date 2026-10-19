/**
 * meeting-finalize-persist-notice.ts — 会议收尾**写库失败**与「遗留录音」两件事的
 * 判定与文案（纯函数，无副作用）。
 *
 * ── D1：收尾写库失败是会议链上唯一一条「完全没被包起来」的落库调用（§834）──
 *   `NoteRecorderRuntime` / `MeetingRecorderRuntime` 的 stop 收尾里有三处落库：
 *     ① `saveMeetingAudio` —— try/catch + 落盘失败如实写 null（§820）；
 *     ② `syncMeetingMetadata` —— `runBestEffort` 包着（注释明写「失败不得冒泡
 *        成未处理拒绝」）；
 *     ③ `updateMeeting(id, { status: 'completed', … })` —— **没有 try/catch**。
 *
 *   ③ 抛错的连锁后果（本模块存在的全部理由）：
 *     · 异常冒泡出 `stop()` ⇒ `RecordingPill.onStop` 的
 *       `await …stop()` 直接中断 ⇒ **`toast.success('会议录音已结束并保存')`
 *       不会执行** ⇒ 用户点完停止，界面上什么都没有；
 *     · `this.activeMeetingId.value = ''` 在它**之后** ⇒ runtime 仍认为这场
 *       会议在录；
 *     · `status: 'completed'` 是全仓**唯一**写这一状态的地方 ⇒ 这场会议**永久**
 *       停在 `recording` ⇒ 列表页一直显示「录音中」。
 *
 * ⚠ 与 §832/§833 同族但**后果更重**：那两处失败后草稿/音频还在，
 *   这里失败后「完成」这个事实本身就**没被记下来**，而它又是 D2 的输入。
 *
 * ── D2：`status === 'recording'` 在本仓库有**两套相反的契约**（§834）──
 *   · `meetings-live.ts::getRecordingBySession` 的注释：「杀进程后提示恢复，
 *     **不自动重开采集**」；
 *   · `useSessionLiveRecord` 真的这么做了：查到遗留行 ⇒ `toast.warning`
 *     一句「上次录音未正常结束，点麦克风可开始新的实时记录（不会自动重开采集）」；
 *   · 而 `MeetingDetailView.onMounted` 却写着
 *     `const auto = … || m.status === 'recording'; if (auto) await onMicToggle()`
 *     ⇒ **静默自动开麦**。
 *
 *   ⇒ 同一个状态值、同一个仓库，两处契约相反，自动开麦那处没有任何说明。
 *   后果：重开一场「卡在 recording」的会议 ⇒ 麦克风授权弹窗 + **把新录音
 *   混进同一场会议**（`seedSegments(storedSegments)` + `start({resume:true})`），
 *   用户完全没打算录。
 *   ⚠ 删掉这条自动分支**不会**破坏进程内续录：麦克风还开着时 `micOn` 为真，
 *   UI 本来就显示在录；这条分支只在麦克风已关时才触发（进程被杀 / 收尾写库失败）。
 *
 * ⇒ 两件事共用一个模块，是因为它们是**同一条因果链的上下游**：D1 让状态卡住，
 *   D2 让卡住的状态自动开麦。文案也放一起，便于两侧共用同一句提示。
 */

/** D1：收尾写库失败时给用户看的那句话。空串 = 不该提示。 */
export function describeMeetingFinalizePersistFailure(_input: { error?: unknown } = {}): string {
  return '会议已停止录音，但这场会议的「已结束」状态没能存下来，音频与文字都还在本地。'
    + '请不要直接关掉页面：重新打开这场会议前，它可能仍被当成进行中的录音。'
    + '若列表里一直显示「录音中」，可以在会议详情页手动结束一次。'
}

/** D1：诊断前缀，与 §820 的落盘失败前缀区分开。 */
export const MEETING_FINALIZE_PERSIST_FAIL_TAG = '[meeting-recorder] 收尾写库失败（音频与分段仍在本地，会议状态没写成 completed）：'

/**
 * D2：遗留录音提示语。
 *
 * ⚠ 刻意与 `useSessionLiveRecord` 里的原句**逐字相同** —— 那是仓库里对
 *   「遗留 recording 不自动重开采集」这件事的**既有用户可见承诺**，
 *   而 `MeetingDetailView` 此前违背了它。两处引用同一常量 ⇒ 文案不会再各自漂移。
 */
export const LEFTOVER_RECORDING_HINT =
  '上次录音未正常结束，点麦克风可开始新的实时记录（不会自动重开采集）'
