/**
 * voice-draft-save-notice.ts — 「语音草稿没能存下来」的诊断文案（纯函数，无副作用）。
 *
 * ── 缺陷（§832）──
 *   `NoteListView` 里两条「录音结束 → 建草稿」的路径**都没有 try/catch**：
 *
 *     · `onMicToggle()`            —— 页面在场时点停止
 *     · `pickupPendingRecording()` —— 跨页停止后回到列表页补建（`onMounted`）
 *
 *   而建草稿要走 `notesStore.createNote` → `persistNotePayload`
 *   （原生 `Filesystem.writeFile` / Web `localDB.run` 写 base64）
 *   ⇒ **存储写失败就一路抛到 `onMicToggle` 的调用方**，而前端**没有** `unhandledrejection` 兜底。
 *
 *   ⇒ 用户看到的现象是：**录音正常结束、界面恢复原样、什么都没有** ——
 *     没有笔记、没有报错、没有提示。整段录音白录，而用户完全不知道它存在过。
 *
 *   ⚠ 与 §820 同族但形态更重：§820 至少还留下了一条「谎报完好」的会议记录；
 *     这里**连记录都不存在**。
 *
 * ⚠ 抽成纯模块是为了能测（§52）：`.vue` 组件在 node 里跑不到，
 *   而这条文案恰恰是判据唯一能验的东西。
 */

/** 用户能采取的动作只有一个：重录。措辞必须把这层意思说清。 */
export interface VoiceDraftSaveFailureInput {
  /** 原始异常。 */
  error: unknown
  /** 是「跨页补建」还是「页面在场」，两者文案里点明的上下文不同。 */
  via?: 'stop-button' | 'pending-pickup'
}

/** 从异常里取一句人话；取不到就给兜底，绝不产出 `undefined`/`[object Object]`。 */
function reasonOf(e: unknown): string {
  if (e instanceof Error && e.message.trim()) return e.message.trim()
  if (typeof e === 'string' && e.trim()) return e.trim()
  return '未知原因'
}

export function describeVoiceDraftSaveFailure(input: VoiceDraftSaveFailureInput): string {
  const reason = reasonOf(input.error)
  const where = input.via === 'pending-pickup'
    ? '这次录音的草稿没能保存（回到列表页时补建失败）'
    : '这次录音的草稿没能保存'
  return `${where}：${reason}。`
    + '录音已停止且不会自动恢复 —— 请先复制走已转写出的文字，再重录一次。'
    + '（常见成因：设备存储空间不足、或本地数据库写入被系统中断。）'
}
