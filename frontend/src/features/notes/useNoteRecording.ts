/**
 * useNoteRecording — NoteRecorderRuntime 的页面级薄壳(2026-09-20 P0)。
 *
 * 录音所有权在进程级单例 native/recordingRuntime.ts:NoteListView 被
 * KeepAlive 驱逐或应用级导航离开时录音不断;停止只属于用户显式操作。
 * 页面不在场时 stop() 的产物暂存 runtime.pendingResult,重进后用
 * consumePendingResult() 补建语音草稿。
 */
import { noteRecorderRuntime as rt } from '../../native/recordingRuntime'
import type { RecordingPhase } from './note-recording'

export function useNoteRecording() {
  return {
    phase: rt.phase,
    elapsedMs: rt.elapsedMs,
    transcript: rt.transcript,
    error: rt.error,
    recording: rt.recording,
    start: () => rt.start(),
    stop: () => rt.stop(),
    toggle: () => rt.toggle(),
    consumePendingResult: () => rt.consumePendingResult(),
  }
}

export type { RecordingPhase }
