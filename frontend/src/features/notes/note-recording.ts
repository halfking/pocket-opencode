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
}

export function noteRecorderUiState(phase: RecordingPhase): NoteRecorderUiState {
  if (phase === 'recording') {
    return { busy: false, ariaLabel: '停止录音', statusText: '' }
  }
  if (phase === 'stopping') {
    return { busy: true, ariaLabel: '正在转写录音，请稍候', statusText: '正在转写录音，请稍候…' }
  }
  return { busy: false, ariaLabel: '开始录音', statusText: '' }
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
