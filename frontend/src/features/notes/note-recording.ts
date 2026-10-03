export type RecordingPhase = 'idle' | 'recording' | 'stopping'

/**
 * 录音停止后的界面状态。
 *
 * 为什么需要它：`runStop()` 在**第一行**就把 `recording` 置 false（这样麦克风
 * 立刻释放、计时器立刻停），但它自己还要同步等一段**最长 10 分钟**的兜底转写
 * （transcribeFull，客户端超时 11 分钟 / 外层 withTimeout 10 分钟）。这段窗口里：
 *
 *   · `recording === false` → FAB 立刻变回「🎤 开始录音」的空闲外观；
 *   · `NoteRecordingStudio` 被 `v-if="isRecording"` 卸载，实时文本消失；
 *   · `error` 仍是空的（它只在**失败**时才写）；
 *   · 再点 FAB → `toggle()` 看到 phase==='stopping' 直接返回进行中的 promise，
 *     视觉上毫无变化。
 *
 * 结果就是用户报的「点击停止没反应」——最长 10 分钟的零反馈。
 * 这里把「收尾中」这个状态显式化，让界面能说出它在干什么。
 */
export interface NoteRecorderUiState {
  /** FAB 是否应显示为不可再点的忙碌态。 */
  busy: boolean
  /** FAB 的无障碍标签。 */
  ariaLabel: string
  /** 列表页要显示的状态文案；空串 = 不显示。 */
  statusText: string
  /**
   * 状态行是否要给出「停止转写」入口。
   *
   * 只在 'stopping' 为真：那正是兜底转写在跑（最长 10 分钟）的窗口，此前
   * 全应用在这一段**没有任何中止手段**——需求「后台执行的 api 可以强行
   * 终止」在这里是空的，用户只能干等或重启应用。
   */
  canCancel: boolean
}

export function noteRecorderUiState(phase: RecordingPhase): NoteRecorderUiState {
  if (phase === 'recording') {
    return { busy: false, ariaLabel: '停止录音', statusText: '', canCancel: false }
  }
  if (phase === 'stopping') {
    return {
      busy: true,
      ariaLabel: '正在转写录音，请稍候',
      statusText: '正在转写录音，请稍候…',
      canCancel: true,
    }
  }
  return { busy: false, ariaLabel: '开始录音', statusText: '', canCancel: false }
}

export function formatRecordingClock(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000))
  const mm = String(Math.floor(total / 60)).padStart(2, '0')
  const ss = String(total % 60).padStart(2, '0')
  return `${mm}:${ss}`
}

export function nextRecordingState(
  phase: RecordingPhase,
  event: 'toggle' | 'drafted',
): RecordingPhase {
  if (event === 'drafted') return 'idle'
  if (phase === 'idle') return 'recording'
  if (phase === 'recording') return 'stopping'
  return 'stopping'
}

export function appendTranscript(
  committed: string,
  _display: string,
  partial: string,
  finalChunk = '',
): { committed: string; display: string } {
  const nextCommitted = finalChunk ? `${committed}${finalChunk}` : committed
  return {
    committed: nextCommitted,
    display: `${nextCommitted}${partial}`,
  }
}

/**
 * 「一个音都没录到」时的收尾提示文案。
 *
 * 单独导出成常量而不是就地写在 runtime 里，是为了让
 * stt-error-render-chain 的「error 写入点必须面向用户」不变量能**放行并校验**
 * 这一处：常量名进白名单，同时那条护栏会断言它的定义确实是字符串字面量。
 * 否则只能二选一——要么把文案复制一份到 runtime（两份真相），要么放宽不变量。
 */
export const EMPTY_RECORDING_NOTICE = '没有录到声音，麦克风可能已被占用或静音；请检查后重试'

/**
 * 「一个音都没录到」时的收尾提示；空串 = 不该提示。
 *
 * ## 为什么需要它
 *
 * 2026-10-03 审计发现：这条分支原来什么都不写。触发条件是 `chunks` 为空
 * ——部分 Android WebView 的 MediaRecorder 既不派 `onstop` 也不派
 * `ondataavailable`（stop() 里那个 3 秒兜底正是为这种 WebView 准备的）。
 * 此时：
 *
 *   · 兜底转写整段被跳过（它要求 `audioBlob.size > 0`）；
 *   · `error` 保持空串；
 *   · `pendingResult.text` 是空串，而 NoteListView 的
 *     `if (pending && pending.text.trim())` 会把它**静默丢弃**。
 *
 * 于是用户点了停止：没有草稿、没有错误提示、音频也是空的，整个过程像是
 * 没发生过——正是被报成「点击停止没有办法停止，语音没有转成文字」的那个现象。
 *
 * ## 判据为什么同时要求「没有文字」且「没有音频」
 *
 * 只按音频为空判会误伤「原生流式已经转出文字、只是没攒到分片」那条路径：
 * 那条是**有成果**的，应当建草稿而不是报错。只按文字为空判又会漏掉
 * 「文字空、音频非空、但兜底转写被跳过」的情况。
 *
 * 抽成纯函数而不是就地写 if：判据本身要能被真正执行到。
 * 就地 if 只能靠源码结构断言守着，而结构断言分不清「代码在跑」和
 * 「注释里写过同样的话」——那正是本轮前面几次负控假红的来源。
 */
export function emptyRecordingNotice(hasText: boolean, audioBytes: number): string {
  if (hasText) return ''
  if (audioBytes > 0) return ''
  return EMPTY_RECORDING_NOTICE
}
