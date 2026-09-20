/**
 * recordingPolicy — 录音启动/互斥纯决策(2026-09-20 P0 录音后台化)。
 *
 * 拆成无依赖模块以便 node --test 直接覆盖(recordingRuntime.ts 的 import
 * 链含浏览器全局,Node 下不可加载)。决策语义见 native/recordingRuntime.ts。
 */

export interface MeetingStartInput {
  /** 目标会议 id。 */
  meetingId: string
  /** runtime 全局是否正在录(任意归属)。 */
  meetingRecording: boolean
  /** 当前录音归属的会议 id;空 = 没有会议录音进行中。 */
  activeMeetingId: string
  /** 正在执行 stop() 收尾(顶替/重入都不允许)。 */
  stopping: boolean
  /** 笔记录音是否占用麦克风。 */
  noteRecording: boolean
  /** 调用方传入的 resume 标志(同场会议续录)。 */
  resume?: boolean
}

export type MeetingStartDecision =
  | { action: 'noop-attached'; ok: true }        // 已在录同一场会议(页面重进)
  | { action: 'reject-stopping'; ok: false }     // stop 收尾中
  | { action: 'reject-note-busy'; ok: false }    // 笔记录音占用麦克风
  | { action: 'start-fresh'; ok: true; freshSession: true }   // 全新录音
  | { action: 'replace-other'; ok: true; freshSession: true } // 顶替别的会议(先收尾旧的)
  | { action: 'resume-self'; ok: true; freshSession: false }  // 同场会议续录(切设备)

export function decideMeetingStart(input: MeetingStartInput): MeetingStartDecision {
  const { meetingId, meetingRecording, activeMeetingId, stopping, noteRecording, resume } = input
  if (!meetingId) return { action: 'reject-stopping', ok: false }
  if (meetingRecording && activeMeetingId === meetingId) return { action: 'noop-attached', ok: true }
  if (stopping) return { action: 'reject-stopping', ok: false }
  const replacingOther = meetingRecording
  if (!replacingOther && noteRecording) return { action: 'reject-note-busy', ok: false }
  // resume 只对"同一场会议、采集已断(如切设备后重开)"有效;顶替别的会议
  // 一律是新会话 —— 否则新会议会继承旧会议的残留分段。
  if (replacingOther) return { action: 'replace-other', ok: true, freshSession: true }
  if (resume) return { action: 'resume-self', ok: true, freshSession: false }
  return { action: 'start-fresh', ok: true, freshSession: true }
}

export interface NoteStartInput {
  /** idle/recording/stopping 状态机位。 */
  phase: 'idle' | 'recording' | 'stopping'
  /** 会议录音是否占用麦克风。 */
  meetingRecording: boolean
}

export type NoteStartDecision =
  | { action: 'noop-busy-phase'; ok: false }   // 非 idle(已在录/收尾中)
  | { action: 'reject-meeting-busy'; ok: false }
  | { action: 'start'; ok: true }

export function decideNoteStart(input: NoteStartInput): NoteStartDecision {
  if (input.phase !== 'idle') return { action: 'noop-busy-phase', ok: false }
  if (input.meetingRecording) return { action: 'reject-meeting-busy', ok: false }
  return { action: 'start', ok: true }
}
